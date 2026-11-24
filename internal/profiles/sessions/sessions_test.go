package sessions

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type sentEvent struct {
	client  string
	kind    string
	payload interface{}
}

type fakeEvents struct {
	mu   sync.Mutex
	sent []sentEvent
}

func (f *fakeEvents) SendEventTo(clientId string, t string, payload interface{}, _ ...bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentEvent{clientId, t, payload})
}

func (f *fakeEvents) of(client string) []sentEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ret []sentEvent
	for _, e := range f.sent {
		if e.client == client {
			ret = append(ret, e)
		}
	}
	return ret
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                   { return &clock{t: time.Date(2026, 11, 24, 20, 0, 0, 0, time.UTC)} }
func req(client string, pid int64, name string, media, ep int) AcquireRequest {
	return AcquireRequest{ClientID: client, ProfileID: pid, ProfileName: name, MediaID: media, Episode: ep, Device: "LG TV"}
}

type releaseLog struct {
	mu   sync.Mutex
	list []string
}

func (l *releaseLog) add(s ViewerSession, r Reason) {
	l.mu.Lock()
	l.list = append(l.list, s.ClientID+":"+string(r))
	l.mu.Unlock()
}

func (l *releaseLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.list...)
}

func newTestRegistry(t *testing.T) (*Registry, *clock, *fakeEvents, *releaseLog) {
	t.Helper()
	c := newClock()
	ev := &fakeEvents{}
	rl := &releaseLog{}
	r := New(Options{Max: 3, DisconnectGrace: 45 * time.Second, IdleTimeout: 15 * time.Minute, Events: ev, Now: c.now, OnRelease: rl.add})
	return r, c, ev, rl
}

func TestThreeViewersThenLanesFull(t *testing.T) {
	r, _, _, _ := newTestRegistry(t)
	for i, name := range []string{"Ana", "Bob", "Carl"} {
		_, err := r.Acquire(req("tv-"+name, int64(i+1), name, 100+i, 1))
		require.NoError(t, err)
	}
	require.Equal(t, 3, r.Count())

	_, err := r.Acquire(req("tv-Dana", 4, "Dana", 200, 1))
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLanesFull))
	var full *FullError
	require.True(t, errors.As(err, &full))
	require.Equal(t, 3, full.Max)
	require.Equal(t, []string{"Ana", "Bob", "Carl"}, full.Viewers)
	require.Equal(t, "LANES_FULL: All 3 viewing slots are in use (Ana, Bob and Carl)", err.Error())
	require.True(t, strings.HasPrefix(err.Error(), "LANES_FULL"))
	require.Equal(t, 3, r.Count(), "the failed acquire changes nothing")
}

func TestConfigurableMaxFromEnv(t *testing.T) {
	t.Setenv(EnvMaxViewers, "2")
	t.Setenv(EnvIdleTimeout, "5m")
	t.Setenv(EnvDisconnectGrace, "10s")
	o := OptionsFromEnv()
	require.Equal(t, 2, o.Max)
	require.Equal(t, 5*time.Minute, o.IdleTimeout)
	require.Equal(t, 10*time.Second, o.DisconnectGrace)

	r := New(o)
	_, err := r.Acquire(req("a", 1, "A", 1, 1))
	require.NoError(t, err)
	_, err = r.Acquire(req("b", 2, "B", 1, 1))
	require.NoError(t, err)
	_, err = r.Acquire(req("c", 3, "C", 1, 1))
	require.ErrorIs(t, err, ErrLanesFull)

	t.Setenv(EnvMaxViewers, "nonsense")
	require.Equal(t, DefaultMax, New(OptionsFromEnv()).Max())
}

