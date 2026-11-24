package profiles

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlaybackLane(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := &PlaybackLane{now: func() time.Time { return now }}
	a, b := &Profile{ID: 1}, &Profile{ID: 2}
	active := false
	l.SetActiveFunc(func() bool { return active })
	var acquired []int64
	l.SetOnAcquire(func(p *Profile) {
		if p == nil {
			acquired = append(acquired, 0)
		} else {
			acquired = append(acquired, p.ID)
		}
	})

	require.NoError(t, l.Acquire(a))
	assert.Equal(t, int64(1), l.OwnerID())
	assert.ErrorIs(t, l.Acquire(b), ErrPlaybackBusy, "grace period while the stream is prepared")
	assert.ErrorIs(t, l.Acquire(nil), ErrPlaybackBusy)
	require.NoError(t, l.Acquire(a), "same profile: replaces its own playback")

	now = now.Add(2 * laneGrace)
	active = true
	assert.ErrorIs(t, l.Acquire(b), ErrPlaybackBusy, "A is playing")
	active = false
	require.NoError(t, l.Acquire(b), "A finished: B takes the lane")
	assert.Equal(t, int64(2), l.OwnerID())
	assert.Equal(t, []int64{1, 1, 2}, acquired)

	// The owner stays after the playback (late progress updates still go to it).
	ctx := l.Context(context.Background())
	assert.Equal(t, int64(2), IDFromContext(ctx))
	// A request context keeps its own profile.
	assert.Equal(t, int64(1), IDFromContext(l.Context(WithProfile(context.Background(), a))))

	l.ForgetProfile(2)
	assert.Zero(t, l.OwnerID())
	require.NoError(t, l.Acquire(a))
}

func TestMiddlewareTakesTheLane(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	pa, err := s.Create(ctx, CreateInput{Name: "Ana", AvatarID: avatar})
	require.NoError(t, err)
	pb, err := s.Create(ctx, CreateInput{Name: "Bruno", AvatarID: avatar})
	require.NoError(t, err)
	sa, err := s.Select(ctx, pa.ID, "", "tv", "mobile")
	require.NoError(t, err)
	sb, err := s.Select(ctx, pb.ID, "", "web", "web")
	require.NoError(t, err)

	tagged := map[string]int64{}
	e := echo.New()
	mw := s.Middleware(MiddlewareConfig{
		ClientID:  func(c echo.Context) string { return c.Request().Header.Get("X-Test-Client") },
		OnProfile: func(clientID string, profileID int64) { tagged[clientID] = profileID },
	})
	ok := func(c echo.Context) error { return c.String(200, "ok") }
	v1 := e.Group("/api/v1", mw)
	v1.POST("/torrentstream/start", ok)
	v1.GET("/anilist/collection", ok)

	as := func(token string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set(HeaderName, token) }
	}
	assert.Equal(t, 200, do(e, "POST", "/api/v1/torrentstream/start", "tv", as(sa.ProfileToken)).Code)
	assert.Equal(t, pa.ID, s.Lane().OwnerID())
	rec := do(e, "POST", "/api/v1/torrentstream/start", "web", as(sb.ProfileToken))
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "PLAYBACK_BUSY")
	assert.Equal(t, pa.ID, s.Lane().OwnerID())
	// Other routes aren't affected.
	assert.Equal(t, 200, do(e, "GET", "/api/v1/anilist/collection", "web", as(sb.ProfileToken)).Code)
	assert.Equal(t, map[string]int64{"tv": pa.ID, "web": pb.ID}, tagged)
	// A client without profile can't take over either.
	assert.Equal(t, http.StatusConflict, do(e, "POST", "/api/v1/torrentstream/start", "anon", nil).Code)
	// Phase 2: debrid starts and manual tracking take a slot in their handlers
	// (direct = viewer session, relayed = this lane), not in the middleware.
	v1.POST("/debrid/stream/start", ok)
	v1.POST("/playback-manager/manual-tracking/start", ok)
	assert.Equal(t, 200, do(e, "POST", "/api/v1/debrid/stream/start", "web", as(sb.ProfileToken)).Code)
	assert.Equal(t, 200, do(e, "POST", "/api/v1/playback-manager/manual-tracking/start", "web", as(sb.ProfileToken)).Code)
	assert.Equal(t, pa.ID, s.Lane().OwnerID())
}

func TestLaneCapacityAndActiveOwner(t *testing.T) {
	now := time.Date(2026, 11, 24, 20, 0, 0, 0, time.UTC)
	l := &PlaybackLane{now: func() time.Time { return now }}
	_, busy := l.ActiveOwner()
	assert.False(t, busy)

	full := errors.New("LANES_FULL: test")
	var askedFor string
	l.SetCapacityFunc(func(p *Profile, clientID string) error {
		askedFor = clientID
		if p != nil && p.ID == 3 {
			return full
		}
		return nil
	})
	assert.ErrorIs(t, l.AcquireFor(&Profile{ID: 3, Name: "Carl"}, "web"), full)
	assert.Equal(t, "web", askedFor)
	assert.Equal(t, int64(0), l.OwnerID(), "a full server gives the lane to nobody")

	assert.NoError(t, l.AcquireFor(&Profile{ID: 1, Name: "Ana"}, "tv"))
	p, busy := l.ActiveOwner()
	assert.True(t, busy)
	assert.Equal(t, int64(1), p.ID)

	now = now.Add(laneGrace + time.Second)
	_, busy = l.ActiveOwner()
	assert.False(t, busy, "after the grace, only an active media core session keeps it")
	l.SetActiveFunc(func() bool { return true })
	_, busy = l.ActiveOwner()
	assert.True(t, busy)
}
