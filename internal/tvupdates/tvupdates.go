// Package tvupdates is Garceslab Anime's proxy to an optional, self-hosted TV
// app updater (docs/tv-updates.md, issue #18).
//
// The updater is a separate service that downloads new builds of the TV app,
// keeps one "pending" and installs it on the TV only when someone confirms. The
// TV app and the web interface never talk to it directly: they ask this server,
// which forwards to the updater with a shared token. The token stays on the
// server; clients only learn whether one is configured.
package tvupdates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	// EnvURL is the updater's base URL (e.g. http://tv-updater:8095). Set, it
	// wins over the URL saved in the web settings.
	EnvURL = "SEANIME_TV_UPDATER_URL"
	// EnvToken is the updater's shared token (its UPDATER_TOKEN). Set, it wins
	// over the token saved in the web settings.
	EnvToken = "SEANIME_TV_UPDATER_TOKEN"
	// EnvTokenFile names a file holding the token (Docker secrets). EnvToken
	// wins when both are set.
	EnvTokenFile = "SEANIME_TV_UPDATER_TOKEN_FILE"

	// TokenHeader is the header the updater checks on POST requests.
	TokenHeader = "X-Updater-Token"

	// SettingsFileName is the file in the data directory with the saved settings.
	SettingsFileName = "tv-updates.json"

	defaultStatusTimeout = 3 * time.Second
	defaultActionTimeout = 10 * time.Second
	maxResponseBytes     = 1 << 20
	maxURLLength         = 512
	maxTokenLength       = 512
)

var (
	// ErrNotConfigured: no updater URL (404).
	ErrNotConfigured = errors.New("TV_UPDATER_NOT_CONFIGURED")
	// ErrUnreachable: the updater didn't answer, or not with JSON (502).
	ErrUnreachable = errors.New("TV_UPDATER_UNREACHABLE")
	// ErrInvalidSettings: a bad URL or token in a settings change (400).
	ErrInvalidSettings = errors.New("TV_UPDATER_INVALID_SETTINGS")
	// ErrSetByEnv: the setting comes from an environment variable and can't be
	// changed from the web (409).
	ErrSetByEnv = errors.New("TV_UPDATER_SET_BY_ENV")
)

// StatusResponse is GET /api/v1/tv-updates/status.
//
//   - not configured: {"configured": false}
//   - unreachable:    {"configured": true, "reachable": false, "error": "..."}
//   - ok:             {"configured": true, "reachable": true, "status": <updater GET /status>}
type StatusResponse struct {
	Configured bool `json:"configured"`
	// Reachable is omitted when the updater is not configured.
	Reachable *bool  `json:"reachable,omitempty"`
	Error     string `json:"error,omitempty"`
	// Status is the updater's GET /status JSON, passed through unchanged.
	Status json.RawMessage `json:"status,omitempty"`
}

// ActionResponse is POST /api/v1/tv-updates/install and /later: the updater's
// answer. OK is true when the updater answered 2xx and its JSON doesn't say
// "ok": false. Result is the updater's JSON, unchanged.
type ActionResponse struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
}

// SettingsView is what clients see of the settings: never the token.
type SettingsView struct {
	// Configured is true when an updater URL is set (saved or from the environment).
	Configured bool `json:"configured"`
	// URL is the effective updater URL ("" when not set).
	URL string `json:"url"`
	// TokenConfigured is true when a token is set; the token itself is never returned.
	TokenConfigured bool `json:"tokenConfigured"`
	// URLFromEnv / TokenFromEnv: the value comes from SEANIME_TV_UPDATER_URL /
	// SEANIME_TV_UPDATER_TOKEN(_FILE) and can't be changed from the web.
	URLFromEnv   bool `json:"urlFromEnv"`
	TokenFromEnv bool `json:"tokenFromEnv"`
}

// SettingsPatch changes the saved settings. A nil field keeps its value; an
// empty string clears it.
type SettingsPatch struct {
	URL   *string `json:"url,omitempty"`
	Token *string `json:"token,omitempty"`
}

// stored is the settings file. The token is in it: the file is 0600 in the
// data directory, like the rest of the server's secrets.
type stored struct {
	URL   string `json:"url,omitempty"`
	Token string `json:"token,omitempty"`
}

// Options configure a Service. Zero values take the defaults.
type Options struct {
	// DataDir holds tv-updates.json. Empty: settings are kept in memory only.
	DataDir string
	Logger  *zerolog.Logger
	// Getenv reads the environment (os.Getenv by default; tests replace it).
	Getenv        func(string) string
	HTTPClient    *http.Client
	StatusTimeout time.Duration
	ActionTimeout time.Duration
}