func TestSameClientReplacesItsSessionWithoutUsingASlot(t *testing.T) {
	r, _, ev, rl := newTestRegistry(t)
	first, err := r.Acquire(req("tv1", 1, "Ana", 100, 1))
	require.NoError(t, err)
	_, _ = r.Acquire(req("tv2", 2, "Bob", 100, 1))
	_, _ = r.Acquire(req("tv3", 3, "Carl", 100, 1))

	// Next episode on tv1 while full: replaces tv1's own session.
	second, err := r.Acquire(req("tv1", 1, "Ana", 100, 2))
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, 3, r.Count())
	got, _ := r.Get("tv1")
	require.Equal(t, 2, got.Episode)
	require.Empty(t, rl.all(), "a new episode is not a release (the new stream replaces the old one)")
	require.Empty(t, ev.of("tv1"))
}

func TestSameProfileOnAnotherDeviceReplacesTheOlderSession(t *testing.T) {
	r, _, ev, rl := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 100, 1))
	_, _ = r.Acquire(req("tv2", 2, "Bob", 100, 1))
	_, _ = r.Acquire(req("tv3", 3, "Carl", 100, 1))

	_, err := r.Acquire(req("phone", 1, "Ana", 300, 4))
	require.NoError(t, err)
	_, ok := r.Get("tv1")
	require.False(t, ok)
	require.Equal(t, []string{"tv1:replaced"}, rl.all())
	evs := ev.of("tv1")
	require.Len(t, evs, 1)
	require.Equal(t, EventSessionEnded, evs[0].kind)
	require.Equal(t, ReasonReplaced, evs[0].payload.(SessionEndedPayload).Reason)
	require.Empty(t, ev.of("tv2"), "other sessions hear nothing")
}

func TestClientsWithoutProfileAreSeparate(t *testing.T) {
	r, _, _, _ := newTestRegistry(t)
	_, err := r.Acquire(req("a", 0, "", 1, 1))
	require.NoError(t, err)
	_, err = r.Acquire(req("b", 0, "", 1, 1))
	require.NoError(t, err)
	require.Equal(t, 2, r.Count())
	_, _ = r.Acquire(req("c", 0, "", 1, 1))
	_, err = r.Acquire(req("d", 0, "", 1, 1))
	var full *FullError
	require.ErrorAs(t, err, &full)
	require.Equal(t, []string{"Guest", "Guest", "Guest"}, full.Viewers)
}

func TestNoClientID(t *testing.T) {
	r, _, _, _ := newTestRegistry(t)
	_, err := r.Acquire(req(" ", 1, "Ana", 1, 1))
	require.ErrorIs(t, err, ErrNoClientID)
}

func TestReleaseOnStopFreesTheSlotAndOnlyMatchingEpisode(t *testing.T) {
	r, _, ev, rl := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 100, 2))

	// A late stop of episode 1 keeps the session of episode 2.
	require.False(t, r.Release("tv1", 100, 1, ReasonStopped))
	require.Equal(t, 1, r.Count())

	require.True(t, r.Release("tv1", 100, 2, ReasonStopped))
	require.Equal(t, 0, r.Count())
	require.Equal(t, []string{"tv1:stopped"}, rl.all())
	require.Empty(t, ev.of("tv1"), "the client stopped it itself: no ended event")

	require.False(t, r.Release("tv1", 0, 0, ReasonStopped), "nothing left")
}

func TestDisconnectGrace(t *testing.T) {
	r, c, ev, rl := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 100, 1))
	_, _ = r.Acquire(req("tv2", 2, "Bob", 100, 1))

	// Reconnects within the grace: keeps the slot.
	r.ClientDisconnected("tv1")
	s, _ := r.Get("tv1")
	require.True(t, s.Disconnected)
	c.advance(30 * time.Second)
	r.Sweep()
	require.Equal(t, 2, r.Count())
	r.ClientConnected("tv1")
	c.advance(30 * time.Second)
	r.Sweep()
	require.Equal(t, 2, r.Count())

	// Stays away for the grace: freed.
	r.ClientDisconnected("tv2")
	c.advance(44 * time.Second)
	r.Sweep()
	require.Equal(t, 2, r.Count())
	c.advance(2 * time.Second)
	r.Sweep()
	require.Equal(t, 1, r.Count())
	_, ok := r.Get("tv2")
	require.False(t, ok)
	require.Equal(t, []string{"tv2:disconnected"}, rl.all())
	require.Len(t, ev.of("tv2"), 1)
	require.Empty(t, ev.of("tv1"))
}

