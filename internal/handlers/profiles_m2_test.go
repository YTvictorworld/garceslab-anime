package handlers

import (
	"net/http"
	"seanime/internal/continuity"
	"seanime/internal/core"
	"seanime/internal/database/models"
	"seanime/internal/events"
	"seanime/internal/platforms/platform"
	"seanime/internal/profiles"
	"seanime/internal/profiles/profilestest"
	"seanime/internal/util"
	"seanime/internal/util/filecache"
	"seanime/internal/util/result"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newM2Env is newProfilesTestEnv with the local profiles runtime installed on a
// fake anonymous AniList client (any token or mutation fails the test) and
// enforcement on.
func newM2Env(t *testing.T) (*profilesTestEnv, *profilestest.FakeAnilist) {
	t.Helper()
	env := newProfilesTestEnv(t)
	logger := zerolog.Nop()
	fake := profilestest.NewFakeAnilist(t)
	ws := events.NewWSEventManager(&logger)
	rt, err := core.NewProfilesRuntime(env.svc, ws, fake, fake, nil, &logger)
	require.NoError(t, err)
	env.app.ProfilesRuntime = rt
	env.app.WSEventManager = ws
	env.app.AnilistPlatformRef = util.NewRef[platform.Platform](rt.Router)
	env.app.OnRefreshAnilistCollectionFuncs = result.NewMap[string, func()]()
	cacher, err := filecache.NewCacher(t.TempDir())
	require.NoError(t, err)
	env.app.ContinuityManager = continuity.NewManager(&continuity.NewManagerOptions{FileCacher: cacher, Logger: &logger})
	env.app.ContinuityManager.SetSettings(&continuity.Settings{WatchContinuityEnabled: true})
	env.app.InstallProfilesRuntime()
	_, err = env.app.Database.UpsertSettings(&models.Settings{
		BaseModel:      models.BaseModel{ID: 1},
		Library:        &models.LibrarySettings{AutoUpdateProgress: true},
		MediaPlayer:    &models.MediaPlayerSettings{},
		Torrent:        &models.TorrentSettings{},
		Anilist:        &models.AnilistSettings{},
		Manga:          &models.MangaSettings{},
		Discord:        &models.DiscordSettings{},
		Notifications:  &models.NotificationSettings{},
		Nakama:         &models.NakamaSettings{},
		AutoDownloader: &models.AutoDownloaderSettings{},
		ListSync:       &models.ListSyncSettings{},
	})
	require.NoError(t, err)
	env.svc.SetEnforced(true)
	env.e = echo.New()
	env.e.JSONSerializer = &core.CustomJSONSerializer{}
	InitRoutes(env.app, env.e)
	return env, fake
}

func (env *profilesTestEnv) session(name, client string) (*profiles.Profile, string) {
	env.t.Helper()
	p, err := env.svc.Create(env.t.Context(), profiles.CreateInput{Name: name, AvatarID: "gl-lorelei-01"})
	require.NoError(env.t, err)
	sess, err := env.svc.Select(env.t.Context(), p.ID, "", client, "web")
	require.NoError(env.t, err)
	return p, sess.ProfileToken
}

func collectionMediaIDs(t *testing.T, res map[string]any) []int {
	t.Helper()
	ids := []int{}
	data := dataMap(t, res)
	mlc, _ := data["MediaListCollection"].(map[string]any)
	lists, _ := mlc["lists"].([]any)
	for _, l := range lists {
		for _, e := range l.(map[string]any)["entries"].([]any) {
			ids = append(ids, int(e.(map[string]any)["media"].(map[string]any)["id"].(float64)))
		}
	}
	return ids
}

func TestM2EnforcedFlow(t *testing.T) {
	env, _ := newM2Env(t)
	ana, tokA := env.session("Ana", "tv")
	bruno, tokB := env.session("Bruno", "web")

	// Without profile: user data is refused, the public catalog works.
	code, res := env.call("GET", "/api/v1/anilist/collection", "tv", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "PROFILE_REQUIRED", res["error"])
	code, res = env.call("GET", "/api/v1/anilist/media-details/1001", "tv", "", nil)
	assert.Equal(t, 200, code, res)
	code, res = env.call("GET", "/api/v1/status", "tv", "", nil)
	assert.Equal(t, 200, code, res)
	user := dataMap(t, res)["user"].(map[string]any)
	assert.Equal(t, true, user["isSimulated"])
	assert.Equal(t, "User", user["viewer"].(map[string]any)["name"])

	// Each profile edits its own list through the usual endpoint.
	code, res = env.call("POST", "/api/v1/anilist/list-entry", "tv", tokA, map[string]any{"mediaId": 1001, "status": "CURRENT", "progress": 2, "type": "anime"})
	require.Equal(t, 200, code, res)
	code, res = env.call("POST", "/api/v1/anilist/list-entry", "web", tokB, map[string]any{"mediaId": 1004, "status": "PLANNING", "type": "anime"})
	require.Equal(t, 200, code, res)

	code, res = env.call("GET", "/api/v1/anilist/collection", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	assert.Equal(t, []int{1001}, collectionMediaIDs(t, res))
	code, res = env.call("GET", "/api/v1/anilist/collection/raw", "web", tokB, nil)
	require.Equal(t, 200, code, res)
	assert.Equal(t, []int{1004}, collectionMediaIDs(t, res))

	// A token only works on the client it was issued to.
	code, _ = env.call("GET", "/api/v1/anilist/collection", "web", tokA, nil)
	assert.Equal(t, http.StatusUnauthorized, code)

	// Deleting from B's list never touches A.
	code, res = env.call("DELETE", "/api/v1/anilist/list-entry", "web", tokB, map[string]any{"mediaId": 1001, "type": "anime"})
	assert.NotEqual(t, 200, code, res)
	code, res = env.call("GET", "/api/v1/anilist/collection", "tv", tokA, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, []int{1001}, collectionMediaIDs(t, res))

	// Status: the user is the profile (never the server's AniList account).
	code, res = env.call("GET", "/api/v1/status", "tv", tokA, nil)
	require.Equal(t, 200, code)
	st := dataMap(t, res)
	assert.Equal(t, "Ana", st["user"].(map[string]any)["viewer"].(map[string]any)["name"])
	assert.Equal(t, float64(ana.ID), st["currentProfile"].(map[string]any)["id"])

	// Settings: a per-profile field goes to the profile, GET returns the effective value.
	code, res = env.call("PATCH", "/api/v1/settings/path", "tv", tokA, map[string]any{"path": "library.autoUpdateProgress", "value": false})
	require.Equal(t, 200, code, res)
	code, res = env.call("GET", "/api/v1/settings", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	assert.Equal(t, false, dataMap(t, res)["library"].(map[string]any)["autoUpdateProgress"])
	code, res = env.call("GET", "/api/v1/settings", "web", tokB, nil)
	require.Equal(t, 200, code, res)
	assert.Equal(t, true, dataMap(t, res)["library"].(map[string]any)["autoUpdateProgress"])
	global, err := env.app.Database.GetSettings()
	require.NoError(t, err)
	assert.True(t, global.Library.AutoUpdateProgress, "the global value is unchanged")

	// Profile settings endpoint.
	code, res = env.call("PATCH", "/api/v1/profiles/me/settings", "web", tokB, map[string]any{"markWatchingOnStart": false, "completedAction": "CURRENT"})
	require.Equal(t, 200, code, res)
	assert.Equal(t, false, dataMap(t, res)["markWatchingOnStart"])
	code, res = env.call("GET", "/api/v1/profiles/me/settings", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	assert.Equal(t, true, dataMap(t, res)["markWatchingOnStart"])
	code, _ = env.call("PATCH", "/api/v1/profiles/me/settings", "web", tokB, map[string]any{"libraryPath": "/etc"})
	assert.Equal(t, http.StatusBadRequest, code)

	// Theme per profile.
	code, res = env.call("PATCH", "/api/v1/theme", "tv", tokA, map[string]any{"theme": map[string]any{"accentColor": "#123456"}})
	require.Equal(t, 200, code, res)
	code, res = env.call("GET", "/api/v1/theme", "tv", tokA, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, "#123456", dataMap(t, res)["accentColor"])
	code, res = env.call("GET", "/api/v1/theme", "web", tokB, nil)
	require.Equal(t, 200, code)
	assert.NotEqual(t, "#123456", dataMap(t, res)["accentColor"])

	// Continuity per profile.
	code, res = env.call("PATCH", "/api/v1/continuity/item", "tv", tokA, map[string]any{"options": map[string]any{"mediaId": 1001, "episodeNumber": 3, "currentTime": 300, "duration": 1440, "kind": "onlinestream"}})
	require.Equal(t, 200, code, res)
	n := 0
	require.NoError(t, env.svc.DB().Raw("SELECT COUNT(*) FROM watch_history WHERE profile_id = ?", ana.ID).Scan(&n).Error)
	assert.Equal(t, 1, n)
	require.NoError(t, env.svc.DB().Raw("SELECT COUNT(*) FROM watch_history WHERE profile_id = ?", bruno.ID).Scan(&n).Error)
	assert.Zero(t, n)

	// Export A and import it into B.
	code, res = env.call("GET", "/api/v1/profiles/"+itoa(ana.ID)+"/export", "web", tokB, nil)
	require.Equal(t, 200, code, res)
	exp := dataMap(t, res)
	assert.Equal(t, "garceslab-anime-profile", exp["format"])
	code, res = env.call("POST", "/api/v1/profiles/"+itoa(bruno.ID)+"/import/json", "web", tokB, map[string]any{"mode": "merge", "data": exp})
	require.Equal(t, 200, code, res)
	assert.Equal(t, float64(1), dataMap(t, res)["imported"])

	// Public AniList import (fixture user, anonymous).
	code, res = env.call("POST", "/api/v1/profiles/"+itoa(bruno.ID)+"/import/anilist", "web", tokB, map[string]any{"username": profilestest.FixtureUserName, "mode": "replace"})
	require.Equal(t, 200, code, res)
	assert.Equal(t, float64(6), dataMap(t, res)["imported"])
	code, res = env.call("POST", "/api/v1/profiles/"+itoa(bruno.ID)+"/import/anilist", "web", tokB, map[string]any{"username": "NotPublic"})
	assert.Equal(t, http.StatusUnprocessableEntity, code, res)
	code, res = env.call("GET", "/api/v1/anilist/collection", "tv", tokA, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, []int{1001}, collectionMediaIDs(t, res), "imports into B never touch A")

	// Stats per profile.
	code, res = env.call("GET", "/api/v1/anilist/stats", "web", tokB, nil)
	require.Equal(t, 200, code, res)
	statsB := dataMap(t, res)["animeStats"].(map[string]any)["count"]
	code, res = env.call("GET", "/api/v1/anilist/stats", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	assert.NotEqual(t, statsB, dataMap(t, res)["animeStats"].(map[string]any)["count"])

	// Custom lists.
	code, res = env.call("POST", "/api/v1/profiles/me/lists", "tv", tokA, map[string]any{"name": "Mine"})
	require.Equal(t, 200, code, res)
	listID := int64(res["data"].([]any)[0].(map[string]any)["id"].(float64))
	code, res = env.call("POST", "/api/v1/profiles/me/lists/"+itoa(listID)+"/items/1001", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	code, _ = env.call("POST", "/api/v1/profiles/me/lists/"+itoa(listID)+"/items/1001", "web", tokB, nil)
	assert.Equal(t, http.StatusNotFound, code, "B can't use A's list")

	// AniList login is gone.
	code, _ = env.call("POST", "/api/v1/auth/login", "tv", tokA, map[string]any{"token": "x"})
	assert.Equal(t, http.StatusGone, code)

	// Playback lane: B can't start a playback while A's is starting.
	require.NoError(t, env.svc.Lane().Acquire(ana))
	code, res = env.call("POST", "/api/v1/debrid/stream/start", "web", tokB, map[string]any{})
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "PLAYBACK_BUSY", res["error"])
}
