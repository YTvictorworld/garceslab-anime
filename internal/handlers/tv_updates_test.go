package handlers

// TV app updates (docs/tv-updates.md, issue #18): the /api/v1/tv-updates routes
// against a fake updater (httptest), the playback refusal and the token, which
// must never reach a client or the logs.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"seanime/internal/core"
	"seanime/internal/database/db"
	"seanime/internal/profiles"
	"seanime/internal/profiles/sessions"
	"seanime/internal/tvupdates"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tvTestToken = "tv-updater-secret-token-9f8e7d6c5b4a"

// fakeTVUpdater imitates the tv-updater API: GET /status, POST /install and
// POST /later (X-Updater-Token).
type fakeTVUpdater struct {
	mu      sync.Mutex
	srv     *httptest.Server
	pending bool
	queued  bool
	posts   []string
	tokens  []string
}

func newFakeTVUpdater(t *testing.T) *fakeTVUpdater {
	f := &fakeTVUpdater{pending: true}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		send := func(code int, v any) {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(v)
		}
		if r.Method == http.MethodPost {
			f.posts = append(f.posts, r.URL.Path)
			f.tokens = append(f.tokens, r.Header.Get(tvupdates.TokenHeader))
			if r.Header.Get(tvupdates.TokenHeader) != tvTestToken {
				send(401, map[string]any{"ok": false, "error": "invalid or missing X-Updater-Token"})
				return
			}
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /status":
			st := map[string]any{"channel": "nightly", "tvOnline": true, "installing": f.queued, "pending": nil, "lastResult": nil,
				"installed": map[string]any{"version": "1.0.0", "commit": "808b9ee0"}}
			if f.pending {
				st["pending"] = map[string]any{"version": "1.0.1", "commit": "0eff7760", "builtAt": "2026-11-26T19:03:40.416Z",
					"changes": []any{map[string]any{"type": "feat", "title": "Seasons tabs", "pr": 21}}}
			}
			send(200, st)
		case "POST /install":
			if !f.pending {
				send(409, map[string]any{"ok": false, "error": "no pending build"})
				return
			}
			f.queued = true
			send(202, map[string]any{"ok": true, "queued": true, "commit": "0eff7760", "version": "1.0.1"})
		case "POST /later":
			f.pending, f.queued = false, false
			send(200, map[string]any{"ok": true, "dismissed": "0eff7760", "cancelledQueue": false})
		default:
			send(404, map[string]any{"ok": false, "error": "not found"})
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTVUpdater) postCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.posts)
}

func tvUpdatesService(t *testing.T, url string, logger *zerolog.Logger) *tvupdates.Service {
	vars := map[string]string{tvupdates.EnvURL: url, tvupdates.EnvToken: tvTestToken}
	return tvupdates.New(tvupdates.Options{
		DataDir: t.TempDir(), Logger: logger, Getenv: func(k string) string { return vars[k] },
		StatusTimeout: 500 * time.Millisecond, ActionTimeout: 500 * time.Millisecond,
	})
}