func TestDisconnectedSlotIsFreedForANewViewerAfterGrace(t *testing.T) {
	r, c, _, _ := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 1, 1))
	_, _ = r.Acquire(req("tv2", 2, "Bob", 1, 1))
	_, _ = r.Acquire(req("tv3", 3, "Carl", 1, 1))
	r.ClientDisconnected("tv3")
	_, err := r.Acquire(req("tv4", 4, "Dana", 1, 1))
	require.ErrorIs(t, err, ErrLanesFull, "still within the grace")
	c.advance(DefaultDisconnectGrace)
	_, err = r.Acquire(req("tv4", 4, "Dana", 1, 1))
	require.NoError(t, err, "acquire expires the stale session itself")
}

func TestIdleTimeoutAndTouch(t *testing.T) {
	r, c, _, rl := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 1, 1))
	_, _ = r.Acquire(req("tv2", 2, "Bob", 1, 1))

	for i := 0; i < 5; i++ {
		c.advance(10 * time.Minute)
		r.Touch("tv1") // continuity updates every 15 s while playing
		r.Sweep()
	}
	_, ok := r.Get("tv1")
	require.True(t, ok)
	_, ok = r.Get("tv2")
	require.False(t, ok)
	require.Equal(t, []string{"tv2:idle"}, rl.all())
}

func TestAdminStopByID(t *testing.T) {
	r, _, ev, rl := newTestRegistry(t)
	s, _ := r.Acquire(req("tv1", 1, "Ana", 1, 1))
	_, err := r.ReleaseByID("nope", ReasonAdmin)
	require.ErrorIs(t, err, ErrNotFound)
	got, err := r.ReleaseByID(s.ID, ReasonAdmin)
	require.NoError(t, err)
	require.Equal(t, "tv1", got.ClientID)
	require.Equal(t, []string{"tv1:admin"}, rl.all())
	require.Equal(t, EventSessionEnded, ev.of("tv1")[0].kind)
}

func TestForgetProfile(t *testing.T) {
	r, _, _, rl := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 1, 1))
	_, _ = r.Acquire(req("tv2", 2, "Bob", 1, 1))
	r.ForgetProfile(2)
	require.Equal(t, 1, r.Count())
	require.Equal(t, []string{"tv2:profile-gone"}, rl.all())
}

func TestTrackingIsPerSessionAndEventsOnlyReachTheOwner(t *testing.T) {
	r, _, ev, _ := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 100, 1))
	_, _ = r.Acquire(req("tv2", 2, "Bob", 200, 7))

	_, ok := r.StartTracking("tv1", ViewerTracking{MediaID: 100, EpisodeNumber: 1, MediaTitle: "Show A", TotalEpisodes: 12})
	require.True(t, ok)
	_, ok = r.StartTracking("tv2", ViewerTracking{MediaID: 200, EpisodeNumber: 7, MediaTitle: "Show B", TotalEpisodes: 24})
	require.True(t, ok)
	_, ok = r.StartTracking("nobody", ViewerTracking{MediaID: 1})
	require.False(t, ok)

	t1, _ := r.Tracking("tv1")
	t2, _ := r.Tracking("tv2")
	require.Equal(t, 100, t1.MediaID)
	require.Equal(t, 200, t2.MediaID)
	s1, _ := r.Get("tv1")
	require.Equal(t, StatePlaying, s1.State)
	require.Equal(t, "Show A", s1.MediaTitle)

	// tv2 stops: tv1's tracking is untouched (upstream's global tracking would stop both).
	require.True(t, r.Release("tv2", 0, 0, ReasonStopped))
	_, ok = r.Tracking("tv1")
	require.True(t, ok)
	_, ok = r.Tracking("tv2")
	require.False(t, ok)

	for _, e := range ev.of("tv1") {
		require.NotEqual(t, EventManualTrackingStopped, e.kind)
	}
	stateFor := func(client string) map[string]interface{} {
		for _, e := range ev.of(client) {
			if e.kind == EventManualTrackingState {
				return e.payload.(map[string]interface{})
			}
		}
		return nil
	}
	require.Equal(t, 100, stateFor("tv1")["mediaId"])
	require.Equal(t, 200, stateFor("tv2")["mediaId"])
	require.Contains(t, kinds(ev.of("tv2")), EventManualTrackingStopped)
}

