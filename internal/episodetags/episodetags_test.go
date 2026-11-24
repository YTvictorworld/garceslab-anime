package episodetags

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock makes the pool's pacing and cooldowns instant and observable.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if d > 0 {
		c.Advance(d)
	}
	return ctx.Err()
}

// fakeAnime is a MAL anime served by the fake providers: episode n is filler
// when fillers[n], recap when recaps[n].
type fakeAnime struct {
	episodes int
	fillers  map[int]bool
	recaps   map[int]bool
}

// bleach mimics Bleach (MAL 269): 366 episodes, 33 and 50 filler, 64-109 filler.
func bleach() fakeAnime {
	f := map[int]bool{33: true, 50: true}
	for n := 64; n <= 109; n++ {
		f[n] = true
	}
	return fakeAnime{episodes: 366, fillers: f, recaps: map[int]bool{110: true}}
}

func allCanon(n int) fakeAnime { return fakeAnime{episodes: n} }

func (a fakeAnime) page(n int) (eps []int, hasNext bool, last int) {
	last = (a.episodes + 99) / 100
	for i := (n-1)*100 + 1; i <= a.episodes && i <= n*100; i++ {
		eps = append(eps, i)
	}
	return eps, n < last, last
}

type fakeProvider struct {
	format   Format
	anime    map[int]fakeAnime
	requests atomic.Int32
	// respond, when set, answers instead of the anime data (nil = serve data).
	respond func(w http.ResponseWriter, r *http.Request) bool
	clock   *fakeClock
	mu      sync.Mutex
	times   []time.Time
}

