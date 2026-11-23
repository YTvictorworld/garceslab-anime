package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"seanime/internal/core"
	"seanime/internal/database/db"
	"seanime/internal/profiles"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type profilesTestEnv struct {
	t     *testing.T
	e     *echo.Echo
	app   *core.App
	svc   *profiles.Service
	proof map[string]string
}

func newProfilesTestEnv(t *testing.T) *profilesTestEnv {
	t.Helper()
	logger := zerolog.Nop()
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
	e := echo.New()
	e.JSONSerializer = &core.CustomJSONSerializer{}
	InitRoutes(app, e)
	return &profilesTestEnv{t: t, e: e, app: app, svc: svc, proof: map[string]string{}}
}

// call sends a request as a signed client (clientID != "") with an optional profile token.
func (env *profilesTestEnv) call(method, path, clientID, token string, body any) (int, map[string]any) {
	env.t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
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
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func dataMap(t *testing.T, res map[string]any) map[string]any {
	t.Helper()
	m, ok := res["data"].(map[string]any)
	require.True(t, ok, "response: %v", res)
	return m
}

func TestProfilesEndpointsFlow(t *testing.T) {
	env := newProfilesTestEnv(t)

	// First run: needsSetup, no current profile.
	code, res := env.call("GET", "/api/v1/status", "c1", "", nil)
	require.Equal(t, 200, code)
	st := dataMap(t, res)
	pst := st["profiles"].(map[string]any)
	assert.Equal(t, true, pst["enabled"])
	assert.Equal(t, true, pst["needsSetup"])
	assert.Equal(t, float64(3), pst["maxLanes"])
	assert.Nil(t, st["currentProfile"])

	// Avatars.
	code, res = env.call("GET", "/api/v1/avatars", "c1", "", nil)
	require.Equal(t, 200, code)
	cat := res["data"].([]any)
	require.NotEmpty(t, cat)
	first := cat[0].(map[string]any)
	avatarID := first["id"].(string)

	req := httptest.NewRequest("GET", "/profile-avatars/"+first["file"].(string), nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	assert.Equal(t, 200, rec.Code)
	assert.Equal(t, "image/svg+xml", rec.Header().Get("Content-Type"))
	rec = httptest.NewRecorder()
	env.e.ServeHTTP(rec, httptest.NewRequest("GET", "/profile-avatars/nope.svg", nil))
	assert.Equal(t, 404, rec.Code)

	// Create.
	code, res = env.call("POST", "/api/v1/profiles", "c1", "", map[string]any{"name": "Ana", "avatarId": avatarID})
	require.Equal(t, 200, code, res)
	ana := dataMap(t, res)
	anaID := int64(ana["id"].(float64))
	assert.Equal(t, false, ana["hasPin"])
	assert.NotContains(t, ana, "pinHash")
	code, _ = env.call("POST", "/api/v1/profiles", "c1", "", map[string]any{"name": "ana", "avatarId": avatarID})
	assert.Equal(t, http.StatusConflict, code)
	code, _ = env.call("POST", "/api/v1/profiles", "c1", "", map[string]any{"name": "Bea", "avatarId": "nope"})
	assert.Equal(t, http.StatusBadRequest, code)

	// Select without PIN (no body).
	code, res = env.call("POST", "/api/v1/profiles/"+itoa(anaID)+"/select", "c1", "", nil)
	require.Equal(t, 200, code, res)
	sess := dataMap(t, res)
	token := sess["profileToken"].(string)
	assert.NotEmpty(t, token)
	assert.NotZero(t, sess["expiresAt"])

	// Status with the token.
	code, res = env.call("GET", "/api/v1/status", "c1", token, nil)
	require.Equal(t, 200, code)
	st = dataMap(t, res)
	assert.Equal(t, false, st["profiles"].(map[string]any)["needsSetup"])
	cur := st["currentProfile"].(map[string]any)
	assert.Equal(t, "Ana", cur["name"])

	// Same token from another client: not resolved.
	code, res = env.call("GET", "/api/v1/status", "c2", token, nil)
	require.Equal(t, 200, code)
	assert.Nil(t, dataMap(t, res)["currentProfile"])

	// PIN flow.
	code, _ = env.call("PUT", "/api/v1/profiles/"+itoa(anaID)+"/pin", "c1", token, map[string]any{"pin": "12"})
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = env.call("PUT", "/api/v1/profiles/"+itoa(anaID)+"/pin", "c1", token, map[string]any{"pin": "2468"})
	require.Equal(t, 200, code)
	code, res = env.call("GET", "/api/v1/status", "c1", token, nil)
	require.Equal(t, 200, code)
	assert.Nil(t, dataMap(t, res)["currentProfile"], "PIN change invalidates the session")
	code, res = env.call("POST", "/api/v1/profiles/"+itoa(anaID)+"/select", "c1", "", nil)
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "PROFILE_PIN_REQUIRED", res["error"])
	code, res = env.call("POST", "/api/v1/profiles/"+itoa(anaID)+"/select", "c1", "", map[string]any{"pin": "0000"})
	assert.Equal(t, http.StatusForbidden, code)
	assert.Equal(t, "PROFILE_WRONG_PIN", res["error"])
	code, res = env.call("POST", "/api/v1/profiles/"+itoa(anaID)+"/select", "c1", "", map[string]any{"pin": "2468"})
	require.Equal(t, 200, code)
	token = dataMap(t, res)["profileToken"].(string)

	// List, update, delete, restore.
	code, res = env.call("GET", "/api/v1/profiles", "c1", token, nil)
	require.Equal(t, 200, code)
	list := res["data"].([]any)
	require.Len(t, list, 1)
	assert.Equal(t, true, list[0].(map[string]any)["hasPin"])

	code, res = env.call("PATCH", "/api/v1/profiles/"+itoa(anaID), "c1", token, map[string]any{"name": "Ana María", "color": "#336699"})
	require.Equal(t, 200, code, res)
	assert.Equal(t, "Ana María", dataMap(t, res)["name"])

	code, _ = env.call("DELETE", "/api/v1/profiles/"+itoa(anaID), "c1", token, nil)
	require.Equal(t, 200, code)
	code, res = env.call("GET", "/api/v1/profiles?deleted=true", "c1", "", nil)
	require.Equal(t, 200, code)
	assert.Len(t, res["data"].([]any), 1)
	code, res = env.call("GET", "/api/v1/status", "c1", "", nil)
	assert.Equal(t, true, dataMap(t, res)["profiles"].(map[string]any)["needsSetup"])
	code, _ = env.call("POST", "/api/v1/profiles/"+itoa(anaID)+"/restore", "c1", "", nil)
	require.Equal(t, 200, code)
	code, _ = env.call("DELETE", "/api/v1/profiles/999", "c1", "", nil)
	assert.Equal(t, http.StatusNotFound, code)

	code, res = env.call("GET", "/api/v1/profiles/audit", "c1", "", nil)
	require.Equal(t, 200, code)
	assert.NotEmpty(t, res["data"])

	code, _ = env.call("POST", "/api/v1/profiles/logout", "c1", "", nil)
	assert.Equal(t, 200, code)
}

func TestProfilesSelectSetsHttpOnlyCookie(t *testing.T) {
	env := newProfilesTestEnv(t)
	p, err := env.svc.Create(t.Context(), profiles.CreateInput{Name: "Ana", AvatarID: "gl-lorelei-01"})
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "http://127.0.0.1:43211/api/v1/profiles/"+itoa(p.ID)+"/select", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set(clientIdHeaderName, "c1")
	req.Header.Set(clientIdProofHeaderName, generateClientIdentityProof(env.app, "c1"))
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)

	var cookie *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == profiles.CookieName {
			cookie = ck
		}
	}
	require.NotNil(t, cookie)
	assert.True(t, cookie.HttpOnly)
	assert.True(t, cookie.Expires.IsZero(), "session cookie")

	// The cookie alone resolves the profile.
	req = httptest.NewRequest("GET", "http://127.0.0.1:43211/api/v1/status", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set(clientIdHeaderName, "c1")
	req.Header.Set(clientIdProofHeaderName, generateClientIdentityProof(env.app, "c1"))
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	assert.Contains(t, rec.Body.String(), `"currentProfile":{"id":`+itoa(p.ID))
}

