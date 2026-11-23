package router_test

import (
	"context"
	"path/filepath"
	"seanime/internal/api/anilist"
	"seanime/internal/continuity"
	"seanime/internal/database/models"
	"seanime/internal/events"
	"seanime/internal/profiles"
	"seanime/internal/profiles/avatars"
	"seanime/internal/profiles/history"
	"seanime/internal/profiles/metadata"
	"seanime/internal/profiles/profilestest"
	"seanime/internal/profiles/profilews"
	"seanime/internal/profiles/router"
	"seanime/internal/profiles/settings"
	"seanime/internal/profiles/store"
	"seanime/internal/util"
	"seanime/internal/util/filecache"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type env struct {
	svc  *profiles.Service
	db   *gorm.DB
	r    *router.Router
	fake *profilestest.FakeAnilist
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open("")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	svc, err := profiles.New(st, profiles.Options{})
	require.NoError(t, err)
	fake := profilestest.NewFakeAnilist(t) // fails the test on any token or mutation
	meta, err := metadata.New(metadata.Options{Client: fake, DB: st.DB()})
	require.NoError(t, err)
	return &env{svc: svc, db: st.DB(), r: router.New(router.Options{Meta: meta, DB: st.DB()}), fake: fake}
}

func (e *env) ctxFor(t *testing.T, name string) (context.Context, *profiles.Profile) {
	t.Helper()
	p, err := e.svc.Create(context.Background(), profiles.CreateInput{Name: name, AvatarID: avatars.Catalog()[0].ID})
	require.NoError(t, err)
	return profiles.WithProfile(context.Background(), p), p
}

func st(s anilist.MediaListStatus) *anilist.MediaListStatus { return &s }

func TestNoProfile(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// Metadata reads work anonymously.
	m, err := e.r.GetAnime(ctx, profilestest.MediaAlpha)
	require.NoError(t, err)
	assert.Equal(t, profilestest.MediaAlpha, m.ID)
	assert.False(t, e.r.GetAnilistClient().IsAuthenticated())

	// Collection reads are empty.
	col, err := e.r.GetAnimeCollection(ctx, false)
	require.NoError(t, err)
	assert.Empty(t, col.MediaListCollection.Lists)
	raw, err := e.r.GetRawAnimeCollection(ctx, true)
	require.NoError(t, err)
	assert.Empty(t, raw.MediaListCollection.Lists)
	rel, err := e.r.GetAnimeCollectionWithRelations(ctx)
	require.NoError(t, err)
	assert.Empty(t, rel.MediaListCollection.Lists)
	stats, err := e.r.GetViewerStats(ctx)
	require.NoError(t, err)
	assert.Zero(t, stats.Viewer.Statistics.Anime.Count)

	// Writes fail with ErrNoProfile (PROFILE_REQUIRED) and nothing is written.
	for _, err := range []error{
		e.r.UpdateEntry(ctx, profilestest.MediaAlpha, st(anilist.MediaListStatusCurrent), nil, nil, nil, nil),
		e.r.UpdateEntryProgress(ctx, profilestest.MediaAlpha, 1, nil),
		e.r.UpdateEntryRepeat(ctx, profilestest.MediaAlpha, 1),
		e.r.DeleteEntry(ctx, profilestest.MediaAlpha, 1),
		e.r.AddMediaToCollection(ctx, []int{profilestest.MediaAlpha}),
	} {
		assert.ErrorIs(t, err, router.ErrNoProfile)
		assert.ErrorIs(t, err, profiles.ErrProfileRequired)
		assert.Contains(t, err.Error(), "PROFILE_REQUIRED")
	}
	var n int
	require.NoError(t, e.db.Raw("SELECT COUNT(*) FROM list_entries").Scan(&n).Error)
	assert.Zero(t, n)
}

func TestListsAreIsolated(t *testing.T) {
	e := newEnv(t)
	ctxA, a := e.ctxFor(t, "Ana")
	ctxB, b := e.ctxFor(t, "Bruno")

	require.NoError(t, e.r.UpdateEntry(ctxA, profilestest.MediaAlpha, st(anilist.MediaListStatusCurrent), new(90), new(3), nil, nil))
	require.NoError(t, e.r.UpdateEntry(ctxB, profilestest.MediaAlpha, st(anilist.MediaListStatusPlanning), nil, nil, nil, nil))
	require.NoError(t, e.r.UpdateEntryProgress(ctxB, profilestest.MediaDelta, 2, nil))

	colA, err := e.r.GetAnimeCollection(ctxA, false)
	require.NoError(t, err)
	colB, err := e.r.GetAnimeCollection(ctxB, false)
	require.NoError(t, err)

	ea, ok := colA.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	require.True(t, ok)
	assert.Equal(t, anilist.MediaListStatusCurrent, *ea.Status)
	assert.Equal(t, 3, *ea.Progress)
	_, ok = colA.GetListEntryFromAnimeId(profilestest.MediaDelta)
	assert.False(t, ok, "B's entry never shows up in A's list")

	eb, ok := colB.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	require.True(t, ok)
	assert.Equal(t, anilist.MediaListStatusPlanning, *eb.Status)
	assert.Equal(t, 0.0, *eb.Score)
	assert.NotEqual(t, ea.ID, eb.ID)

	// Deleting B's entry with A's entry id does nothing to A.
	assert.Error(t, e.r.DeleteEntry(ctxB, profilestest.MediaAlpha, ea.ID))
	colA, err = e.r.GetAnimeCollection(ctxA, true)
	require.NoError(t, err)
	_, ok = colA.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	assert.True(t, ok)

	// Stats are per profile.
	sa, err := e.r.GetViewerStats(ctxA)
	require.NoError(t, err)
	sb, err := e.r.GetViewerStats(ctxB)
	require.NoError(t, err)
	assert.Equal(t, 1, sa.Viewer.Statistics.Anime.Count)
	assert.Equal(t, 1, sb.Viewer.Statistics.Anime.Count) // PLANNING isn't counted, Delta is
	assert.NotEqual(t, a.ID, b.ID)

	// The union (global modules) has both media, each in its most active status.
	union, err := e.r.UnionAnimeCollection(context.Background())
	require.NoError(t, err)
	ua, ok := union.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	require.True(t, ok)
	assert.Equal(t, anilist.MediaListStatusCurrent, *ua.Status)
	_, ok = union.GetListEntryFromAnimeId(profilestest.MediaDelta)
	assert.True(t, ok)

	// A trashed profile drops out of the union.
	require.NoError(t, e.svc.Delete(context.Background(), b.ID))
	union, err = e.r.UnionAnimeCollection(context.Background())
	require.NoError(t, err)
	_, ok = union.GetListEntryFromAnimeId(profilestest.MediaDelta)
	assert.False(t, ok)
}

func TestLanePlatformUsesTheOwner(t *testing.T) {
	e := newEnv(t)
	ctxA, a := e.ctxFor(t, "Ana")
	ctxB, b := e.ctxFor(t, "Bruno")
	lane := e.svc.Lane()
	lp := router.NewLanePlatform(e.r, lane.Context)

	// No owner yet: the playback modules can't write.
	assert.ErrorIs(t, lp.UpdateEntryProgress(context.Background(), profilestest.MediaAlpha, 1, nil), router.ErrNoProfile)

	require.NoError(t, lane.Acquire(a))
	require.NoError(t, lp.UpdateEntryProgress(context.Background(), profilestest.MediaAlpha, 1, nil))
	// A context with its own profile is respected.
	require.NoError(t, lp.UpdateEntryProgress(ctxB, profilestest.MediaGamma, 1, nil))

	colA, _ := e.r.GetAnimeCollection(ctxA, true)
	colB, _ := e.r.GetAnimeCollection(ctxB, true)
	_, ok := colA.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	assert.True(t, ok)
	_, ok = colB.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	assert.False(t, ok)
	_, ok = colB.GetListEntryFromAnimeId(profilestest.MediaGamma)
	assert.True(t, ok)

	// B can't take the lane while A's playback is being prepared.
	assert.ErrorIs(t, lane.Acquire(b), profiles.ErrPlaybackBusy)
	require.NoError(t, lane.Acquire(a), "the owner can start another playback")
}

func TestContinuityIsIsolated(t *testing.T) {
	e := newEnv(t)
	ctxA, a := e.ctxFor(t, "Ana")
	ctxB, _ := e.ctxFor(t, "Bruno")
	lane := e.svc.Lane()

	cacher, err := filecache.NewCacher(filepath.Join(t.TempDir(), "cache"))
	require.NoError(t, err)
	m := continuity.NewManager(&continuity.NewManagerOptions{FileCacher: cacher, Logger: util.NewLogger()})
	sstore := settings.NewStore(e.db)
	m.SetHistoryStore(history.New(e.db), func() context.Context { return lane.Context(context.Background()) },
		func(ctx context.Context) *continuity.Settings {
			pid := profiles.IDFromContext(ctx)
			s, err := sstore.Get(ctx, pid)
			if pid == 0 || err != nil || s.Library == nil || s.Library.EnableWatchContinuity == nil {
				return nil
			}
			return &continuity.Settings{WatchContinuityEnabled: *s.Library.EnableWatchContinuity}
		})
	m.SetSettings(&continuity.Settings{WatchContinuityEnabled: true})

	require.NoError(t, m.UpdateWatchHistoryItemCtx(ctxA, &continuity.UpdateWatchHistoryItemOptions{
		MediaId: profilestest.MediaAlpha, EpisodeNumber: 3, CurrentTime: 600, Duration: 1440, Kind: continuity.MediastreamKind}))
	assert.True(t, m.GetWatchHistoryItemCtx(ctxA, profilestest.MediaAlpha).Found)
	assert.False(t, m.GetWatchHistoryItemCtx(ctxB, profilestest.MediaAlpha).Found)
	assert.Empty(t, m.GetWatchHistoryCtx(ctxB))
	assert.Len(t, m.GetWatchHistoryCtx(ctxA), 1)

	// Without profile nothing is read or written.
	assert.Empty(t, m.GetWatchHistory())
	assert.Error(t, m.UpdateWatchHistoryItem(&continuity.UpdateWatchHistoryItemOptions{MediaId: profilestest.MediaDelta, EpisodeNumber: 1, CurrentTime: 100, Duration: 1400}))

	// Server-side players (no context) write to the lane owner.
	require.NoError(t, lane.Acquire(a))
	require.NoError(t, m.UpdateWatchHistoryItem(&continuity.UpdateWatchHistoryItemOptions{
		MediaId: profilestest.MediaDelta, EpisodeNumber: 1, CurrentTime: 100, Duration: 1400, Kind: continuity.MediastreamKind}))
	assert.True(t, m.GetWatchHistoryItemCtx(ctxA, profilestest.MediaDelta).Found)
	assert.False(t, m.GetWatchHistoryItemCtx(ctxB, profilestest.MediaDelta).Found)

	// episode_progress keeps every episode.
	rows, err := history.New(e.db).EpisodeProgress(ctxA, profilestest.MediaAlpha)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 3, rows[0].EpisodeNumber)

	// The continuity switch is per profile.
	require.NoError(t, sstore.Save(ctxB, profiles.IDFromContext(ctxB), &settings.ProfileSettings{Library: &settings.ProfileLibraryOverrides{EnableWatchContinuity: new(false)}}))
	assert.True(t, m.GetSettingsCtx(ctxA).WatchContinuityEnabled)
	assert.False(t, m.GetSettingsCtx(ctxB).WatchContinuityEnabled)
}