// rawCall is env.call that also returns the raw body.
func (env *profilesTestEnv) rawCall(method, path, clientID, token string, body any) (int, string) {
	env.t.Helper()
	rdr := strings.NewReader("")
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:43211"+path, rdr)
	req.RemoteAddr = "127.0.0.1:50000"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if clientID != "" {
		req.Header.Set(clientIdHeaderName, clientID)
		req.Header.Set(clientIdProofHeaderName, generateClientIdentityProof(env.app, clientID))
	}
	if token != "" {
		req.Header.Set(profiles.HeaderName, token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestTVUpdatesNotConfigured(t *testing.T) {
	env := newProfilesTestEnv(t)
	// No service at all (older wiring) and a service without URL behave the same.
	for _, svc := range []*tvupdates.Service{nil, tvupdates.New(tvupdates.Options{Getenv: func(string) string { return "" }})} {
		env.app.TVUpdates = svc
		code, body := env.rawCall(http.MethodGet, "/api/v1/tv-updates/status", "c1", "", nil)
		require.Equal(t, 200, code)
		assert.JSONEq(t, `{"data": {"configured": false}}`, body)

		code, res := env.call(http.MethodPost, "/api/v1/tv-updates/install", "c1", "", nil)
		assert.Equal(t, http.StatusNotFound, code)
		assert.Equal(t, "TV_UPDATER_NOT_CONFIGURED", res["error"])
		code, res = env.call(http.MethodPost, "/api/v1/tv-updates/later", "c1", "", nil)
		assert.Equal(t, http.StatusNotFound, code)
		assert.Equal(t, "TV_UPDATER_NOT_CONFIGURED", res["error"])

		code, res = env.call(http.MethodGet, "/api/v1/tv-updates/settings", "c1", "", nil)
		require.Equal(t, 200, code)
		assert.Equal(t, false, dataMap(t, res)["configured"])
	}
}

func TestTVUpdatesUnreachable(t *testing.T) {
	env := newProfilesTestEnv(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	env.app.TVUpdates = tvUpdatesService(t, url, nil)

	code, res := env.call(http.MethodGet, "/api/v1/tv-updates/status", "c1", "", nil)
	require.Equal(t, 200, code)
	data := dataMap(t, res)
	assert.Equal(t, true, data["configured"])
	assert.Equal(t, false, data["reachable"])
	assert.NotEmpty(t, data["error"])
	assert.NotContains(t, data, "status")

	code, res = env.call(http.MethodPost, "/api/v1/tv-updates/install", "c1", "", nil)
	assert.Equal(t, http.StatusBadGateway, code)
	assert.True(t, strings.HasPrefix(res["error"].(string), "TV_UPDATER_UNREACHABLE"), res)
}

func TestTVUpdatesPendingInstallLater(t *testing.T) {
	env := newProfilesTestEnv(t)
	f := newFakeTVUpdater(t)
	env.app.TVUpdates = tvUpdatesService(t, f.srv.URL, nil)

	// Pending: the updater's status comes back unchanged.
	code, res := env.call(http.MethodGet, "/api/v1/tv-updates/status", "c1", "", nil)
	require.Equal(t, 200, code)
	data := dataMap(t, res)
	assert.Equal(t, true, data["reachable"])
	st := data["status"].(map[string]any)
	assert.Equal(t, "nightly", st["channel"])
	assert.Equal(t, false, st["installing"])
	assert.Equal(t, "1.0.1", st["pending"].(map[string]any)["version"])

	// Install: forwarded with the token.
	code, res = env.call(http.MethodPost, "/api/v1/tv-updates/install", "c1", "", nil)
	require.Equal(t, 200, code, res)
	data = dataMap(t, res)
	assert.Equal(t, true, data["ok"])
	assert.Equal(t, true, data["result"].(map[string]any)["queued"])

	// Installing.
	_, res = env.call(http.MethodGet, "/api/v1/tv-updates/status", "c1", "", nil)
	assert.Equal(t, true, dataMap(t, res)["status"].(map[string]any)["installing"])

	// Later.
	code, res = env.call(http.MethodPost, "/api/v1/tv-updates/later", "c1", "", nil)
	require.Equal(t, 200, code)
	data = dataMap(t, res)
	assert.Equal(t, true, data["ok"])
	assert.Equal(t, "0eff7760", data["result"].(map[string]any)["dismissed"])

	// Nothing pending: the updater's 409 is ok=false with its error.
	code, res = env.call(http.MethodPost, "/api/v1/tv-updates/install", "c1", "", nil)
	require.Equal(t, 200, code)
	data = dataMap(t, res)
	assert.Equal(t, false, data["ok"])
	assert.Equal(t, "no pending build", data["result"].(map[string]any)["error"])

	assert.Equal(t, []string{"/install", "/later", "/install"}, f.posts)
	for _, tok := range f.tokens {
		assert.Equal(t, tvTestToken, tok)
	}
}

func TestTVUpdatesRequireAProfileWhenEnforced(t *testing.T) {
	env, _ := newM2Env(t)
	f := newFakeTVUpdater(t)
	env.app.TVUpdates = tvUpdatesService(t, f.srv.URL, nil)
	_, tok := env.session("Ana", "web-ana")

	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/tv-updates/status"},
		{http.MethodPost, "/api/v1/tv-updates/install"},
		{http.MethodPost, "/api/v1/tv-updates/later"},
		{http.MethodGet, "/api/v1/tv-updates/settings"},
		{http.MethodPatch, "/api/v1/tv-updates/settings"},
	} {
		code, _ := env.call(r.method, r.path, "web-ana", "", map[string]any{})
		assert.Equal(t, http.StatusUnauthorized, code, r.path)
	}
	assert.Zero(t, f.postCount(), "nothing reached the updater without a profile")

	code, res := env.call(http.MethodGet, "/api/v1/tv-updates/status", "web-ana", tok, nil)
	require.Equal(t, 200, code)
	assert.Equal(t, true, dataMap(t, res)["reachable"])
}

func TestTVUpdatesInstallRefusedDuringPlayback(t *testing.T) {
	env, _ := newM2Env(t)
	f := newFakeTVUpdater(t)
	env.app.TVUpdates = tvUpdatesService(t, f.srv.URL, nil)
	ana, tok := env.session("Ana", "web-ana")
	viewers := env.app.ViewerSessions()
	require.NotNil(t, viewers)

	// Someone watching in a browser doesn't block the TV.
	_, err := viewers.Acquire(sessions.AcquireRequest{ClientID: "laptop", ProfileID: 99, ProfileName: "Bob", Device: "Browser", MediaID: 1, Episode: 1})
	require.NoError(t, err)

	// A direct-play session on the TV blocks the install (the app would close).
	_, err = viewers.Acquire(sessions.AcquireRequest{ClientID: "tv", ProfileID: ana.ID, ProfileName: "Ana",
		Device: sessions.DeviceLabel("", "Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36"), MediaID: 1, Episode: 2})
	require.NoError(t, err)
	code, res := env.call(http.MethodPost, "/api/v1/tv-updates/install", "web-ana", tok, nil)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "PLAYBACK_ACTIVE: Ana is watching on the TV", res["error"])
	assert.Zero(t, f.postCount(), "the updater was not asked")

	// "Later" is never blocked.
	code, _ = env.call(http.MethodPost, "/api/v1/tv-updates/later", "web-ana", tok, nil)
	assert.Equal(t, 200, code)
	f.mu.Lock()
	f.pending = true
	f.mu.Unlock()

	// The session ends: the install goes through.
	require.True(t, viewers.Release("tv", 0, 0, sessions.ReasonStopped))
	code, res = env.call(http.MethodPost, "/api/v1/tv-updates/install", "web-ana", tok, nil)
	require.Equal(t, 200, code, res)
	assert.Equal(t, true, dataMap(t, res)["ok"])

	// A playback through the server (relayed lane): the server can't tell which
	// device, so it refuses too.
	require.True(t, viewers.Release("laptop", 0, 0, sessions.ReasonStopped))
	require.NoError(t, env.app.ProfilesRuntime.Lane.AcquireFor(ana, "web-ana"))
	code, res = env.call(http.MethodPost, "/api/v1/tv-updates/install", "web-ana", tok, nil)
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, "PLAYBACK_ACTIVE: Ana is watching through the server", res["error"])
}

// newTVUpdatesLoggedEnv is newProfilesTestEnv with every log line (requests
// included) written to logs.
func newTVUpdatesLoggedEnv(t *testing.T, logs *bytes.Buffer) *profilesTestEnv {
	t.Helper()
	logger := zerolog.New(logs).Level(zerolog.TraceLevel)
	t.Setenv("TEST_ENV", "true")
	database, err := db.NewDatabase("", "seanime", &logger)
	require.NoError(t, err)
	svc, err := profiles.Open(profiles.Options{DataDir: t.TempDir(), Logger: &logger})
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Close() })
	app := &core.App{
		Config:               &core.Config{},
		Logger:               &logger,
		Database:             database,
		FeatureManager:       core.NewFeatureManager(&logger, core.SeanimeFlags{}),
		ClientIdentitySecret: "test-client-identity-secret",
		Profiles:             svc,
	}
	app.TVUpdates = tvupdates.New(tvupdates.Options{DataDir: t.TempDir(), Logger: &logger, Getenv: func(string) string { return "" }})
	e := echo.New()
	e.JSONSerializer = &core.CustomJSONSerializer{}
	InitRoutes(app, e)
	return &profilesTestEnv{t: t, e: e, app: app, svc: svc, proof: map[string]string{}}
}

