package tvupdates

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secret = "s3cr3t-updater-token-0123456789"

// fakeUpdater imitates the tv-updater HTTP API (GET /status, POST /install,
// POST /later with X-Updater-Token).
type fakeUpdater struct {
	mu       sync.Mutex
	Token    string
	Pending  bool
	Queued   bool
	Calls    []string
	Tokens   []string
	Delay    time.Duration
	srv      *httptest.Server
	BadReply bool
}

func newFakeUpdater(t *testing.T, token string) *fakeUpdater {
	f := &fakeUpdater{Token: token, Pending: true}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeUpdater) URL() string { return f.srv.URL }

func (f *fakeUpdater) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.Calls = append(f.Calls, r.Method+" "+r.URL.Path)
	f.Tokens = append(f.Tokens, r.Header.Get(TokenHeader))
	delay, pending, queued, token, bad := f.Delay, f.Pending, f.Queued, f.Token, f.BadReply
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if bad {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("<html>not json</html>"))
		return
	}
	reply := func(code int, v any) {
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /status":
		st := map[string]any{
			"channel":    "nightly",
			"installed":  map[string]any{"version": "1.0.0", "commit": "808b9ee0", "shortCommit": "808b9ee"},
			"pending":    nil,
			"tvOnline":   true,
			"installing": queued,
			"lastResult": nil,
		}
		if pending {
			st["pending"] = map[string]any{
				"version": "1.0.0", "commit": "0eff7760", "shortCommit": "0eff776", "builtAt": "2026-11-26T19:03:40.416Z",
				"changes":         []any{map[string]any{"type": "feat", "title": "Seasons tabs", "pr": 21}, map[string]any{"type": "fix", "title": "Spanish subtitles", "pr": 22}},
				"changesComplete": true,
			}
		}
		if queued {
			st["installPhase"] = "waiting-tv"
		}
		reply(200, st)
	case "POST /install", "POST /later":
		if token != "" && r.Header.Get(TokenHeader) != token {
			reply(401, map[string]any{"ok": false, "error": "invalid or missing X-Updater-Token"})
			return
		}
		if r.URL.Path == "/install" {
			if queued {
				reply(202, map[string]any{"ok": true, "alreadyQueued": true, "commit": "0eff7760"})
				return
			}
			if !pending {
				reply(409, map[string]any{"ok": false, "error": "no pending build"})
				return
			}
			f.mu.Lock()
			f.Queued = true
			f.mu.Unlock()
			reply(202, map[string]any{"ok": true, "queued": true, "commit": "0eff7760", "version": "1.0.0"})
			return
		}
		if !pending && !queued {
			reply(409, map[string]any{"ok": false, "error": "no pending build"})
			return
		}
		f.mu.Lock()
		f.Pending, f.Queued = false, false
		f.mu.Unlock()
		reply(200, map[string]any{"ok": true, "dismissed": "0eff7760", "cancelledQueue": queued})
	default:
		reply(404, map[string]any{"ok": false, "error": "not found"})
	}
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func newSvc(t *testing.T, vars map[string]string, logs *bytes.Buffer) *Service {
	t.Helper()
	var logger zerolog.Logger
	if logs != nil {
		logger = zerolog.New(logs).Level(zerolog.DebugLevel)
	} else {
		logger = zerolog.Nop()
	}
	return New(Options{DataDir: t.TempDir(), Logger: &logger, Getenv: env(vars), StatusTimeout: 300 * time.Millisecond, ActionTimeout: 300 * time.Millisecond})
}

func TestNotConfigured(t *testing.T) {
	s := newSvc(t, nil, nil)
	assert.False(t, s.Configured())
	b, err := json.Marshal(s.Status(context.Background()))
	require.NoError(t, err)
	assert.JSONEq(t, `{"configured": false}`, string(b))
	_, err = s.Install(context.Background())
	assert.ErrorIs(t, err, ErrNotConfigured)
	_, err = s.Later(context.Background())
	assert.ErrorIs(t, err, ErrNotConfigured)

	var nilSvc *Service
	assert.False(t, nilSvc.Configured())
	assert.False(t, nilSvc.Status(context.Background()).Configured)
}

