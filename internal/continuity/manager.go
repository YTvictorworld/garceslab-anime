package continuity

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/samber/mo"
	"seanime/internal/database/db"
	"seanime/internal/util/filecache"
	"sync"
	"time"
)

const (
	OnlinestreamKind   Kind = "onlinestream"
	MediastreamKind    Kind = "mediastream"
	ExternalPlayerKind Kind = "external_player"
)

type (
	// Manager is used to manage the user's viewing history across different media types.
	Manager struct {
		fileCacher                  *filecache.Cacher
		db                          *db.Database
		watchHistoryFileCacheBucket *filecache.Bucket

		externalPlayerEpisodeDetails mo.Option[*ExternalPlayerEpisodeDetails]

		logger   *zerolog.Logger
		settings *Settings
		mu       sync.RWMutex

		// profiles-hook: pluggable history storage (see store.go)
		store         HistoryStore
		backgroundCtx func() context.Context
		settingsFor   func(ctx context.Context) *Settings
	}

	// ExternalPlayerEpisodeDetails is used to store the episode details when using an external player.
	// Since the media player module only cares about the filepath, the PlaybackManager will store the episode number and media id here when playback starts.
	ExternalPlayerEpisodeDetails struct {
		EpisodeNumber int    `json:"episodeNumber"`
		MediaId       int    `json:"mediaId"`
		Filepath      string `json:"filepath"`
	}

	Settings struct {
		WatchContinuityEnabled bool
	}

	Kind string
)

type (
	NewManagerOptions struct {
		FileCacher *filecache.Cacher
		Logger     *zerolog.Logger
		Database   *db.Database
	}
)

// NewManager creates a new Manager, it should be initialized once.
func NewManager(opts *NewManagerOptions) *Manager {
	watchHistoryFileCacheBucket := filecache.NewBucket(WatchHistoryBucketName, time.Hour*24*99999)

	ret := &Manager{
		fileCacher:                  opts.FileCacher,
		logger:                      opts.Logger,
		db:                          opts.Database,
		watchHistoryFileCacheBucket: &watchHistoryFileCacheBucket,
		settings: &Settings{
			WatchContinuityEnabled: false,
		},
		externalPlayerEpisodeDetails: mo.None[*ExternalPlayerEpisodeDetails](),
	}
	ret.store = &fileCacheHistoryStore{fileCacher: opts.FileCacher, bucket: &watchHistoryFileCacheBucket} // profiles-hook

	ret.logger.Info().Msg("continuity: Initialized manager")

	return ret
}

// SetSettings should be called after initializing the Manager.
func (m *Manager) SetSettings(settings *Settings) {
	if m == nil || settings == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = settings
}

// GetSettings returns the current settings.
func (m *Manager) GetSettings() *Settings {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settingsLocked(m.background()) // profiles-hook: per-profile switch
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

func (m *Manager) SetExternalPlayerEpisodeDetails(details *ExternalPlayerEpisodeDetails) {
	if m == nil || details == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.externalPlayerEpisodeDetails = mo.Some(details)
}