func TestTVUpdatesTokenNeverLeaks(t *testing.T) {
	var logs bytes.Buffer
	env := newTVUpdatesLoggedEnv(t, &logs)
	f := newFakeTVUpdater(t)

	var bodies []string
	collect := func(code int, body string) {
		t.Helper()
		bodies = append(bodies, body)
	}

	// Saved from the web settings.
	code, body := env.rawCall(http.MethodPatch, "/api/v1/tv-updates/settings", "c1", "", map[string]any{"url": f.srv.URL, "token": tvTestToken})
	require.Equal(t, 200, code, body)
	collect(code, body)
	var view struct {
		Data tvupdates.SettingsView `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &view))
	assert.True(t, view.Data.TokenConfigured)
	assert.True(t, view.Data.Configured)

	collect(env.rawCall(http.MethodGet, "/api/v1/tv-updates/settings", "c1", "", nil))
	collect(env.rawCall(http.MethodGet, "/api/v1/tv-updates/status", "c1", "", nil))
	code, body = env.rawCall(http.MethodPost, "/api/v1/tv-updates/install", "c1", "", nil)
	require.Equal(t, 200, code, body)
	collect(code, body)
	collect(env.rawCall(http.MethodPost, "/api/v1/tv-updates/later", "c1", "", nil))
	// A token in the URL is refused, and the error doesn't repeat it.
	collect(env.rawCall(http.MethodPatch, "/api/v1/tv-updates/settings", "c1", "", map[string]any{"url": "http://u:" + tvTestToken + "@h"}))
	// The status of an unreachable updater.
	f.srv.Close()
	collect(env.rawCall(http.MethodGet, "/api/v1/tv-updates/status", "c1", "", nil))
	collect(env.rawCall(http.MethodPost, "/api/v1/tv-updates/install", "c1", "", nil))

	require.Equal(t, []string{tvTestToken, tvTestToken}, f.tokens, "the updater got the token")
	for _, b := range bodies {
		assert.NotContains(t, b, tvTestToken)
	}
	assert.NotEmpty(t, logs.String())
	assert.NotContains(t, logs.String(), tvTestToken)
}

func TestTVUpdatesSettingsErrors(t *testing.T) {
	env := newProfilesTestEnv(t)
	vars := map[string]string{tvupdates.EnvURL: "http://tv-updater:8095"}
	env.app.TVUpdates = tvupdates.New(tvupdates.Options{Getenv: func(k string) string { return vars[k] }})

	code, res := env.call(http.MethodPatch, "/api/v1/tv-updates/settings", "c1", "", map[string]any{"url": "http://other:1"})
	assert.Equal(t, http.StatusConflict, code)
	assert.True(t, strings.HasPrefix(res["error"].(string), "TV_UPDATER_SET_BY_ENV"), res)

	// The token can still be saved when only the URL comes from the environment.
	code, res = env.call(http.MethodPatch, "/api/v1/tv-updates/settings", "c1", "", map[string]any{"token": "abc"})
	require.Equal(t, 200, code, res)
	data := dataMap(t, res)
	assert.Equal(t, true, data["urlFromEnv"])
	assert.Equal(t, true, data["tokenConfigured"])
	assert.Equal(t, "http://tv-updater:8095", data["url"])

	env.app.TVUpdates = tvupdates.New(tvupdates.Options{Getenv: func(string) string { return "" }})
	code, res = env.call(http.MethodPatch, "/api/v1/tv-updates/settings", "c1", "", map[string]any{"url": "not a url"})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.True(t, strings.HasPrefix(res["error"].(string), "TV_UPDATER_INVALID_SETTINGS"), res)
}