// Service holds the settings and talks to the updater.
type Service struct {
	mu       sync.RWMutex
	path     string
	saved    stored
	envURL   string
	envToken string
	logger   *zerolog.Logger
	client   *http.Client
	statusTO time.Duration
	actionTO time.Duration
}

// New loads the saved settings and reads the environment. An unreadable
// settings file is logged and ignored (the feature stays off), never fatal.
func New(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		nop := zerolog.Nop()
		logger = &nop
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	s := &Service{
		logger:   logger,
		client:   opts.HTTPClient,
		statusTO: opts.StatusTimeout,
		actionTO: opts.ActionTimeout,
	}
	if s.client == nil {
		// No redirects: the token must never be sent to another host.
		s.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	if s.statusTO <= 0 {
		s.statusTO = defaultStatusTimeout
	}
	if s.actionTO <= 0 {
		s.actionTO = defaultActionTimeout
	}

	if v := strings.TrimSpace(getenv(EnvURL)); v != "" {
		if u, err := normalizeURL(v); err != nil {
			logger.Warn().Msgf("tvupdates: Ignoring %s: %v", EnvURL, err)
		} else {
			s.envURL = u
		}
	}
	s.envToken = strings.TrimSpace(getenv(EnvToken))
	if s.envToken == "" {
		if f := strings.TrimSpace(getenv(EnvTokenFile)); f != "" {
			b, err := os.ReadFile(f)
			if err != nil {
				// The path is not secret; the content is never logged.
				logger.Warn().Msgf("tvupdates: Cannot read %s (%s)", EnvTokenFile, errors.Unwrap(err))
			} else {
				s.envToken = strings.TrimSpace(string(b))
			}
		}
	}

	if opts.DataDir != "" {
		s.path = filepath.Join(opts.DataDir, SettingsFileName)
		b, err := os.ReadFile(s.path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &s.saved); err != nil {
				logger.Warn().Msgf("tvupdates: Ignoring %s: invalid JSON", SettingsFileName)
				s.saved = stored{}
			}
		case !errors.Is(err, os.ErrNotExist):
			logger.Warn().Msgf("tvupdates: Cannot read %s", SettingsFileName)
		}
	}
	return s
}