func (f *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests.Add(1)
	if f.clock != nil {
		f.mu.Lock()
		f.times = append(f.times, f.clock.Now())
		f.mu.Unlock()
	}
	if f.respond != nil && f.respond(w, r) {
		return
	}
	var malID int
	if _, err := fmt.Sscanf(r.URL.Path, "/anime/%d/episodes", &malID); err != nil {
		http.NotFound(w, r)
		return
	}
	a, ok := f.anime[malID]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404,"message":"Resource does not exist"}`))
		return
	}
	pageNum, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pageNum < 1 {
		pageNum = 1
	}
	eps, hasNext, last := a.page(pageNum)
	w.Header().Set("Content-Type", "application/json")
	switch f.format {
	case FormatJikanEdge:
		data := make([]map[string]any, 0, len(eps))
		for _, n := range eps {
			data = append(data, map[string]any{"number": n, "title": fmt.Sprintf("Ep %d", n), "filler": a.fillers[n], "recap": a.recaps[n]})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": data,
			"meta": map[string]any{"pagination": map[string]any{"page": pageNum, "limit": 100, "count": len(data), "total": nil, "hasNextPage": hasNext}},
		})
	default:
		data := make([]map[string]any, 0, len(eps))
		for _, n := range eps {
			data = append(data, map[string]any{"mal_id": n, "title": fmt.Sprintf("Ep %d", n), "filler": a.fillers[n], "recap": a.recaps[n]})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pagination": map[string]any{"last_visible_page": last, "has_next_page": hasNext},
			"data":       data,
		})
	}
}

func newFake(t *testing.T, format Format, clock *fakeClock, anime map[int]fakeAnime) (*fakeProvider, Provider) {
	fp := &fakeProvider{format: format, anime: anime, clock: clock}
	srv := httptest.NewServer(fp)
	t.Cleanup(srv.Close)
	return fp, Provider{Name: "fake-" + string(format) + "-" + srv.URL[len(srv.URL)-5:], BaseURL: srv.URL, Format: format, MinInterval: time.Second}
}

func testPool(clock *fakeClock, providers ...Provider) *pool {
	p := newPool(providers, &http.Client{Timeout: 5 * time.Second}, nil)
	p.now = clock.Now
	p.sleep = clock.Sleep
	return p
}

func TestFetchAllBothShapes(t *testing.T) {
	for _, format := range []Format{FormatJikanV4, FormatJikanEdge} {
		t.Run(string(format), func(t *testing.T) {
			clock := newFakeClock()
			fp, prov := newFake(t, format, clock, map[int]fakeAnime{269: bleach()})
			p := testPool(clock, prov)

			e, err := p.fetchAll(context.Background(), 269)
			require.NoError(t, err)
			assert.Equal(t, int32(4), fp.requests.Load(), "366 episodes = 4 pages")
			assert.Len(t, e.Episodes, 366)
			assert.Equal(t, KindCanon, e.Episodes[1])
			assert.Equal(t, KindFiller, e.Episodes[33])
			assert.Equal(t, KindFiller, e.Episodes[50])
			assert.Equal(t, KindFiller, e.Episodes[64])
			assert.Equal(t, KindFiller, e.Episodes[109])
			assert.Equal(t, KindRecap, e.Episodes[110])
			assert.Equal(t, KindCanon, e.Episodes[366])
			assert.Equal(t, prov.Name, e.Source)
		})
	}
}

func TestPacingAtLeastOneSecondPerProvider(t *testing.T) {
	clock := newFakeClock()
	fp, prov := newFake(t, FormatJikanV4, clock, map[int]fakeAnime{269: bleach()})
	prov.MinInterval = 10 * time.Millisecond // raised to the 1 s floor
	p := testPool(clock, prov)

	_, err := p.fetchAll(context.Background(), 269)
	require.NoError(t, err)
	require.Len(t, fp.times, 4)
	for i := 1; i < len(fp.times); i++ {
		assert.GreaterOrEqual(t, fp.times[i].Sub(fp.times[i-1]), time.Second)
	}
}

func TestRateLimitFailsOverAndHonoursRetryAfter(t *testing.T) {
	clock := newFakeClock()
	limited, provA := newFake(t, FormatJikanV4, clock, map[int]fakeAnime{269: bleach()})
	limited.respond = func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"status":429,"type":"RateLimitException"}`))
		return true
	}
	edge, provB := newFake(t, FormatJikanEdge, clock, map[int]fakeAnime{269: bleach(), 1: allCanon(12)})
	p := testPool(clock, provA, provB)

	e, err := p.fetchAll(context.Background(), 269)
	require.NoError(t, err)
	assert.Equal(t, provB.Name, e.Source)
	assert.Len(t, e.Episodes, 366)
	assert.Equal(t, int32(1), limited.requests.Load(), "a rate-limited provider rests")
	assert.Equal(t, int32(4), edge.requests.Load())

	st := p.status()
	assert.Equal(t, 1, st[0].Failures)
	assert.False(t, st[0].CooldownUntil.Before(clock.Now().Add(590*time.Second)), "cooldown covers Retry-After")

	// Still resting: the next anime goes straight to B.
	_, err = p.fetchAll(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, int32(1), limited.requests.Load())

	// Retry-After is over: A is first in order again and gets the request.
	clock.Advance(11 * time.Minute)
	limited.respond = nil
	limited.anime[1] = allCanon(12)
	e, err = p.fetchAll(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, provA.Name, e.Source)
	assert.Equal(t, 0, p.status()[0].Failures)
}