func TestSettingsAreIsolated(t *testing.T) {
	e := newEnv(t)
	ctxA, a := e.ctxFor(t, "Ana")
	_, b := e.ctxFor(t, "Bruno")
	s := settings.NewStore(e.db)

	global := &models.Settings{Library: &models.LibrarySettings{AutoUpdateProgress: true, LibraryPath: "/anime"}, Anilist: &models.AnilistSettings{}}
	// A saves a full body with its own values: they go to A, the global body keeps the old ones.
	next := &models.Settings{Library: &models.LibrarySettings{AutoUpdateProgress: false, AutoPlayNextEpisode: true, LibraryPath: "/anime"},
		Anilist: &models.AnilistSettings{HideAudienceScore: true}}
	overrides := settings.Split(global, next, &settings.ProfileSettings{})
	require.NoError(t, s.Save(ctxA, a.ID, overrides))
	assert.True(t, next.Library.AutoUpdateProgress, "global value restored")
	assert.False(t, next.Library.AutoPlayNextEpisode)
	assert.False(t, next.Anilist.HideAudienceScore)

	sa, err := s.Get(ctxA, a.ID)
	require.NoError(t, err)
	sb, err := s.Get(ctxA, b.ID)
	require.NoError(t, err)
	effA := settings.Effective(global, sa)
	effB := settings.Effective(global, sb)
	assert.False(t, effA.Library.AutoUpdateProgress)
	assert.True(t, effA.Library.AutoPlayNextEpisode)
	assert.True(t, effA.Anilist.HideAudienceScore)
	assert.True(t, effB.Library.AutoUpdateProgress)
	assert.False(t, effB.Anilist.HideAudienceScore)
	assert.Equal(t, "/anime", effA.Library.LibraryPath)

	// Theme and auto-select too.
	require.NoError(t, s.SaveTheme(ctxA, a.ID, &models.Theme{AccentColor: "#ff0000"}))
	_, found, err := s.Theme(ctxA, b.ID)
	require.NoError(t, err)
	assert.False(t, found)
	ta, found, err := s.Theme(ctxA, a.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "#ff0000", ta.AccentColor)

	// MarkWatchingOnStart defaults on.
	enabled, action := sb.MarkWatching()
	assert.True(t, enabled)
	assert.Equal(t, "REPEATING", action)

	assert.Error(t, s.Save(ctxA, a.ID, &settings.ProfileSettings{CompletedAction: new("WATCHING")}))
	assert.Error(t, s.Save(ctxA, a.ID, &settings.ProfileSettings{Player: []byte(`[1,2]`)}))
	sp := &settings.ProfileSettings{}
	assert.NoError(t, sp.SetPath("library.autoPlayNextEpisode", true))
	assert.Error(t, sp.SetPath("library.libraryPath", "/x"))
	assert.Error(t, sp.SetPath("library.autoPlayNextEpisode", "yes"))
}