// effective returns the URL and token in use: the environment wins over the
// saved settings, field by field.
func (s *Service) effective() (string, string) {
	if s == nil {
		return "", ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, t := s.saved.URL, s.saved.Token
	if s.envURL != "" {
		u = s.envURL
	}
	if s.envToken != "" {
		t = s.envToken
	}
	return u, t
}

// Configured reports whether an updater URL is set.
func (s *Service) Configured() bool {
	u, _ := s.effective()
	return u != ""
}

// View returns the settings as clients see them.
func (s *Service) View() SettingsView {
	if s == nil {
		return SettingsView{}
	}
	u, t := s.effective()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SettingsView{
		Configured:      u != "",
		URL:             u,
		TokenConfigured: t != "",
		URLFromEnv:      s.envURL != "",
		TokenFromEnv:    s.envToken != "",
	}
}

// Update changes the saved settings and writes them to the data directory.
func (s *Service) Update(p SettingsPatch) (SettingsView, error) {
	if s == nil {
		return SettingsView{}, ErrNotConfigured
	}
	s.mu.Lock()
	next := s.saved
	if p.URL != nil {
		if s.envURL != "" {
			s.mu.Unlock()
			return SettingsView{}, fmt.Errorf("%w: the URL is set by %s", ErrSetByEnv, EnvURL)
		}
		v := strings.TrimSpace(*p.URL)
		if v == "" {
			next.URL = ""
		} else {
			u, err := normalizeURL(v)
			if err != nil {
				s.mu.Unlock()
				return SettingsView{}, fmt.Errorf("%w: %v", ErrInvalidSettings, err)
			}
			next.URL = u
		}
	}
	if p.Token != nil {
		if s.envToken != "" {
			s.mu.Unlock()
			return SettingsView{}, fmt.Errorf("%w: the token is set by %s", ErrSetByEnv, EnvToken)
		}
		v := strings.TrimSpace(*p.Token)
		if len(v) > maxTokenLength || strings.ContainsAny(v, "\r\n") {
			s.mu.Unlock()
			return SettingsView{}, fmt.Errorf("%w: the token is too long or has line breaks", ErrInvalidSettings)
		}
		next.Token = v
	}
	if err := s.writeLocked(next); err != nil {
		s.mu.Unlock()
		s.logger.Error().Msgf("tvupdates: Cannot save %s: %v", SettingsFileName, err)
		return SettingsView{}, errors.New("could not save the TV updater settings")
	}
	s.saved = next
	s.mu.Unlock()
	s.logger.Info().Bool("configured", next.URL != "" || s.envURL != "").Msg("tvupdates: Settings saved")
	return s.View(), nil
}

// writeLocked writes the settings file atomically with mode 0600.
func (s *Service) writeLocked(v stored) error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tv-updates-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// normalizeURL accepts http(s)://host[:port][/path] without credentials,
// query or fragment, and drops a trailing slash.
func normalizeURL(raw string) (string, error) {
	if len(raw) > maxURLLength {
		return "", errors.New("the URL is too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("the URL is not valid")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("the URL must start with http:// or https://")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", errors.New("the URL has no host")
	}
	if u.User != nil {
		return "", errors.New("put the token in the token field, not in the URL")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("the URL must not have a query or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

// Status asks the updater for its GET /status. It never returns an error: an
// unreachable updater is a state clients show.
func (s *Service) Status(ctx context.Context) StatusResponse {
	base, _ := s.effective()
	if base == "" {
		return StatusResponse{Configured: false}
	}
	ctx, cancel := context.WithTimeout(ctx, s.statusTO)
	defer cancel()
	code, body, err := s.do(ctx, http.MethodGet, base+"/status", "")
	if err == nil && (code < 200 || code > 299) {
		err = fmt.Errorf("the updater answered HTTP %d", code)
	}
	if err == nil && !isJSONObject(body) {
		err = errors.New("the updater did not answer with a JSON object")
	}
	if err != nil {
		f := false
		msg := shortError(err, s.statusTO)
		s.logger.Debug().Msgf("tvupdates: Updater status: %s", msg)
		return StatusResponse{Configured: true, Reachable: &f, Error: msg}
	}
	t := true
	return StatusResponse{Configured: true, Reachable: &t, Status: json.RawMessage(body)}
}

// Install confirms the pending build: POST /install on the updater.
func (s *Service) Install(ctx context.Context) (ActionResponse, error) {
	return s.action(ctx, "/install")
}

// Later dismisses the pending build: POST /later on the updater.
func (s *Service) Later(ctx context.Context) (ActionResponse, error) {
	return s.action(ctx, "/later")
}

func (s *Service) action(ctx context.Context, path string) (ActionResponse, error) {
	base, token := s.effective()
	if base == "" {
		return ActionResponse{}, ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, s.actionTO)
	defer cancel()
	code, body, err := s.do(ctx, http.MethodPost, base+path, token)
	if err == nil && !isJSONObject(body) {
		err = fmt.Errorf("the updater answered HTTP %d without a JSON object", code)
	}
	if err != nil {
		msg := shortError(err, s.actionTO)
		s.logger.Warn().Msgf("tvupdates: POST %s failed: %s", path, msg)
		return ActionResponse{}, fmt.Errorf("%w: %s", ErrUnreachable, msg)
	}
	ok := code >= 200 && code <= 299
	var flag struct {
		OK *bool `json:"ok"`
	}
	if json.Unmarshal(body, &flag) == nil && flag.OK != nil && !*flag.OK {
		ok = false
	}
	s.logger.Info().Int("status", code).Bool("ok", ok).Msgf("tvupdates: POST %s forwarded to the updater", path)
	return ActionResponse{OK: ok, Result: json.RawMessage(body)}, nil
}

// do sends one request. The token goes only in TokenHeader, never in the URL,
// so Go's URL-bearing errors can't contain it.
func (s *Service) do(ctx context.Context, method, target, token string) (int, []byte, error) {
	var body io.Reader
	if method == http.MethodPost {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	res, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return 0, nil, err
	}
	if len(b) > maxResponseBytes {
		return 0, nil, errors.New("the updater's answer is too large")
	}
	return res.StatusCode, b, nil
}

func isJSONObject(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && b[0] == '{' && json.Valid(b)
}

// shortError turns a transport error into a short message for clients,
// without the URL.
func shortError(err error, timeout time.Duration) string {
	var netErr net.Error
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return fmt.Sprintf("the updater did not answer within %s", timeout)
	case errors.As(err, &dnsErr):
		return "the updater's host name was not found"
	case errors.As(err, &opErr) && opErr.Op == "dial":
		return "the updater refused the connection or is down"
	case errors.Is(err, context.Canceled):
		return "the request was cancelled"
	}
	var uErr *url.Error
	if errors.As(err, &uErr) {
		// url.Error repeats the URL; keep only the cause.
		return "could not reach the updater: " + uErr.Err.Error()
	}
	return err.Error()
}