func kinds(list []sentEvent) []string {
	ret := make([]string, 0, len(list))
	for _, e := range list {
		ret = append(ret, e.kind)
	}
	return ret
}

func TestRelayedPlaybackTakesASlot(t *testing.T) {
	r, _, _, _ := newTestRegistry(t)
	r.SetRelayedBusy(func() (int64, string, bool) { return 9, "Web", true })
	_, _ = r.Acquire(req("tv1", 1, "Ana", 1, 1))
	_, _ = r.Acquire(req("tv2", 2, "Bob", 1, 1))
	_, err := r.Acquire(req("tv3", 3, "Carl", 1, 1))
	var full *FullError
	require.ErrorAs(t, err, &full)
	require.Equal(t, []string{"Ana", "Bob", "Web"}, full.Viewers)

	// The relayed lane's own profile moving to a direct stream doesn't count twice.
	_, err = r.Acquire(req("tv9", 9, "Web", 1, 1))
	require.NoError(t, err)
}

func TestCapacityForTheRelayedLane(t *testing.T) {
	r, _, _, _ := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 1, 1))
	_, _ = r.Acquire(req("tv2", 2, "Bob", 1, 1))
	require.NoError(t, r.Capacity(4, "web"))
	_, _ = r.Acquire(req("tv3", 3, "Carl", 1, 1))
	require.ErrorIs(t, r.Capacity(4, "web"), ErrLanesFull)
	require.NoError(t, r.Capacity(3, "web"), "Carl's own session doesn't count")
	require.NoError(t, r.Capacity(0, "tv2"), "the same client doesn't count")
}

func TestSetStateAndTitleOnlyForTheCurrentEpisode(t *testing.T) {
	r, _, _, _ := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 100, 2))
	require.False(t, r.SetState("tv1", 100, 1, StateReady))
	require.True(t, r.SetState("tv1", 100, 2, StateReady))
	r.SetTitle("tv1", 100, 2, "Show")
	s, _ := r.Get("tv1")
	require.Equal(t, StateReady, s.State)
	require.Equal(t, "Show", s.MediaTitle)
}

func TestListOrderAndJSONHidesClientID(t *testing.T) {
	r, c, _, _ := newTestRegistry(t)
	_, _ = r.Acquire(req("tv1", 1, "Ana", 1, 1))
	c.advance(time.Second)
	_, _ = r.Acquire(req("tv2", 2, "Bob", 1, 1))
	list := r.List()
	require.Len(t, list, 2)
	require.Equal(t, "Ana", list[0].ProfileName)
	require.Equal(t, "Bob", list[1].ProfileName)
}

func TestConcurrentAcquireNeverExceedsMax(t *testing.T) {
	r := New(Options{Max: 3})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, full := 0, 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := r.Acquire(req("c"+string(rune('a'+i)), int64(i+1), "P", 1, 1))
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if errors.Is(err, ErrLanesFull) {
				full++
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, 3, ok)
	require.Equal(t, 17, full)
	require.Equal(t, 3, r.Count())
}

func TestDeviceLabel(t *testing.T) {
	require.Equal(t, "LG TV", DeviceLabel("web", "Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36"))
	require.Equal(t, "Browser", DeviceLabel("web", "Mozilla/5.0 (X11; Linux x86_64)"))
	require.Equal(t, "Desktop app", DeviceLabel("denshi", ""))
	require.Equal(t, "Unknown device", DeviceLabel("", ""))
}
