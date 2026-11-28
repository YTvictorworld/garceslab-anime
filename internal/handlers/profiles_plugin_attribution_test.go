package handlers

// Issue #26: list writes made by plugins (no request context) in response to a
// playback go to the profile that owns that playback. Before the fix, a direct
// play (viewer session, e.g. the LG TV) had no profile for them and every write
// failed with PROFILE_REQUIRED.

import (
	"context"
	"fmt"
	"net/http"
	"seanime/internal/api/metadata"
	"seanime/internal/api/metadata_provider"
	"seanime/internal/database/db_bridge"
	"seanime/internal/events"
	"seanime/internal/extension"
	"seanime/internal/extension_repo"
	"seanime/internal/library/anime"
	"seanime/internal/library/fillermanager"
	"seanime/internal/plugin"
	"seanime/internal/profiles"
	"seanime/internal/testmocks"
	"seanime/internal/util"
	gojautil "seanime/internal/util/goja"
	"testing"

	"github.com/dop251/goja"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// pluginVM is a plugin runtime with $anilist bound to the app's platform, like
// an installed plugin with the "anilist" permission.
func pluginVM(t *testing.T, env *profilesTestEnv) *goja.Runtime {
	t.Helper()
	appCtx := plugin.NewAppContext().(*plugin.AppContextImpl)
	appCtx.SetModulesPartial(plugin.AppContextModules{AnilistPlatformRef: env.app.AnilistPlatformRef})
	vm := goja.New()
	vm.SetFieldNameMapper(extension_repo.FieldMapper{})
	scheduler := gojautil.NewScheduler()
	t.Cleanup(scheduler.Stop)
	logger := zerolog.Nop()
	appCtx.BindAnilist(vm, &logger, &extension.Extension{ID: "auto-watching", Name: "Auto Watching"}, scheduler)
	return vm
}

// pluginMarkWatching is what the auto-watching plugin does when an episode
// starts: $anilist.updateEntry(mediaId, "CURRENT", 0, progress).
func pluginMarkWatching(vm *goja.Runtime, mediaID, progress int) error {
	_, err := vm.RunString(fmt.Sprintf(`$anilist.updateEntry(%d, "CURRENT", 0, %d, undefined, undefined)`, mediaID, progress))
	return err
}

// pluginCollectionIDs is $anilist.getAnimeCollection(true) as the plugin reads it.
func pluginCollectionIDs(t *testing.T, vm *goja.Runtime) []int {
	t.Helper()
	v, err := vm.RunString(`(function () {
		var c = $anilist.getAnimeCollection(true), ids = []
		var lists = (c && c.MediaListCollection && c.MediaListCollection.lists) || []
		for (var i = 0; i < lists.length; i++) {
			var es = lists[i].entries || []
			for (var j = 0; j < es.length; j++) ids.push(Number(es[j].media.id))
		}
		return ids.sort()
	})()`)
	require.NoError(t, err)
	var ids []int
	require.NoError(t, vm.ExportTo(v, &ids))
	return ids
}

// listOf returns the media ids and progress of a profile's list.
func listOf(t *testing.T, env *profilesTestEnv, p *profiles.Profile) map[int]int {
	t.Helper()
	col, err := env.app.ProfilesRuntime.Router.For(p.ID).GetAnimeCollection(context.Background(), true)
	require.NoError(t, err)
	ret := map[int]int{}
	for _, l := range col.GetMediaListCollection().GetLists() {
		for _, e := range l.GetEntries() {
			progress := 0
			if e.GetProgress() != nil {
				progress = *e.GetProgress()
			}
			ret[e.GetMedia().GetID()] = progress
		}
	}
	return ret
}

// startDirectAndWait starts a direct debrid stream (externalPlayerLink, like the
// TV) and waits for the link to reach the client: that is when the
// onDebridSendStreamToMediaPlayer hook runs and the plugin writes.
func startDirectAndWait(t *testing.T, env *profilesTestEnv, ws *viewerWS, provider *fakeDirectProvider, client, token string, mediaID int) {
	t.Helper()
	code, res := startDirect(env, client, token, mediaID)
	require.Equal(t, http.StatusOK, code, res)
	if g := provider.gate(fmt.Sprintf("item-hash%d", mediaID)); g != nil {
		select {
		case <-g: // already open (another client streams the same torrent)
		default:
			close(g)
		}
	}
	waitUntil(t, "link for "+client, func() bool {
		return ws.has(client, events.ExternalPlayerOpenURL, fmt.Sprintf("item-hash%d", mediaID))
	})
}

func forEnforce(t *testing.T, run func(t *testing.T, enforce bool)) {
	for _, enforce := range []bool{true, false} {
		t.Run(fmt.Sprintf("enforce=%v", enforce), func(t *testing.T) { run(t, enforce) })
	}
}

// Regression test for #26 (the production path: a direct debrid stream of the
// TV, externalPlayerLink). Fails on main with "PROFILE_REQUIRED: no profile
// selected, the change was not saved".
func TestPluginListWriteDuringDirectPlayGoesToThePlaybackProfile(t *testing.T) {
	forEnforce(t, func(t *testing.T, enforce bool) {
		env, ws, provider := newViewersEnv(t)
		env.svc.SetEnforced(enforce)
		ana, tokA := env.session("Ana", "tv-ana")
		bob, _ := env.session("Bob", "web-bob")
		vm := pluginVM(t, env)

		startDirectAndWait(t, env, ws, provider, "tv-ana", tokA, 1)

		// The plugin's write goes to the list of the profile that plays (on main:
		// "PROFILE_REQUIRED: no profile selected, the change was not saved")...
		require.NoError(t, pluginMarkWatching(vm, 1, 0))
		require.Equal(t, map[int]int{1: 0}, listOf(t, env, ana))
		require.Empty(t, listOf(t, env, bob))

		// ...and it reads that list too (on main: empty, so every anime looked
		// "not in list").
		code, res := env.call(http.MethodPost, "/api/v1/anilist/list-entry", "tv-ana", tokA,
			map[string]any{"mediaId": 2, "status": "PLANNING", "type": "anime"})
		require.Equal(t, http.StatusOK, code, res)
		require.Equal(t, []int{1, 2}, pluginCollectionIDs(t, vm))

		// Same once the TV's player starts (manual tracking), as in production.
		code, _ = env.call(http.MethodPost, "/api/v1/playback-manager/manual-tracking/start", "tv-ana", tokA,
			map[string]any{"mediaId": 1, "episodeNumber": 1, "clientId": "tv-ana"})
		require.Equal(t, http.StatusOK, code)
		require.NoError(t, pluginMarkWatching(vm, 1, 3))
		require.Equal(t, 3, listOf(t, env, ana)[1])

		// The session ends: the next write has no playback and is refused again.
		code, _ = env.call(http.MethodPost, "/api/v1/playback-manager/manual-tracking/cancel", "tv-ana", tokA,
			map[string]any{"mediaId": 1, "episodeNumber": 1})
		require.Equal(t, http.StatusOK, code)
		err := pluginMarkWatching(vm, 1, 7)
		require.ErrorContains(t, err, "PROFILE_REQUIRED")
		require.Equal(t, 3, listOf(t, env, ana)[1])
	})
}

func TestPluginRewatchKeepsScoreAndDates(t *testing.T) {
	// auto-watching 1.1.0 sends undefined for the fields it doesn't change.
	env, ws, provider := newViewersEnv(t)
	ana, tokA := env.session("Ana", "tv-ana")
	code, res := env.call(http.MethodPatch, "/api/v1/profiles/me/entries/1", "tv-ana", tokA, map[string]any{
		"status": "COMPLETED", "scoreRaw": 80, "progress": 12,
		"startedAt": map[string]any{"year": 2024, "month": 1}, "completedAt": map[string]any{"year": 2024, "month": 3, "day": 2},
	})
	require.Equal(t, http.StatusOK, code, res)
	before, err := env.app.ProfilesRuntime.Router.For(ana.ID).GetAnimeCollection(context.Background(), true)
	require.NoError(t, err)
	be, found := before.GetListEntryFromAnimeId(1)
	require.True(t, found)
	require.NotZero(t, *be.GetScore())
	vm := pluginVM(t, env)
	startDirectAndWait(t, env, ws, provider, "tv-ana", tokA, 1)

	_, err = vm.RunString(`$anilist.updateEntry(1, "REPEATING", undefined, 2, undefined, undefined)`)
	require.NoError(t, err)
	col, err := env.app.ProfilesRuntime.Router.For(ana.ID).GetAnimeCollection(context.Background(), true)
	require.NoError(t, err)
	e, found := col.GetListEntryFromAnimeId(1)
	require.True(t, found)
	require.Equal(t, "REPEATING", string(*e.GetStatus()))
	require.Equal(t, 2, *e.GetProgress())
	require.Equal(t, *be.GetScore(), *e.GetScore())
	require.Equal(t, 2024, *e.GetStartedAt().GetYear())
	require.Equal(t, 1, *e.GetStartedAt().GetMonth())
	require.Nil(t, e.GetStartedAt().GetDay())
	require.Equal(t, 2, *e.GetCompletedAt().GetDay())
}

func TestPluginGetAnimeEntryReadsThePlaybackProfile(t *testing.T) {
	// ctx.anime.getAnimeEntry(mediaId) is about one anime: with two people
	// watching, it still reads the list of the one playing that anime.
	env, ws, provider := newViewersEnv(t)
	_, tokA := env.session("Ana", "tv-ana")
	_, tokB := env.session("Bob", "tv-bob")
	for client, tok := range map[string]string{"tv-ana": tokA, "tv-bob": tokB} {
		code, res := env.call(http.MethodPost, "/api/v1/anilist/list-entry", client, tok,
			map[string]any{"mediaId": 1, "status": map[string]string{"tv-ana": "PAUSED", "tv-bob": "DROPPED"}[client], "progress": 3, "type": "anime"})
		require.Equal(t, http.StatusOK, code, res)
	}
	startDirectAndWait(t, env, ws, provider, "tv-ana", tokA, 1)
	startDirectAndWait(t, env, ws, provider, "tv-bob", tokB, 2)

	_, err := db_bridge.InsertLocalFiles(env.app.Database, []*anime.LocalFile{}) // a scanned, empty library
	require.NoError(t, err)
	logger := zerolog.Nop()
	appCtx := plugin.NewAppContext().(*plugin.AppContextImpl)
	appCtx.SetModulesPartial(plugin.AppContextModules{
		Database:           env.app.Database,
		AnilistPlatformRef: env.app.AnilistPlatformRef,
		MetadataProviderRef: util.NewRef[metadata_provider.Provider](testmocks.NewFakeMetadataProviderBuilder().
			WithAnimeMetadata(1, &metadata.AnimeMetadata{Episodes: map[string]*metadata.EpisodeMetadata{}}).Build()),
		FillerManager: fillermanager.New(&fillermanager.NewFillerManagerOptions{DB: env.app.Database, Logger: &logger}),
	})
	vm := goja.New()
	vm.SetFieldNameMapper(extension_repo.FieldMapper{})
	scheduler := gojautil.NewScheduler()
	t.Cleanup(scheduler.Stop)
	ctxObj := vm.NewObject()
	appCtx.BindAnimeToContextObj(vm, ctxObj, &logger, &extension.Extension{ID: "auto-watching"}, scheduler)
	require.NoError(t, vm.Set("ctx", ctxObj))

	v, err := vm.RunString(`ctx.anime.getAnimeEntry(1)`)
	require.NoError(t, err)
	promise, ok := v.Export().(*goja.Promise)
	require.True(t, ok)
	var state goja.PromiseState
	waitUntil(t, "getAnimeEntry", func() bool {
		done := make(chan goja.PromiseState, 1)
		scheduler.ScheduleAsync(func() error { done <- promise.State(); return nil })
		state = <-done
		return state != goja.PromiseStatePending
	})
	require.Equal(t, goja.PromiseStateFulfilled, state, "%v", promise.Result())
	entry, ok := promise.Result().Export().(*anime.Entry)
	require.True(t, ok)
	require.NotNil(t, entry.EntryListData)
	require.Equal(t, "PAUSED", string(*entry.EntryListData.Status))
	require.Equal(t, 3, entry.EntryListData.Progress)
}

func TestPluginListWritesOfTwoPlayingProfilesDontCross(t *testing.T) {
	forEnforce(t, func(t *testing.T, enforce bool) {
		env, ws, provider := newViewersEnv(t)
		env.svc.SetEnforced(enforce)
		ana, tokA := env.session("Ana", "tv-ana")
		bob, tokB := env.session("Bob", "tv-bob")
		vm := pluginVM(t, env)

		startDirectAndWait(t, env, ws, provider, "tv-ana", tokA, 1)
		startDirectAndWait(t, env, ws, provider, "tv-bob", tokB, 2)

		// Each write goes to the profile playing that anime.
		require.NoError(t, pluginMarkWatching(vm, 1, 4))
		require.NoError(t, pluginMarkWatching(vm, 2, 9))
		require.Equal(t, map[int]int{1: 4}, listOf(t, env, ana))
		require.Equal(t, map[int]int{2: 9}, listOf(t, env, bob))

		// An anime nobody plays: refused, never given to one of them.
		require.ErrorContains(t, pluginMarkWatching(vm, 3, 1), "PROFILE_REQUIRED")
		// A read that isn't about one anime has two owners: anonymous (empty).
		require.Empty(t, pluginCollectionIDs(t, vm))

		// Both play the same anime: nobody can tell whose write it is, refused.
		startDirectAndWait(t, env, ws, provider, "tv-bob", tokB, 1)
		require.ErrorContains(t, pluginMarkWatching(vm, 1, 11), "PROFILE_REQUIRED")
		require.Equal(t, map[int]int{1: 4}, listOf(t, env, ana))
		require.Equal(t, map[int]int{2: 9}, listOf(t, env, bob))
	})
}

func TestPluginListWriteWithoutPlaybackIsRefused(t *testing.T) {
	forEnforce(t, func(t *testing.T, enforce bool) {
		env, _, _ := newViewersEnv(t)
		env.svc.SetEnforced(enforce)
		ana, _ := env.session("Ana", "tv-ana")
		vm := pluginVM(t, env)

		err := pluginMarkWatching(vm, 1, 1)
		require.ErrorContains(t, err, "PROFILE_REQUIRED: no profile selected, the change was not saved")
		require.Empty(t, listOf(t, env, ana))
		require.Empty(t, pluginCollectionIDs(t, vm))
	})
}

func TestPluginListWriteOfAGuestDirectPlayIsRefused(t *testing.T) {
	// Only possible with enforcement off: a client without a profile plays.
	env, ws, provider := newViewersEnv(t)
	env.svc.SetEnforced(false)
	ana, _ := env.session("Ana", "tv-ana")
	vm := pluginVM(t, env)

	startDirectAndWait(t, env, ws, provider, "tv-guest", "", 1)
	require.ErrorContains(t, pluginMarkWatching(vm, 1, 1), "PROFILE_REQUIRED")
	require.Empty(t, listOf(t, env, ana))
}

func TestPluginListWriteDuringRelayedPlaybackStillGoesToTheLaneOwner(t *testing.T) {
	forEnforce(t, func(t *testing.T, enforce bool) {
		env, ws, provider := newViewersEnv(t)
		env.svc.SetEnforced(enforce)
		ana, _ := env.session("Ana", "web-ana")
		bob, tokB := env.session("Bob", "tv-bob")
		vm := pluginVM(t, env)

		// Phase 1 behaviour: the relayed lane owner.
		require.NoError(t, env.app.ProfilesRuntime.Lane.Acquire(ana))
		require.NoError(t, pluginMarkWatching(vm, 1, 2))
		require.Equal(t, map[int]int{1: 2}, listOf(t, env, ana))

		// While the relayed playback's anime is unknown (being prepared), a direct
		// play of Bob can't be told apart from it: refused rather than crossed (on
		// main this write went to Ana's list, the lane owner).
		startDirectAndWait(t, env, ws, provider, "tv-bob", tokB, 2)
		require.ErrorContains(t, pluginMarkWatching(vm, 2, 5), "PROFILE_REQUIRED")
		require.Empty(t, listOf(t, env, bob))
	})
}
