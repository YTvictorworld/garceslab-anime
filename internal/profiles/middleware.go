package profiles

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

const (
	// HeaderName carries the profile token (web, Denshi, Tenji).
	HeaderName = "X-Seanime-Profile"
	// CookieName is the HttpOnly cookie set by the select endpoint (same-origin web).
	CookieName = "Seanime-Profile"
	// QueryParam carries the token on the websocket URL (/events), like id/proof.
	QueryParam = "profile"
)

// MiddlewareConfig wires the middleware to the server's client identity.
type MiddlewareConfig struct {
	// ClientID returns the verified client id of the request (required).
	ClientID func(c echo.Context) string
	// Platform returns the client platform (web, denshi, mobile), optional.
	Platform func(c echo.Context) string
	// Reject writes the error response when a profile is required. Defaults to a
	// JSON {"error": "PROFILE_REQUIRED"} with status 401.
	Reject func(c echo.Context, err error) error
	// RejectBusy writes the 409 PLAYBACK_BUSY response. Defaults to a JSON error.
	RejectBusy func(c echo.Context, err error) error
	// OnProfile is called for every request with a valid profile (connection tagging).
	OnProfile func(clientID string, profileID int64)
}

// TokenFromRequest returns the profile token from the header, the cookie or (only
// for /events) the query string.
func TokenFromRequest(req *http.Request) string {
	if req == nil {
		return ""
	}
	if t := strings.TrimSpace(req.Header.Get(HeaderName)); t != "" {
		return t
	}
	if ck, err := req.Cookie(CookieName); err == nil && strings.TrimSpace(ck.Value) != "" {
		return strings.TrimSpace(ck.Value)
	}
	if req.URL != nil && req.URL.Path == "/events" {
		return strings.TrimSpace(req.URL.Query().Get(QueryParam))
	}
	return ""
}

// Middleware resolves the profile of each request and stores it in the request
// context (FromContext) and in echo.Context (EchoContextKey). It must run after the
// server password check. When enforcement is on, routes that are not public get
// 401 PROFILE_REQUIRED without a valid profile token.
func (s *Service) Middleware(cfg MiddlewareConfig) echo.MiddlewareFunc {
	reject := cfg.Reject
	if reject == nil {
		reject = func(c echo.Context, err error) error {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": err.Error()})
		}
	}
	rejectBusy := cfg.RejectBusy
	if rejectBusy == nil {
		rejectBusy = func(c echo.Context, err error) error {
			return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		}
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			req := c.Request()

			if token := TokenFromRequest(req); token != "" && cfg.ClientID != nil {
				clientID := cfg.ClientID(c)
				p, err := s.Resolve(req.Context(), token, clientID)
				if err == nil {
					c.SetRequest(req.WithContext(WithProfile(req.Context(), p)))
					c.Set(EchoContextKey, p)
					platform := ""
					if cfg.Platform != nil {
						platform = cfg.Platform(c)
					}
					s.TouchDevice(req.Context(), clientID, p.ID, platform)
					if cfg.OnProfile != nil {
						cfg.OnProfile(clientID, p.ID)
					}
					if err := s.acquireLaneIfPlayback(c, p); err != nil {
						return rejectBusy(c, err)
					}
					return next(c)
				}
			}

			if s.Enforced() && !IsPublicRoute(req.Method, routePath(c)) && !IsPublicRoute(req.Method, req.URL.Path) {
				return reject(c, ErrProfileRequired)
			}
			if err := s.acquireLaneIfPlayback(c, nil); err != nil {
				return rejectBusy(c, err)
			}
			return next(c)
		}
	}
}

// routePath returns the matched route template (e.g. /api/v1/profiles/:id/select),
// or the raw path if routing hasn't happened.
func routePath(c echo.Context) string {
	if p := c.Path(); p != "" {
		return p
	}
	return c.Request().URL.Path
}

// FromEcho returns the profile stored by the middleware, or nil.
func FromEcho(c echo.Context) *Profile {
	if c == nil {
		return nil
	}
	if p, ok := c.Get(EchoContextKey).(*Profile); ok {
		return p
	}
	return FromContext(c.Request().Context())
}

// acquireLaneIfPlayback takes the playback lane for p on the routes that start a
// server-side playback (phase 1: one playback at a time, 409 PLAYBACK_BUSY).
func (s *Service) acquireLaneIfPlayback(c echo.Context, p *Profile) error {
	if s.lane == nil || !IsPlaybackStartRoute(c.Request().Method, routePath(c)) {
		return nil
	}
	return s.lane.Acquire(p)
}