// recorder is a fake websocket manager.
type recorder struct {
	mu        sync.Mutex
	clients   []string
	sent      map[string][]string // client -> event types
	broadcast []string
}

func (r *recorder) SendEvent(t string, payload interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.broadcast = append(r.broadcast, t)
}
func (r *recorder) SendEventTo(clientId string, t string, payload interface{}, noLog ...bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sent == nil {
		r.sent = map[string][]string{}
	}
	r.sent[clientId] = append(r.sent[clientId], t)
}
func (r *recorder) GetClientIds() []string                   { return r.clients }
func (r *recorder) GetClientPlatform(clientId string) string { return "web" }
func (r *recorder) SubscribeToClientEvents(id string) *events.ClientEventSubscriber {
	return nil
}
func (r *recorder) SubscribeToClientNativePlayerEvents(id string) *events.ClientEventSubscriber {
	return nil
}
func (r *recorder) SubscribeToClientVideoCoreEvents(id string) *events.ClientEventSubscriber {
	return nil
}
func (r *recorder) SubscribeToClientMpvCoreEvents(id string) *events.ClientEventSubscriber {
	return nil
}
func (r *recorder) SubscribeToClientNakamaEvents(id string) *events.ClientEventSubscriber {
	return nil
}
func (r *recorder) SubscribeToClientPlaylistEvents(id string) *events.ClientEventSubscriber {
	return nil
}
func (r *recorder) UnsubscribeFromClientEvents(id string) {}

