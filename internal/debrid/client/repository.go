package debrid_client

import (
	"context"
	"fmt"
	"path/filepath"
	"seanime/internal/api/anilist"
	"seanime/internal/api/metadata_provider"
	"seanime/internal/database/db"
	"seanime/internal/database/models"
	"seanime/internal/debrid/alldebrid"
	"seanime/internal/debrid/debrid"
	"seanime/internal/debrid/dummy"
	"seanime/internal/debrid/premiumize"
	"seanime/internal/debrid/realdebrid"
	"seanime/internal/debrid/torbox"
	"seanime/internal/directstream"
	"seanime/internal/events"
	"seanime/internal/hook"
	"seanime/internal/library/playbackmanager"
	"seanime/internal/platforms/platform"
	"seanime/internal/torrents/autoselect"
	"seanime/internal/torrents/torrent"
	"seanime/internal/torrents/trackprobe"
	"seanime/internal/util"
	"seanime/internal/util/result"

	"github.com/rs/zerolog"
	"github.com/samber/mo"
)

var (
	ErrProviderNotSet = fmt.Errorf("debrid: Provider not set")
)

type (
	Repository struct {
		provider               mo.Option[debrid.Provider]
		logger                 *zerolog.Logger
		db                     *db.Database
		settings               *models.DebridSettings
		wsEventManager         events.WSEventManagerInterface
		ctxMap                 *result.Map[string, context.CancelFunc]
		downloadLoopCancelFunc context.CancelFunc
		torrentRepository      *torrent.Repository
		directStreamManager    *directstream.Manager
		dummyDebridEnabled     bool

		playbackManager     *playbackmanager.PlaybackManager
		streamManager       *StreamManager
		completeAnimeCache  *anilist.CompleteAnimeCache
		metadataProviderRef *util.Ref[metadata_provider.Provider]
		platformRef         *util.Ref[platform.Platform]

		autoSelect *autoselect.AutoSelect

		previousStreamOptions mo.Option[*StartStreamOptions]

		directStreamObserver DirectStreamObserver // Garceslab Anime: viewer sessions

		trackProbeCache *trackprobe.Cache // Garceslab Anime: subtitle-aware auto-select
	}

	NewRepositoryOptions struct {
		Logger         *zerolog.Logger
		WSEventManager events.WSEventManagerInterface
		Database       *db.Database

		TorrentRepository   *torrent.Repository
		PlaybackManager     *playbackmanager.PlaybackManager
		DirectStreamManager *directstream.Manager
		MetadataProviderRef *util.Ref[metadata_provider.Provider]
		PlatformRef         *util.Ref[platform.Platform]
		DummyDebridEnabled  bool
		// TrackProbeCacheDir keeps the MKV track lists read by the
		// subtitle-aware auto-select (Garceslab Anime; "" = no cache).
		TrackProbeCacheDir string
	}
)

func NewRepository(opts *NewRepositoryOptions) (ret *Repository) {
	ret = &Repository{
		provider:       mo.None[debrid.Provider](),
		logger:         opts.Logger,
		wsEventManager: opts.WSEventManager,
		db:             opts.Database,
		settings: &models.DebridSettings{
			Enabled: false,
		},
		torrentRepository:     opts.TorrentRepository,
		platformRef:           opts.PlatformRef,
		playbackManager:       opts.PlaybackManager,
		dummyDebridEnabled:    opts.DummyDebridEnabled,
		metadataProviderRef:   opts.MetadataProviderRef,
		completeAnimeCache:    anilist.NewCompleteAnimeCache(),
		ctxMap:                result.NewMap[string, context.CancelFunc](),
		previousStreamOptions: mo.None[*StartStreamOptions](),
		directStreamManager:   opts.DirectStreamManager,
		trackProbeCache:       trackprobe.NewCache(opts.TrackProbeCacheDir, 0),
	}

	ret.streamManager = NewStreamManager(ret)

	ret.autoSelect = autoselect.New(&autoselect.NewAutoSelectOptions{
		Logger:            opts.Logger,
		TorrentRepository: opts.TorrentRepository,
		MetadataProvider:  opts.MetadataProviderRef,
		Platform:          opts.PlatformRef,
		OnStatusCtx: func(ctx context.Context, status autoselect.StreamAutoSelectStatusPayload) {
			if clientId := autoselect.StatusClient(ctx); clientId != "" { // Garceslab Anime: direct stream
				opts.WSEventManager.SendEventTo(clientId, events.StreamAutoSelectStatus, status)
				return
			}
			opts.WSEventManager.SendEvent(events.StreamAutoSelectStatus, status)
		},
	})

	return
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

func (r *Repository) startOrStopDownloadLoop() {
	// Cancel the previous download loop if it's running
	if r.downloadLoopCancelFunc != nil {
		r.downloadLoopCancelFunc()
	}

	// Start the download loop if the provider is set and enabled
	if r.settings.Enabled && r.provider.IsPresent() {
		ctx, cancel := context.WithCancel(context.Background())
		r.downloadLoopCancelFunc = cancel
		r.launchDownloadLoop(ctx)
	}
}

func (r *Repository) closeProvider() {
	provider, found := r.provider.Get()
	if !found {
		return
	}

	if closer, ok := provider.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			r.logger.Warn().Err(err).Msg("debrid: Failed to close provider")
		}
	}
}