func TestStatusPassesThroughUnchanged(t *testing.T) {
	f := newFakeUpdater(t, secret)
	s := newSvc(t, map[string]string{EnvURL: f.URL() + "/", EnvToken: secret}, nil)
	res := s.Status(context.Background())
	require.True(t, res.Configured)
	require.NotNil(t, res.Reachable)
	require.True(t, *res.Reachable)
	assert.Empty(t, res.Error)

	var st map[string]any
	require.NoError(t, json.Unmarshal(res.Status, &st))
	assert.Equal(t, "nightly", st["channel"])
	assert.Equal(t, false, st["installing"])
	pending := st["pending"].(map[string]any)
	assert.Equal(t, "0eff776", pending["shortCommit"])
	assert.Len(t, pending["changes"], 2)
	assert.Equal(t, []string{"GET /status"}, f.Calls, "the trailing slash of the URL is dropped")
	assert.Equal(t, "", f.Tokens[0], "GET /status doesn't need the token")
}

func TestStatusInstalling(t *testing.T) {
	f := newFakeUpdater(t, "")
	f.Queued = true
	s := newSvc(t, map[string]string{EnvURL: f.URL()}, nil)
	var st map[string]any
	require.NoError(t, json.Unmarshal(s.Status(context.Background()).Status, &st))
	assert.Equal(t, true, st["installing"])
	assert.Equal(t, "waiting-tv", st["installPhase"], "extra fields pass through")
}

func TestStatusUnreachable(t *testing.T) {
	// A port nobody listens on.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())

	s := newSvc(t, map[string]string{EnvURL: "http://" + addr}, nil)
	res := s.Status(context.Background())
	b, _ := json.Marshal(res)
	assert.True(t, res.Configured)
	require.NotNil(t, res.Reachable)
	assert.False(t, *res.Reachable)
	assert.NotEmpty(t, res.Error)
	assert.NotContains(t, string(b), `"status"`)
}

func TestStatusTimeout(t *testing.T) {
	f := newFakeUpdater(t, "")
	f.Delay = 2 * time.Second
	s := newSvc(t, map[string]string{EnvURL: f.URL()}, nil)
	start := time.Now()
	res := s.Status(context.Background())
	assert.Less(t, time.Since(start), 1500*time.Millisecond)
	require.NotNil(t, res.Reachable)
	assert.False(t, *res.Reachable)
	assert.Contains(t, res.Error, "did not answer within")
}

func TestStatusNotJSON(t *testing.T) {
	f := newFakeUpdater(t, "")
	f.BadReply = true
	s := newSvc(t, map[string]string{EnvURL: f.URL()}, nil)
	res := s.Status(context.Background())
	require.NotNil(t, res.Reachable)
	assert.False(t, *res.Reachable)
	assert.Contains(t, res.Error, "JSON")
}

func TestInstallAndLaterSendTheToken(t *testing.T) {
	f := newFakeUpdater(t, secret)
	var logs bytes.Buffer
	s := newSvc(t, map[string]string{EnvURL: f.URL(), EnvToken: secret}, &logs)

	res, err := s.Install(context.Background())
	require.NoError(t, err)
	assert.True(t, res.OK)
	assert.JSONEq(t, `{"ok":true,"queued":true,"commit":"0eff7760","version":"1.0.0"}`, string(res.Result))

	res, err = s.Install(context.Background())
	require.NoError(t, err)
	assert.True(t, res.OK)
	assert.Contains(t, string(res.Result), "alreadyQueued")

	res, err = s.Later(context.Background())
	require.NoError(t, err)
	assert.True(t, res.OK)
	assert.Contains(t, string(res.Result), `"cancelledQueue":true`)

	// Nothing pending any more: the updater's 409 comes back as ok=false.
	res, err = s.Install(context.Background())
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, string(res.Result), "no pending build")

	for i, call := range f.Calls {
		assert.Equal(t, secret, f.Tokens[i], call)
	}
	assert.NotContains(t, logs.String(), secret)
}

func TestWrongTokenIsNotOK(t *testing.T) {
	f := newFakeUpdater(t, secret)
	s := newSvc(t, map[string]string{EnvURL: f.URL(), EnvToken: "wrong"}, nil)
	res, err := s.Install(context.Background())
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, string(res.Result), "X-Updater-Token")
}