func TestEventsAreIsolated(t *testing.T) {
	base := &recorder{clients: []string{"tv-a", "phone-a", "web-b", "anon"}}
	tags := profilews.NewTags()
	tags.Tag("tv-a", 1)
	tags.Tag("phone-a", 1)
	tags.Tag("web-b", 2)
	tags.Tag("gone-a", 1) // not connected

	profilews.ForProfile(base, tags, 1).SendEvent("refreshed-anilist-anime-collection", nil)
	profilews.ForProfile(base, tags, 2).SendEvent("settings", nil)
	assert.Equal(t, map[string][]string{
		"tv-a":    {"refreshed-anilist-anime-collection"},
		"phone-a": {"refreshed-anilist-anime-collection"},
		"web-b":   {"settings"},
	}, base.sent)
	assert.Empty(t, base.broadcast)

	// The lane WS follows the owner; without owner it broadcasts (nothing private).
	owner := int64(0)
	lane := profilews.New(base, tags, func() int64 { return owner })
	lane.BroadcastWithoutProfile = true
	lane.SendEvent("video-core", nil)
	assert.Equal(t, []string{"video-core"}, base.broadcast)
	owner = 2
	lane.SendEvent("video-core", nil)
	assert.Equal(t, []string{"settings", "video-core"}, base.sent["web-b"])
	assert.Len(t, base.sent["tv-a"], 1)

	// Logging out / deleting untags.
	tags.UntagProfile(1)
	profilews.ForProfile(base, tags, 1).SendEvent("x", nil)
	assert.Len(t, base.sent["tv-a"], 1)
	tags.Untag("web-b")
	assert.Equal(t, int64(0), tags.ProfileOf("web-b"))
}
