package core

// Garceslab Anime: local profiles wiring (see internal/profiles/HOOKS.md).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"seanime/internal/api/anilist"
	"seanime/internal/continuity"
	"seanime/internal/database/db"
	"seanime/internal/database/db_bridge"
	"seanime/internal/database/models"
	"seanime/internal/events"
	"seanime/internal/extension"
	"seanime/internal/library/anime"
	"seanime/internal/platforms/platform"
	"seanime/internal/platforms/shared_platform"
	"seanime/internal/player"
	"seanime/internal/plugin"
	"seanime/internal/profiles"
	"seanime/internal/profiles/history"
	"seanime/internal/profiles/importer"
	"seanime/internal/profiles/localplatform"
	"seanime/internal/profiles/metadata"
	"seanime/internal/profiles/profilews"
	"seanime/internal/profiles/router"
	"seanime/internal/profiles/settings"
	"seanime/internal/user"
	"seanime/internal/util"
	"seanime/internal/util/filecache"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// initProfiles opens profiles.db. Startup fails if it can't be opened, if foreign
// keys aren't enforced or if a migration fails: running without the profile store
// would silently mix user data.
func initProfiles(cfg *Config, logger *zerolog.Logger) *profiles.Service {
	dataDir := cfg.Data.AppDataDir
	if os.Getenv("TEST_ENV") == "true" {
		dataDir = "" // in-memory, like seanime.db in tests
	}
	svc, err := profiles.Open(profiles.Options{
		DataDir: dataDir,
		Enforce: profiles.EnforceFromEnv(),
		Logger:  logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("app: Failed to initialize profiles database")
	}
	svc.StartPurgeLoop()
	logger.Info().Bool("enforce", svc.Enforced()).Msg("profiles: Profiles database ready")
	return svc
}

// ProfilesRuntime is everything the local profiles add at runtime: the router
// platform behind AnilistPlatformRef, the playback lane (phase 1: one global
// playback, owned by the profile that started it), websocket tagging, the
// per-profile settings and watch history.
type ProfilesRuntime struct {
	Service  *profiles.Service
	Router   *router.Router
	Meta     *metadata.Service
	Lane     *profiles.PlaybackLane
	Tags     *profilews.Tags
	Settings *settings.Store
	History  *history.Store
	// DirectAnilist is the anonymous client without the cache layer (public list
	// imports: the cache layer stores every user's collection under one key).
	DirectAnilist anilist.AnilistClient

	lanePlatformRef *util.Ref[platform.Platform]
	laneWS          *profilews.ProfileWS
	logger          *zerolog.Logger

	watchMu     sync.Mutex
	watchRecent map[string]time.Time
}

// profilesPlatformEnabled: the router replaces the AniList/simulated platforms
// unless the server runs in offline mode (desktop feature, not used here).
func profilesPlatformEnabled(cfg *Config) bool {
	return !cfg.Server.Offline
}

// newProfilesRuntime builds the shared anonymous metadata service and the router.
func newProfilesRuntime(svc *profiles.Service, wsEventManager events.WSEventManagerInterface, database *db.Database,
	extensionBankRef *util.Ref[*extension.UnifiedBank], anilistCacheDir string, logger *zerolog.Logger) *ProfilesRuntime {

	direct := anilist.NewAnilistClient("", anilistCacheDir)
	anonRef := util.NewRef[anilist.AnilistClient](direct)
	helper := shared_platform.NewPlatformHelper(extensionBankRef, database, logger)
	rt, err := NewProfilesRuntime(svc, wsEventManager, shared_platform.NewCacheLayer(anonRef), direct, helper, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("app: Failed to initialize the profiles metadata service")
	}
	return rt
}

// NewProfilesRuntime builds the runtime on the given anonymous AniList clients
// (cached for metadata, direct for public list imports). Tests pass a fake.
func NewProfilesRuntime(svc *profiles.Service, wsEventManager events.WSEventManagerInterface, client anilist.AnilistClient,
	direct anilist.AnilistClient, helper *shared_platform.PlatformHelper, logger *zerolog.Logger) (*ProfilesRuntime, error) {
	meta, err := metadata.New(metadata.Options{
		Client: client,
		DB:     svc.DB(),
		Helper: helper,
		Logger: logger,
	})
	if err != nil {
		return nil, err
	}
	if direct.IsAuthenticated() {
		return nil, metadata.ErrAuthenticatedClient
	}
	rt := &ProfilesRuntime{
		Service:       svc,
		Meta:          meta,
		Router:        router.New(router.Options{Meta: meta, DB: svc.DB(), Logger: logger}),
		Lane:          svc.Lane(),
		Tags:          profilews.NewTags(),
		Settings:      settings.NewStore(svc.DB()),
		History:       history.New(svc.DB()),
		DirectAnilist: direct,
		logger:        logger,
		watchRecent:   map[string]time.Time{},
	}
	rt.lanePlatformRef = util.NewRef[platform.Platform](router.NewLanePlatform(rt.Router, rt.Lane.Context))
	rt.laneWS = profilews.New(wsEventManager, rt.Tags, rt.Lane.OwnerID)
	rt.laneWS.BroadcastWithoutProfile = true

	// Plugins act on behalf of the profile that owns the playback (or none).
	plugin.ProfileContext = func() context.Context { return rt.Lane.Context(context.Background()) }
	// Torrent auto-selection during a stream start uses the lane owner's profile.
	db_bridge.ProfileAutoSelectHook = func() (*anime.AutoSelectProfile, bool) {
		ctx := rt.Lane.Context(context.Background())
		pid := profiles.IDFromContext(ctx)
		if pid <= 0 {
			return nil, false
		}
		p, found, err := rt.Settings.AutoSelect(ctx, pid)
		if err != nil || !found {
			return nil, false
		}
		return p, true
	}
	return rt, nil
}

// InstallProfilesRuntime connects the runtime to the app once the modules exist.
func (a *App) InstallProfilesRuntime() {
	rt := a.ProfilesRuntime
	if rt == nil {
		return
	}
	rt.Lane.SetActiveFunc(func() bool {
		if a.MediacoreCoordinator != nil {
			if _, ok := a.MediacoreCoordinator.GetActiveSession(); ok {
				return true
			}
		}
		return false
	})
	rt.Lane.SetOnAcquire(func(p *profiles.Profile) {
		// The playback modules get the collection of the profile that plays.
		ctx := profiles.WithProfile(context.Background(), p)
		col, err := rt.Router.GetAnimeCollection(ctx, false)
		if err != nil {
			return
		}
		if a.PlaybackManager != nil {
			a.PlaybackManager.SetAnimeCollection(col)
		}
		if a.DirectStreamManager != nil {
			a.DirectStreamManager.SetAnimeCollection(col)
		}
	})
	if a.ContinuityManager != nil {
		a.ContinuityManager.SetHistoryStore(rt.History,
			func() context.Context { return rt.Lane.Context(context.Background()) },
			func(ctx context.Context) *continuity.Settings {
				pid := profiles.IDFromContext(ctx)
				if pid <= 0 {
					return nil
				}
				st, err := rt.Settings.Get(ctx, pid)
				if err != nil || st.Library == nil || st.Library.EnableWatchContinuity == nil {
					return nil
				}
				return &continuity.Settings{WatchContinuityEnabled: *st.Library.EnableWatchContinuity}
			})
	}
	if a.MediacoreCoordinator != nil {
		a.MediacoreCoordinator.RegisterEventCallback(func(ev player.Event) bool {
			if e, ok := ev.(*player.PlaybackLoadedEvent); ok {
				state := e.State
				go a.onProfilePlaybackLoaded(state)
			}
			return true
		})
	}
}

// playbackPlatformRef is the platform given to the server-side playback modules.
func (a *App) playbackPlatformRef() *util.Ref[platform.Platform] {
	if a.ProfilesRuntime != nil {
		return a.ProfilesRuntime.lanePlatformRef
	}
	return a.AnilistPlatformRef
}

// playbackWS is the event manager given to the server-side playback modules: their
// "broadcasts" only reach the devices of the profile that owns the playback.
func (a *App) playbackWS() events.WSEventManagerInterface {
	if a.ProfilesRuntime != nil {
		return a.ProfilesRuntime.laneWS
	}
	return a.WSEventManager
}

// ProfileWS returns an event manager whose broadcasts only reach one profile.
func (a *App) ProfileWS(profileID int64) events.WSEventManagerInterface {
	if a.ProfilesRuntime == nil || profileID <= 0 {
		return a.WSEventManager
	}
	return profilews.ForProfile(a.WSEventManager, a.ProfilesRuntime.Tags, profileID)
}

//----------------------------------------------------------------------------------------------------------------------
// Context-aware collection accessors (plan §3.4.2). Handlers pass
// c.Request().Context(), which carries the request's profile.

func (a *App) GetAnimeCollectionCtx(ctx context.Context, bypassCache bool) (*anilist.AnimeCollection, error) {
	return a.AnilistPlatformRef.Get().GetAnimeCollection(ctx, bypassCache)
}

func (a *App) GetRawAnimeCollectionCtx(ctx context.Context, bypassCache bool) (*anilist.AnimeCollection, error) {
	return a.AnilistPlatformRef.Get().GetRawAnimeCollection(ctx, bypassCache)
}

func (a *App) GetMangaCollectionCtx(ctx context.Context, bypassCache bool) (*anilist.MangaCollection, error) {
	return a.AnilistPlatformRef.Get().GetMangaCollection(ctx, bypassCache)
}

func (a *App) GetRawMangaCollectionCtx(ctx context.Context, bypassCache bool) (*anilist.MangaCollection, error) {
	return a.AnilistPlatformRef.Get().GetRawMangaCollection(ctx, bypassCache)
}

// GlobalAnimeCollection is the collection for server-wide features (scanner, auto
// downloader rules, Nakama host): the union of every profile's list.
func (a *App) GlobalAnimeCollection(ctx context.Context) (*anilist.AnimeCollection, error) {
	if a.ProfilesRuntime == nil {
		return a.GetAnimeCollectionCtx(ctx, false)
	}
	return a.ProfilesRuntime.Router.UnionAnimeCollection(context.WithoutCancel(ctx))
}

// RefreshAnimeCollectionCtx refreshes the collection of the profile in ctx. Without
// profiles it is RefreshAnimeCollection.
func (a *App) RefreshAnimeCollectionCtx(ctx context.Context) (*anilist.AnimeCollection, error) {
	if a.ProfilesRuntime == nil {
		return a.RefreshAnimeCollection()
	}
	return a.refreshAnimeCollectionProfiles(ctx)
}

func (a *App) refreshAnimeCollectionProfiles(ctx context.Context) (*anilist.AnimeCollection, error) {
	rt := a.ProfilesRuntime
	go func() {
		a.OnRefreshAnilistCollectionFuncs.Range(func(key string, f func()) bool {
			go f()
			return true
		})
	}()

	ret, err := rt.Router.RefreshAnimeCollection(ctx)
	if err != nil {
		return nil, err
	}
	pid := profiles.IDFromContext(ctx)

	// Playback modules: the collection of the profile that owns the playback lane.
	if pid == rt.Lane.OwnerID() {
		if a.PlaybackManager != nil {
			a.PlaybackManager.SetAnimeCollection(ret)
		}
		if a.DirectStreamManager != nil {
			a.DirectStreamManager.SetAnimeCollection(ret)
		}
	}
	// Global modules: the union of every profile's list (plan §3.4.3).
	if union, uerr := rt.Router.UnionAnimeCollection(context.Background()); uerr == nil {
		if a.AutoDownloader != nil {
			a.AutoDownloader.SetAnimeCollection(union)
		}
		if a.LibraryExplorer != nil {
			a.LibraryExplorer.SetAnimeCollection(union)
		}
		if a.AutoScanner != nil {
			a.AutoScanner.SetAnimeCollection(union)
		}
	} else {
		a.Logger.Warn().Err(uerr).Msg("profiles: Could not build the union collection")
	}

	if pid > 0 {
		a.ProfileWS(pid).SendEvent(events.RefreshedAnilistAnimeCollection, nil)
	} else {
		a.WSEventManager.SendEvent(events.RefreshedAnilistAnimeCollection, nil)
	}
	return ret, nil
}

//----------------------------------------------------------------------------------------------------------------------
// Users, settings

// ProfileUser builds status.user from a profile (plan §3.4.3). IsSimulated stays
// true so the clients treat the app as "without AniList".
func ProfileUser(p *profiles.Profile) *user.User {
	u := user.NewSimulatedUser()
	if p == nil || u.Viewer == nil {
		return u
	}
	u.Viewer.Name = p.Name
	if p.AvatarURL != "" {
		u.Viewer.Avatar = &anilist.GetViewer_Viewer_Avatar{Large: new(p.AvatarURL), Medium: new(p.AvatarURL)}
	}
	return u
}

// EffectiveSettings returns the global settings with the overrides of the profile in ctx.
func (a *App) EffectiveSettings(ctx context.Context, global *models.Settings) *models.Settings {
	if a.ProfilesRuntime == nil || global == nil {
		return global
	}
	pid := profiles.IDFromContext(ctx)
	if pid <= 0 {
		return global
	}
	st, err := a.ProfilesRuntime.Settings.Get(ctx, pid)
	if err != nil {
		return global
	}
	return settings.Effective(global, st)
}

//----------------------------------------------------------------------------------------------------------------------
// Native auto-watching (plan §3.8)

const markWatchingDedupe = 30 * time.Minute

func (a *App) onProfilePlaybackLoaded(state player.PlaybackState) {
	defer util.HandlePanicInModuleThen("core/onProfilePlaybackLoaded", func() {})
	rt := a.ProfilesRuntime
	info := state.PlaybackInfo
	if rt == nil || info == nil || info.Media == nil || info.Episode == nil || info.IsNakamaWatchParty {
		return
	}
	ctx := rt.Lane.Context(context.Background())
	pid := profiles.IDFromContext(ctx)
	if pid <= 0 {
		return
	}
	st, err := rt.Settings.Get(ctx, pid)
	if err != nil {
		return
	}
	enabled, completedAction := st.MarkWatching()
	if !enabled {
		return
	}
	episode := info.Episode.GetProgressNumber()
	if info.Episode.Type != "" && info.Episode.Type != anime.LocalFileTypeMain {
		episode = 0
	}
	mediaID := info.Media.GetID()

	key := strconv.FormatInt(pid, 10) + "|" + strconv.Itoa(mediaID) + "|" + strconv.Itoa(episode)
	rt.watchMu.Lock()
	if last, ok := rt.watchRecent[key]; ok && time.Since(last) < markWatchingDedupe {
		rt.watchMu.Unlock()
		return
	}
	rt.watchRecent[key] = time.Now()
	for k, t := range rt.watchRecent {
		if time.Since(t) > markWatchingDedupe {
			delete(rt.watchRecent, k)
		}
	}
	rt.watchMu.Unlock()

	action, err := rt.Router.For(pid).MarkWatchingOnStart(ctx, mediaID, episode, localplatform.MarkWatchingOptions{
		CompletedAction: anilist.MediaListStatus(completedAction),
	})
	if err != nil {
		rt.watchMu.Lock()
		delete(rt.watchRecent, key) // retry on the next event
		rt.watchMu.Unlock()
		a.Logger.Warn().Err(err).Int("mediaId", mediaID).Int64("profileId", pid).Msg("profiles: Could not mark the anime as watching")
		return
	}
	a.Logger.Debug().Str("action", string(action)).Int("mediaId", mediaID).Int("episode", episode).Int64("profileId", pid).Msg("profiles: Mark watching on start")
	if action != localplatform.WatchingNone {
		_, _ = a.RefreshAnimeCollectionCtx(ctx)
	}
}

// ErrAnilistLoginDisabled: profiles never log in to AniList (plan §3.4.3).
var ErrAnilistLoginDisabled = fmt.Errorf("profiles don't use AniList accounts: AniList is only used anonymously")

//----------------------------------------------------------------------------------------------------------------------
// Import of the server's AniList account (plan §3.5, one-time migration)

// serverAccountCollectionKey is the CacheLayer key of the account's collection
// (generateCacheKey("collection", nil)).
const serverAccountCollectionKey = "collection-nil"

// ServerAccountAnimeCollection returns the anime list of the server's existing
// AniList account without using its token: the collection cached by the AniList
// platform, else the account's public list (anonymous), else the local
// (simulated) account's collection.
func (a *App) ServerAccountAnimeCollection(ctx context.Context) (*anilist.AnimeCollection, string, error) {
	rt := a.ProfilesRuntime
	if rt == nil {
		return nil, "", errors.New("profiles are not available")
	}
	hasEntries := func(c *anilist.AnimeCollection) bool {
		for _, l := range c.GetMediaListCollection().GetLists() {
			if len(l.GetEntries()) > 0 {
				return true
			}
		}
		return false
	}
	username := ""
	if acc, err := a.Database.GetAccount(); err == nil && acc != nil {
		username = acc.Username
	}
	if username != "" {
		if fc, err := filecache.NewCacher(a.AnilistCacheDir); err == nil {
			var col anilist.AnimeCollection
			found, gerr := fc.GetPerm(filecache.NewPermanentBucket(shared_platform.AnimeCollectionBucket), serverAccountCollectionKey, &col)
			if gerr == nil && found && hasEntries(&col) {
				return &col, "server-account-cache:" + username, nil
			}
		}
		col, err := importer.ClientFetcher{Client: rt.DirectAnilist}.AnimeCollection(ctx, &username)
		if err == nil && hasEntries(col) {
			return col, "server-account-public:" + username, nil
		}
	}
	if a.LocalManager != nil {
		if c := a.LocalManager.GetSimulatedAnimeCollection(); c.IsPresent() && hasEntries(c.MustGet()) {
			return c.MustGet(), "server-local-account", nil
		}
	}
	return nil, "", importer.ErrNothingToImport
}