// InitializeProvider is called each time the settings change
func (r *Repository) InitializeProvider(settings *models.DebridSettings) error {
	r.settings = settings

	if !settings.Enabled {
		r.closeProvider()
		r.provider = mo.None[debrid.Provider]()
		// Stop the download loop if it's running
		r.startOrStopDownloadLoop()
		return nil
	}

	r.closeProvider()

	switch settings.Provider {
	case "torbox":
		r.provider = mo.Some(torbox.NewTorBox(r.logger))
	case "realdebrid":
		r.provider = mo.Some(realdebrid.NewRealDebrid(r.logger))
	case "alldebrid":
		r.provider = mo.Some(alldebrid.NewAllDebrid(r.logger))
	case "premiumize":
		r.provider = mo.Some(premiumize.NewPremiumize(r.logger, &premiumizeHashStore{db: r.db}))
	case "dummy":
		if r.dummyDebridEnabled {
			r.provider = mo.Some(dummy.New(r.logger, r.db))
		} else {
			r.provider = mo.None[debrid.Provider]()
			r.logger.Warn().Msg("debrid: Dummy provider is disabled")
		}
	default:
		r.provider = mo.None[debrid.Provider]()
	}

	if r.provider.IsAbsent() {
		r.logger.Warn().Str("provider", settings.Provider).Msg("debrid: No provider set")
		// Stop the download loop if it's running
		r.startOrStopDownloadLoop()
		return nil
	}

	// Authenticate the provider
	err := r.provider.MustGet().Authenticate(r.settings.ApiKey)
	if err != nil {
		r.logger.Err(err).Msg("debrid: Failed to authenticate")
		r.provider = mo.None[debrid.Provider]()
		// Cancel the download loop if it's running
		if r.downloadLoopCancelFunc != nil {
			r.downloadLoopCancelFunc()
		}
		return err
	}

	// Start the download loop
	r.startOrStopDownloadLoop()

	return nil
}

func (r *Repository) GetProvider() (debrid.Provider, error) {
	p, found := r.provider.Get()
	if !found {
		return nil, ErrProviderNotSet
	}

	return p, nil
}

// premiumizeHashStore implements premiumize.HashStore on top of the app database, so transfer
// hashes survive a restart instead of only living in the provider's in-memory cache.
type premiumizeHashStore struct {
	db *db.Database
}

func (s *premiumizeHashStore) LoadAll() (map[string]string, error) {
	rows, err := s.db.GetDebridTransferHashes("premiumize")
	if err != nil {
		return nil, err
	}

	ret := make(map[string]string, len(rows))
	for _, row := range rows {
		ret[row.TransferID] = row.Hash
	}

	return ret, nil
}

func (s *premiumizeHashStore) Save(transferId, hash string) {
	_ = s.db.UpsertDebridTransferHash("premiumize", transferId, hash)
}

