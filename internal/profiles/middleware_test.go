package profiles

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestEcho mounts the middleware with the client id taken from a test header.
func newTestEcho(s *Service) *echo.Echo {
	e := echo.New()
	mw := s.Middleware(MiddlewareConfig{
		ClientID: func(c echo.Context) string { return c.Request().Header.Get("X-Test-Client") },
	})
	handler := func(c echo.Context) error {
		id := IDFromContext(c.Request().Context())
		if p := FromEcho(c); p != nil && p.ID != id {
			return c.String(500, "mismatch")
		}
		return c.JSON(200, map[string]int64{"profileId": id})
	}
	v1 := e.Group("/api/v1", mw)
	v1.GET("/status", handler)
	v1.GET("/profiles", handler)
	v1.POST("/profiles/:id/select", handler)
	v1.GET("/anilist/collection", handler)
	v1.GET("/directstream/stream", handler)
	v1.HEAD("/directstream/stream", handler)
	e.GET("/events", handler, mw)
	return e
}

func do(e *echo.Echo, method, target, client string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if client != "" {
		req.Header.Set("X-Test-Client", client)
	}
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestMiddleware(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	p, err := s.Create(ctx, CreateInput{Name: "Ana", AvatarID: avatar})
	require.NoError(t, err)
	sess, err := s.Select(ctx, p.ID, "", "c1", "web")
	require.NoError(t, err)
	e := newTestEcho(s)

	withHeader := func(r *http.Request) { r.Header.Set(HeaderName, sess.ProfileToken) }
	withCookie := func(r *http.Request) { r.AddCookie(&http.Cookie{Name: CookieName, Value: sess.ProfileToken}) }
	expectProfile := `{"profileId":` + strconv.FormatInt(p.ID, 10) + `}`

	for _, enforce := range []bool{false, true} {
		s.SetEnforced(enforce)
		name := map[bool]string{false: "enforce off", true: "enforce on"}[enforce]
		t.Run(name, func(t *testing.T) {
			// Header and cookie resolve the profile.
			rec := do(e, "GET", "/api/v1/anilist/collection", "c1", withHeader)
			assert.Equal(t, 200, rec.Code)
			assert.JSONEq(t, expectProfile, rec.Body.String())
			rec = do(e, "GET", "/api/v1/anilist/collection", "c1", withCookie)
			assert.Equal(t, 200, rec.Code)
			assert.JSONEq(t, expectProfile, rec.Body.String())

			// ?profile= only on /events.
			rec = do(e, "GET", "/events?profile="+sess.ProfileToken, "c1", nil)
			assert.JSONEq(t, expectProfile, rec.Body.String())
			rec = do(e, "GET", "/api/v1/anilist/collection?profile="+sess.ProfileToken, "c1", nil)
			if enforce {
				assert.Equal(t, 401, rec.Code)
			} else {
				assert.JSONEq(t, `{"profileId":0}`, rec.Body.String())
			}

			// A token from another client is ignored.
			rec = do(e, "GET", "/api/v1/anilist/collection", "c2", withHeader)
			if enforce {
				assert.Equal(t, 401, rec.Code)
				assert.JSONEq(t, `{"error":"PROFILE_REQUIRED"}`, rec.Body.String())
			} else {
				assert.Equal(t, 200, rec.Code)
				assert.JSONEq(t, `{"profileId":0}`, rec.Body.String())
			}

			// No token: user-data routes depend on the flag, public routes always pass.
			rec = do(e, "GET", "/api/v1/anilist/collection", "c1", nil)
			assert.Equal(t, map[bool]int{false: 200, true: 401}[enforce], rec.Code)
			for _, target := range []struct{ method, path string }{
				{"GET", "/api/v1/status"},
				{"GET", "/api/v1/profiles"},
				{"POST", "/api/v1/profiles/1/select"},
				{"GET", "/api/v1/directstream/stream"},
				{"HEAD", "/api/v1/directstream/stream"},
				{"GET", "/events"},
			} {
				rec = do(e, target.method, target.path, "c1", nil)
				assert.Equal(t, 200, rec.Code, target)
			}
		})
	}
}

func TestIsPublicRoute(t *testing.T) {
	assert.True(t, IsPublicRoute("GET", "/api/v1/status"))
	assert.True(t, IsPublicRoute("POST", "/api/v1/profiles"))
	assert.False(t, IsPublicRoute("PATCH", "/api/v1/profiles/:id"))
	assert.False(t, IsPublicRoute("DELETE", "/api/v1/profiles/:id"))
	assert.False(t, IsPublicRoute("PUT", "/api/v1/profiles/:id/pin"))
	assert.True(t, IsPublicRoute("GET", "/api/v1/mediastream/transcode/*"))
	assert.True(t, IsPublicRoute("GET", "/api/v1/anilist/media-details/:id"))
	assert.False(t, IsPublicRoute("POST", "/api/v1/anilist/list-entry"))
	assert.True(t, IsPublicRoute("GET", "/api/v1/search/suggest"))
	assert.True(t, IsPublicRoute("GET", "/api/v1/anime/episode-tags/:id"))
	assert.False(t, IsPublicRoute("GET", "/api/v1/continuity/history"))
	assert.False(t, IsPublicRoute("GET", "/api/v1/settings"))
}

func TestTokenFromRequestPrecedence(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/events?profile=query", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "cookie"})
	assert.Equal(t, "query", TokenFromRequest(req), "on /events the tab's ?profile= wins over the shared cookie")

	req.Header.Set(HeaderName, "header")
	assert.Equal(t, "header", TokenFromRequest(req))

	req = httptest.NewRequest(http.MethodGet, "/events", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "cookie"})
	assert.Equal(t, "cookie", TokenFromRequest(req))

	req = httptest.NewRequest(http.MethodGet, "/api/v1/status?profile=query", nil)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "cookie"})
	assert.Equal(t, "cookie", TokenFromRequest(req), "?profile= is only read on /events")
}