func TestActionUnreachable(t *testing.T) {
	f := newFakeUpdater(t, "")
	f.Delay = 2 * time.Second
	s := newSvc(t, map[string]string{EnvURL: f.URL()}, nil)
	_, err := s.Install(context.Background())
	assert.ErrorIs(t, err, ErrUnreachable)
}

func TestSettingsSavedAndTokenNeverShown(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	logger := zerolog.New(&logs).Level(zerolog.DebugLevel)
	s := New(Options{DataDir: dir, Logger: &logger, Getenv: env(nil)})

	u, tok := "http://tv-updater:8095/", secret
	view, err := s.Update(SettingsPatch{URL: &u, Token: &tok})
	require.NoError(t, err)
	assert.Equal(t, SettingsView{Configured: true, URL: "http://tv-updater:8095", TokenConfigured: true}, view)
	b, _ := json.Marshal(view)
	assert.NotContains(t, string(b), secret)
	assert.NotContains(t, logs.String(), secret)

	path := filepath.Join(dir, SettingsFileName)
	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	// Reloaded from disk.
	s2 := New(Options{DataDir: dir, Getenv: env(nil)})
	assert.Equal(t, view, s2.View())

	// Only the URL changes; the token is kept. "" clears.
	u2 := "https://updates.example.test/base"
	view, err = s2.Update(SettingsPatch{URL: &u2})
	require.NoError(t, err)
	assert.True(t, view.TokenConfigured)
	assert.Equal(t, u2, view.URL)
	empty := ""
	view, err = s2.Update(SettingsPatch{Token: &empty})
	require.NoError(t, err)
	assert.False(t, view.TokenConfigured)
	view, err = s2.Update(SettingsPatch{URL: &empty})
	require.NoError(t, err)
	assert.False(t, view.Configured)
}

func TestSettingsRejectBadURLs(t *testing.T) {
	s := newSvc(t, nil, nil)
	for _, bad := range []string{"tv-updater:8095", "ftp://x", "http://", "http://user:" + secret + "@host", "http://host/?token=x", strings.Repeat("a", 600)} {
		_, err := s.Update(SettingsPatch{URL: &bad})
		assert.ErrorIs(t, err, ErrInvalidSettings, bad)
		if err != nil {
			assert.NotContains(t, err.Error(), secret)
		}
	}
	assert.False(t, s.Configured())
}

func TestEnvWinsOverSavedSettings(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{DataDir: dir, Getenv: env(nil)})
	u, tok := "http://saved:1", "saved-token"
	_, err := s.Update(SettingsPatch{URL: &u, Token: &tok})
	require.NoError(t, err)

	f := newFakeUpdater(t, secret)
	s = New(Options{DataDir: dir, Getenv: env(map[string]string{EnvURL: f.URL(), EnvToken: secret})})
	view := s.View()
	assert.Equal(t, f.URL(), view.URL)
	assert.True(t, view.URLFromEnv)
	assert.True(t, view.TokenFromEnv)
	_, err = s.Update(SettingsPatch{URL: &u})
	assert.ErrorIs(t, err, ErrSetByEnv)
	_, err = s.Update(SettingsPatch{Token: &tok})
	assert.ErrorIs(t, err, ErrSetByEnv)

	res, err := s.Install(context.Background())
	require.NoError(t, err)
	assert.True(t, res.OK, "the token from the environment is used")

	// Only the URL from the environment: the saved token is used.
	s = New(Options{DataDir: dir, Getenv: env(map[string]string{EnvURL: f.URL()})})
	assert.False(t, s.View().TokenFromEnv)
	assert.True(t, s.View().TokenConfigured)
}

func TestTokenFromFile(t *testing.T) {
	f := newFakeUpdater(t, secret)
	file := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(file, []byte(secret+"\n"), 0o600))
	s := New(Options{Getenv: env(map[string]string{EnvURL: f.URL(), EnvTokenFile: file})})
	assert.True(t, s.View().TokenFromEnv)
	res, err := s.Later(context.Background())
	require.NoError(t, err)
	assert.True(t, res.OK)
}