func (s *premiumizeHashStore) Delete(transferId string) {
	_ = s.db.DeleteDebridTransferHash("premiumize", transferId)
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

// AddAndQueueTorrent adds a torrent to the debrid service and queues it for automatic download
func (r *Repository) AddAndQueueTorrent(opts debrid.AddTorrentOptions, destination string, mId int) (string, error) {
	hTorrentItemId, err := triggerOnAddTorrentRequestedHook(&opts, &destination, &mId)
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(destination) {
		return "", fmt.Errorf("debrid: Failed to add torrent, destination must be an absolute path")
	}

	provider, err := r.GetProvider()
	if err != nil {
		return "", err
	}

	torrentItemId := hTorrentItemId
	if torrentItemId == "" {
		// Add the torrent to the debrid service
		torrentItemId, err = provider.AddTorrent(opts)
		if err != nil {
			return "", err
		}
	}

	// Add the torrent item to the database (so it can be downloaded automatically once it's ready)
	// We ignore the error since it's non-critical
	_ = r.db.UpsertDebridTorrentItem(&models.DebridTorrentItem{
		TorrentItemID: torrentItemId,
		Destination:   destination,
		Provider:      provider.GetSettings().ID,
		MediaId:       mId,
	})

	event := &DebridAddTorrentEvent{
		Options:       opts,
		Destination:   destination,
		MediaID:       mId,
		TorrentItemID: torrentItemId,
	}

	_ = hook.GlobalHookManager.OnDebridAddTorrent().Trigger(event)

	return torrentItemId, nil
}

func triggerOnAddTorrentRequestedHook(opts *debrid.AddTorrentOptions, destination *string, mediaID *int) (string, error) {
	requestedEvent := &DebridAddTorrentRequestedEvent{
		Options:     *opts,
		Destination: *destination,
		MediaID:     *mediaID,
	}

	if err := hook.GlobalHookManager.OnDebridAddTorrentRequested().Trigger(requestedEvent); err != nil {
		return "", err
	}

	*opts = requestedEvent.Options
	*destination = requestedEvent.Destination
	*mediaID = requestedEvent.MediaID

	if requestedEvent.DefaultPrevented {
		if requestedEvent.TorrentItemID == "" {
			return "", fmt.Errorf("debrid: add torrent prevented by hook without torrent item id")
		}
		return requestedEvent.TorrentItemID, nil
	}

	return "", nil
}

// GetTorrentInfo retrieves information about a torrent.
// This is used for file section for debrid streaming.
// On Real Debrid, this adds the torrent to the user's account.
func (r *Repository) GetTorrentInfo(opts debrid.GetTorrentInfoOptions) (*debrid.TorrentInfo, error) {
	provider, err := r.GetProvider()
	if err != nil {
		return nil, err
	}

	torrentInfo, err := provider.GetTorrentInfo(opts)
	if err != nil {
		return nil, err
	}

	// Remove non-video files
	torrentInfo.Files = debrid.FilterVideoFiles(torrentInfo.Files)

	return torrentInfo, nil
}

func (r *Repository) HasProvider() bool {
	return r.provider.IsPresent()
}

func (r *Repository) GetSettings() *models.DebridSettings {
	return r.settings
}

func (r *Repository) IsDownloadActive(itemID string) bool {
	if r.ctxMap == nil {
		return false
	}

	return r.ctxMap.Has(itemID)
}

// CancelDownload cancels the download for the given item ID
func (r *Repository) CancelDownload(itemID string) error {
	cancelFunc, found := r.ctxMap.Get(itemID)
	if !found {
		return fmt.Errorf("no download found for item ID: %s", itemID)
	}

	// Call the cancel function to cancel the download
	if cancelFunc != nil {
		cancelFunc()
	}

	r.ctxMap.Delete(itemID)

	// Notify that the download has been cancelled
	r.wsEventManager.SendEvent(events.DebridDownloadProgress, map[string]interface{}{
		"status": "cancelled",
		"itemID": itemID,
	})

	return nil
}

func (r *Repository) StartStream(ctx context.Context, opts *StartStreamOptions) error {
	return r.streamManager.startStream(ctx, opts)
}

// GetStreamURL returns the URL of the relayed stream (upstream's single stream).
func (r *Repository) GetStreamURL() (string, bool) {
	u := r.streamManager.streamURL("")
	return u, u != ""
}

// GetStreamURLFor returns the URL of the client's direct stream, else the relayed one.
func (r *Repository) GetStreamURLFor(clientId string) (string, bool) {
	u := r.streamManager.streamURL(clientId)
	return u, u != ""
}

// CancelStream cancels the relayed stream (upstream behaviour). Direct streams
// of other clients keep going.
func (r *Repository) CancelStream(opts *CancelStreamOptions) {
	r.streamManager.cancelStream(opts)
}

// CancelStreamFor cancels the direct stream of clientId if it has one, else the
// relayed stream (Garceslab Anime).
func (r *Repository) CancelStreamFor(clientId string, opts *CancelStreamOptions) {
	if r.streamManager.cancelClientStream(clientId, opts) {
		return
	}
	r.streamManager.cancelStream(opts)
}

// CancelDirectStream cancels the direct stream of clientId, if any, and reports
// whether there was one. The relayed stream is never touched.
func (r *Repository) CancelDirectStream(clientId string, opts *CancelStreamOptions) bool {
	return r.streamManager.cancelClientStream(clientId, opts)
}

// DirectStreamClientIds returns the clients with a direct stream.
func (r *Repository) DirectStreamClientIds() []string {
	return r.streamManager.directClientIds()
}

// SetProviderForTesting installs a provider without settings (tests and the
// phase 2 E2E harness use fakes; no debrid service is called).
func (r *Repository) SetProviderForTesting(p debrid.Provider) {
	r.provider = mo.Some(p)
	r.settings = &models.DebridSettings{Enabled: true, Provider: p.GetSettings().ID}
}

// SetDirectStreamObserver sets who hears how direct streams went (viewer sessions).
func (r *Repository) SetDirectStreamObserver(f DirectStreamObserver) {
	r.directStreamObserver = f
}

func (r *Repository) GetPreviousStreamOptions() (*StartStreamOptions, bool) {
	r.streamManager.mu.Lock()
	defer r.streamManager.mu.Unlock()
	return r.previousStreamOptions.Get()
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