func TestEdgeRateLimitBody(t *testing.T) {
	clock := newFakeClock()
	fp, prov := newFake(t, FormatJikanEdge, clock, nil)
	fp.respond = func(w http.ResponseWriter, r *http.Request) bool {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"slow down","requestId":"x"}}`))
		return true
	}
	_, err := fetchPage(context.Background(), http.DefaultClient, prov, 1, 1)
	var pe *providerError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, errRateLimited, pe.Kind)
	assert.Equal(t, 7*time.Second, pe.RetryAfter)
}

func TestServerErrorAndUnreachableFailOver(t *testing.T) {
	clock := newFakeClock()
	broken, provA := newFake(t, FormatJikanV4, clock, nil)
	broken.respond = func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusBadGateway)
		return true
	}
	// Nothing listens there: the connection is refused.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	provB := Provider{Name: "dead", BaseURL: deadURL, Format: FormatJikanV4, MinInterval: time.Second}
	_, provC := newFake(t, FormatJikanEdge, clock, map[int]fakeAnime{5: allCanon(24)})
	p := testPool(clock, provA, provB, provC)

	e, err := p.fetchAll(context.Background(), 5)
	require.NoError(t, err)
	assert.Equal(t, provC.Name, e.Source)
	st := p.status()
	assert.Equal(t, 1, st[0].Failures)
	assert.Equal(t, 1, st[1].Failures)
	assert.Equal(t, clock.Now().Add(defaultBaseCooldown).Unix(), st[0].CooldownUntil.Unix(), "first failure rests the base cooldown")
}

func TestCooldownIsExponentialAndCapped(t *testing.T) {
	p := newPool(nil, http.DefaultClient, nil)
	assert.Equal(t, 30*time.Second, p.cooldownFor(1, 0))
	assert.Equal(t, 60*time.Second, p.cooldownFor(2, 0))
	assert.Equal(t, 120*time.Second, p.cooldownFor(3, 0))
	assert.Equal(t, 30*time.Minute, p.cooldownFor(20, 0))
	assert.Equal(t, 45*time.Minute, p.cooldownFor(1, 45*time.Minute), "Retry-After wins over the cap")
}

func TestAllProvidersDown(t *testing.T) {
	clock := newFakeClock()
	a, provA := newFake(t, FormatJikanV4, clock, nil)
	b, provB := newFake(t, FormatJikanEdge, clock, nil)
	fail := func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusServiceUnavailable)
		return true
	}
	a.respond, b.respond = fail, fail
	p := testPool(clock, provA, provB)

	_, err := p.fetchAll(context.Background(), 269)
	require.ErrorIs(t, err, errAllProvidersDown)
	// Each provider is tried a bounded number of times per page.
	assert.LessOrEqual(t, a.requests.Load()+b.requests.Load(), int32(4))
}

func TestNotFound(t *testing.T) {
	clock := newFakeClock()
	_, prov := newFake(t, FormatJikanV4, clock, map[int]fakeAnime{})
	p := testPool(clock, prov)
	e, err := p.fetchAll(context.Background(), 999999)
	require.NoError(t, err)
	assert.True(t, e.NotFound)
	assert.Equal(t, StatusUnavailable, e.response().Status)
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, 30*time.Second, parseRetryAfter("30", now))
	assert.Equal(t, time.Duration(0), parseRetryAfter("", now))
	assert.Equal(t, time.Duration(0), parseRetryAfter("soon", now))
	assert.Equal(t, 90*time.Second, parseRetryAfter(now.Add(90*time.Second).Format(http.TimeFormat), now))
}

func TestParseProviders(t *testing.T) {
	ps, skipped := ParseProviders(" jikan , tenrai,nope, jikan4=https://mirror.example/v4/ ,edge=ftp://x, tenrai")
	require.Len(t, ps, 3)
	assert.Equal(t, "jikan", ps[0].Name)
	assert.Equal(t, "tenrai", ps[1].Name)
	assert.Equal(t, "https://mirror.example/v4", ps[2].BaseURL)
	assert.Equal(t, FormatJikanV4, ps[2].Format)
	assert.Equal(t, []string{"nope", "edge=ftp://x"}, skipped)

	ps, _ = ParseProviders("")
	assert.Empty(t, ps)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func testManager(t *testing.T, dir string, clock *fakeClock, providers ...Provider) *Manager {
	m := NewManager(Options{Dir: dir, Providers: providers, HTTPClient: &http.Client{Timeout: 5 * time.Second}})
	m.now = clock.Now
	m.pool.now = clock.Now
	m.pool.sleep = clock.Sleep
	m.Start()
	t.Cleanup(m.Stop)
	return m
}

func TestManagerPendingThenReadyAndDiskCache(t *testing.T) {
	clock := newFakeClock()
	dir := t.TempDir()
	fp, prov := newFake(t, FormatJikanV4, clock, map[int]fakeAnime{269: bleach(), 2: allCanon(12)})
	m := testManager(t, dir, clock, prov)

	r := m.Get(269, false)
	assert.Equal(t, StatusPending, r.Status)
	assert.Equal(t, 269, r.MalID)
	assert.NotNil(t, r.Episodes)
	waitFor(t, func() bool { return m.Get(269, false).Status == StatusReady })

	r = m.Get(269, false)
	assert.True(t, r.HasNonCanon)
	assert.Equal(t, KindFiller, r.Episodes["33"])
	assert.Equal(t, KindCanon, r.Episodes["1"])
	assert.Equal(t, prov.Name, r.Source)
	_, err := os.Stat(filepath.Join(dir, "269.json"))
	require.NoError(t, err)

	// All canon: ready, but nothing for the clients to show.
	m.Get(2, false)
	waitFor(t, func() bool { return m.Get(2, false).Status == StatusReady })
	assert.False(t, m.Get(2, false).HasNonCanon)

	// A new manager reads the disk cache without asking the provider.
	before := fp.requests.Load()
	m2 := testManager(t, dir, clock, prov)
	r = m2.Get(269, false)
	assert.Equal(t, StatusReady, r.Status)
	assert.Equal(t, KindFiller, r.Episodes["64"])
	assert.Equal(t, before, fp.requests.Load())
}

func TestManagerRefreshIntervals(t *testing.T) {
	clock := newFakeClock()
	fp, prov := newFake(t, FormatJikanV4, clock, map[int]fakeAnime{2: allCanon(12)})
	m := testManager(t, t.TempDir(), clock, prov)

	m.Get(2, true)
	waitFor(t, func() bool { return m.Get(2, true).Status == StatusReady })
	assert.Equal(t, int32(1), fp.requests.Load())

	// Airing: fresh for a day, then served stale while it refreshes.
	clock.Advance(23 * time.Hour)
	assert.Equal(t, StatusReady, m.Get(2, true).Status)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(1), fp.requests.Load())

	clock.Advance(2 * time.Hour)
	fp.anime[2] = fakeAnime{episodes: 13, recaps: map[int]bool{13: true}}
	assert.Equal(t, StatusReady, m.Get(2, true).Status, "stale data is served while refreshing")
	waitFor(t, func() bool { return m.Get(2, true).HasNonCanon })
	assert.Equal(t, int32(2), fp.requests.Load())

	// Finished: the same entry is fresh for 30 days.
	clock.Advance(10 * 24 * time.Hour)
	m.Get(2, false)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(2), fp.requests.Load())
	clock.Advance(21 * 24 * time.Hour)
	m.Get(2, false)
	waitFor(t, func() bool { return fp.requests.Load() == 3 })
}

func TestManagerFailureIsUnavailableThenRetried(t *testing.T) {
	clock := newFakeClock()
	fp, prov := newFake(t, FormatJikanV4, clock, map[int]fakeAnime{7: allCanon(3)})
	down := true
	var mu sync.Mutex
	fp.respond = func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		defer mu.Unlock()
		if down {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return down
	}
	m := testManager(t, t.TempDir(), clock, prov)

	assert.Equal(t, StatusPending, m.Get(7, false).Status)
	waitFor(t, func() bool { return m.Get(7, false).Status == StatusUnavailable })
	n := fp.requests.Load()
	assert.Equal(t, StatusUnavailable, m.Get(7, false).Status, "no new fetch right after a failure")
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, n, fp.requests.Load())

	mu.Lock()
	down = false
	mu.Unlock()
	clock.Advance(DefaultRetryAfterFailure + pastAnyCooldown)
	assert.Equal(t, StatusPending, m.Get(7, false).Status)
	waitFor(t, func() bool { return m.Get(7, false).Status == StatusReady })
}

// pastAnyCooldown moves the clock past any provider cooldown.
const pastAnyCooldown = defaultMaxCooldown

func TestManagerOfflineAndNil(t *testing.T) {
	var nilManager *Manager
	assert.Equal(t, StatusUnavailable, nilManager.Get(1, false).Status)

	clock := newFakeClock()
	fp, prov := newFake(t, FormatJikanV4, clock, map[int]fakeAnime{1: allCanon(1)})
	m := NewManager(Options{Dir: t.TempDir(), Providers: []Provider{prov}, IsOffline: func() bool { return true }})
	m.Start()
	defer m.Stop()
	assert.Equal(t, StatusUnavailable, m.Get(1, false).Status)
	assert.Equal(t, StatusUnavailable, m.Get(0, false).Status)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, int32(0), fp.requests.Load())
}