func TestProfilesEnforcementFlag(t *testing.T) {
	env := newProfilesTestEnv(t)
	p, err := env.svc.Create(t.Context(), profiles.CreateInput{Name: "Ana", AvatarID: "gl-lorelei-01"})
	require.NoError(t, err)
	sess, err := env.svc.Select(t.Context(), p.ID, "", "c1", "")
	require.NoError(t, err)

	// Off (default): user-data routes keep working without a profile.
	code, res := env.call("GET", "/api/v1/theme", "c1", "", nil)
	assert.Equal(t, 200, code, res)

	env.svc.SetEnforced(true)
	code, res = env.call("GET", "/api/v1/theme", "c1", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	assert.Equal(t, "PROFILE_REQUIRED", res["error"])
	code, _ = env.call("GET", "/api/v1/theme", "c1", sess.ProfileToken, nil)
	assert.Equal(t, 200, code)
	code, _ = env.call("GET", "/api/v1/theme", "c2", sess.ProfileToken, nil)
	assert.Equal(t, http.StatusUnauthorized, code, "token bound to c1")

	// Public routes still work without a profile.
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/v1/status"},
		{"GET", "/api/v1/profiles"},
		{"GET", "/api/v1/avatars"},
	} {
		code, res = env.call(r.method, r.path, "c2", "", nil)
		assert.Equal(t, 200, code, "%v %v", r, res)
	}
	code, _ = env.call("POST", "/api/v1/profiles/"+itoa(p.ID)+"/select", "c2", "", nil)
	assert.Equal(t, 200, code)
	// Management of existing profiles needs a profile when enforced.
	code, _ = env.call("DELETE", "/api/v1/profiles/"+itoa(p.ID), "c2", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestProfilesServerPasswordStaysFirst(t *testing.T) {
	env := newProfilesTestEnv(t)
	env.app.Config.Server.Password = "secret"
	env.app.ServerPasswordHash = "hash"
	// Rebuild routes so the auth middleware sees the password.
	env.e = echo.New()
	env.e.JSONSerializer = &core.CustomJSONSerializer{}
	InitRoutes(env.app, env.e)

	code, _ := env.call("GET", "/api/v1/profiles", "c1", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code)
	code, _ = env.call("POST", "/api/v1/profiles", "c1", "", map[string]any{"name": "Ana", "avatarId": "gl-lorelei-01"})
	assert.Equal(t, http.StatusUnauthorized, code)
	needs, err := env.svc.NeedsSetup(t.Context())
	require.NoError(t, err)
	assert.True(t, needs)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
