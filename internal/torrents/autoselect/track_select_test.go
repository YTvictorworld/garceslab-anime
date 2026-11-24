package autoselect

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"seanime/internal/debrid/debrid"
	hibiketorrent "seanime/internal/extension/hibike/torrent"
	"seanime/internal/torrents/trackprobe"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLinker hands out links to fixture files served by an httptest server.
type fakeLinker struct {
	t        *testing.T
	srv      *httptest.Server
	fixtures map[string]string // info hash -> fixture name
	rate     map[string]bool   // info hash -> answer 429

	mu       sync.Mutex
	calls    []string
	times    []time.Time
	releases map[string]bool // info hash -> keep
}

func newFakeLinker(t *testing.T, fixtures map[string]string) *fakeLinker {
	t.Helper()
	f := &fakeLinker{t: t, fixtures: fixtures, rate: map[string]bool{}, releases: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		data, err := os.ReadFile(filepath.Join("..", "trackprobe", "testdata", name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLinker) CachedFileLink(ctx context.Context, opts debrid.CachedFileLinkOptions) (*debrid.CachedFileLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, opts.InfoHash)
	f.times = append(f.times, time.Now())
	if f.rate[opts.InfoHash] {
		return nil, debrid.ErrRateLimited
	}
	name, ok := f.fixtures[opts.InfoHash]
	if !ok {
		return nil, debrid.ErrNotCached
	}
	hash := opts.InfoHash
	return &debrid.CachedFileLink{
		URL: f.srv.URL + "/" + name + "?token=secret",
		Release: func(keep bool) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.releases[hash] = keep
		},
	}, nil
}

func (f *fakeLinker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func probeCandidate(hash, name string) (*hibiketorrent.AnimeTorrent, *Result) {
	t := &hibiketorrent.AnimeTorrent{Name: name, InfoHash: hash, Provider: "fake"}
	file := &debrid.TorrentItemFile{ID: "ep01.mkv", Name: "ep01.mkv", Path: "/" + hash + "/ep01.mkv"}
	return t, &Result{
		DebridTorrent:   &debrid.TorrentInfo{Hash: hash, Files: []*debrid.TorrentItemFile{file}},
		DebridFileID:    file.ID,
		OriginalTorrent: t,
	}
}

type probeFixture struct {
	ranked  []*hibiketorrent.AnimeTorrent
	results map[string]*Result
	cached  map[string]bool
}

func newProbeFixture(entries ...[2]string) *probeFixture {
	pf := &probeFixture{results: map[string]*Result{}, cached: map[string]bool{}}
	for _, e := range entries {
		t, r := probeCandidate(e[0], e[1])
		pf.ranked = append(pf.ranked, t)
		pf.results[e[0]] = r
		pf.cached[e[0]] = true
	}
	return pf
}

func (pf *probeFixture) options(linker debrid.CachedFileLinker, pref TrackPreference) *TrackProbeOptions {
	return &TrackProbeOptions{
		Preference: pref,
		Linker:     linker,
		ProviderID: "fake",
		TopN:       4,
		Pace:       time.Millisecond,
		resolve: func(ctx context.Context, t *hibiketorrent.AnimeTorrent) (*Result, error) {
			return pf.results[t.InfoHash], nil
		},
	}
}

func (pf *probeFixture) run(s *AutoSelect, o *TrackProbeOptions) *Result {
	return s.preferTracks(context.Background(), nil, 1, pf.ranked, pf.results[pf.ranked[0].InfoHash], pf.cached, o, nil)
}

type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newLoggedAutoSelect() (*AutoSelect, *logBuffer) {
	buf := &logBuffer{}
	logger := zerolog.New(buf)
	return New(&NewAutoSelectOptions{Logger: &logger}), buf
}

var spanishSubs = NewTrackPreference("spa, es, spanish", "jpn, jp, ja, japanese", "")

func TestNewTrackPreference(t *testing.T) {
	p := NewTrackPreference("spa, es, spanish", "jpn, spa", "")
	assert.Equal(t, []string{"es"}, p.Subtitles)
	assert.Empty(t, p.Dub, "Japanese first: no dub preference")
	assert.Equal(t, []string{"signs", "songs"}, p.IgnoredLabels)

	p = NewTrackPreference("", "spa, jpn", "signs")
	assert.Equal(t, []string{"es"}, p.Dub)
	assert.False(t, p.IsEmpty())
	assert.True(t, NewTrackPreference("", "jpn", "").IsEmpty())
}

func TestPreferTracks_PicksSpanishSubCandidate(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[SubsPlease] Show - 01 (1080p) [AAA].mkv"},
		[2]string{"bbb", "[Erai-raws] Show - 01 [1080p][MultiSub].mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "multisub.mkv"})
	s, logs := newLoggedAutoSelect()
	o := pf.options(linker, spanishSubs)
	var progress []string
	o.OnProgress = func(d string) { progress = append(progress, d) }

	res := pf.run(s, o)
	require.NotNil(t, res)
	assert.Equal(t, "bbb", res.OriginalTorrent.InfoHash)
	assert.Equal(t, map[string]bool{"aaa": false, "bbb": true}, linker.releases, "only the pick keeps its torrent")
	assert.Equal(t, []string{"Checking subtitle languages… (1/2)", "Checking subtitle languages… (2/2)"}, progress)
	assert.Contains(t, logs.String(), "Picked [Erai-raws] Show - 01 [1080p][MultiSub].mkv (has spa subs) over [SubsPlease] Show - 01 (1080p) [AAA].mkv (eng, fre only)")
	assert.NotContains(t, logs.String(), "secret")
	assert.NotContains(t, logs.String(), "127.0.0.1")
}

func TestPreferTracks_ChoiceAlreadyHasIt(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[Erai-raws] Show - 01 [1080p][MultiSub].mkv"},
		[2]string{"bbb", "[SubsPlease] Show - 01 (1080p).mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "multisub.mkv", "bbb": "eng-fre.mkv"})
	s, logs := newLoggedAutoSelect()
	res := pf.run(s, pf.options(linker, spanishSubs))
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash)
	assert.Equal(t, 1, linker.callCount(), "stops at the first candidate with the language")
	assert.Equal(t, map[string]bool{"aaa": true}, linker.releases)
	assert.Contains(t, logs.String(), "Kept [Erai-raws]")
}

func TestPreferTracks_NoPreferenceSkipsProbing(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[SubsPlease] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[Erai-raws] Show - 01 [1080p][MultiSub].mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "multisub.mkv"})
	s, _ := newLoggedAutoSelect()
	res := pf.run(s, pf.options(linker, NewTrackPreference("", "jpn", "")))
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash)
	assert.Zero(t, linker.callCount())
}

func TestPreferTracks_NoneHasItKeepsNameChoice(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[SubsPlease] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[Group] Show - 01 (1080p).mkv"}, // Spanish forced signs only
		[2]string{"ccc", "[Other] Show - 01 (1080p).mp4"},
	)
	pf.results["ccc"].DebridTorrent.Files[0].Path = "/ccc/ep01.mp4"
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "spa-signs.mkv", "ccc": "video.mp4"})
	s, logs := newLoggedAutoSelect()
	res := pf.run(s, pf.options(linker, spanishSubs))
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash)
	assert.Equal(t, 2, linker.callCount(), "the MP4 is not probed")
	assert.Equal(t, map[string]bool{"aaa": true, "bbb": false}, linker.releases)
	assert.Contains(t, logs.String(), "No checked candidate has spa subs")
}

func TestPreferTracks_SameResolutionOnly(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[SubsPlease] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[Erai-raws] Show - 01 [720p][MultiSub].mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "multisub.mkv"})
	s, _ := newLoggedAutoSelect()
	res := pf.run(s, pf.options(linker, spanishSubs))
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash)
	assert.Equal(t, []string{"aaa"}, linker.calls)
}

func TestPreferTracks_OnlyCachedCandidates(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[SubsPlease] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[Erai-raws] Show - 01 [1080p][MultiSub].mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "multisub.mkv"})
	s, _ := newLoggedAutoSelect()

	pf.cached["bbb"] = false
	res := pf.run(s, pf.options(linker, spanishSubs))
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash)
	assert.Equal(t, []string{"aaa"}, linker.calls)

	// Uncached name-based choice: nothing is probed at all.
	pf.cached["bbb"] = true
	pf.cached["aaa"] = false
	res = pf.run(s, pf.options(linker, spanishSubs))
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash)
	assert.Equal(t, []string{"aaa"}, linker.calls)
}

func TestPreferTracks_TopN(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[A] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[B] Show - 01 (1080p).mkv"},
		[2]string{"ccc", "[C] Show - 01 (1080p).mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "eng-fre.mkv", "ccc": "multisub.mkv"})
	s, _ := newLoggedAutoSelect()
	o := pf.options(linker, spanishSubs)
	o.TopN = 2
	res := pf.run(s, o)
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash)
	assert.Equal(t, []string{"aaa", "bbb"}, linker.calls)
}

func TestPreferTracks_DubBeatsSubs(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[A] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[B] Show - 01 (1080p).mkv"},
		[2]string{"ccc", "[C] Show - 01 (1080p).mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "multisub.mkv", "ccc": "spa-dub.mkv"})
	s, logs := newLoggedAutoSelect()
	res := pf.run(s, pf.options(linker, NewTrackPreference("spa", "spa, jpn", "")))
	assert.Equal(t, "ccc", res.OriginalTorrent.InfoHash)
	assert.Contains(t, logs.String(), "(has spa dub)")
}

func TestPreferTracks_CacheHit(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[SubsPlease] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[Erai-raws] Show - 01 [1080p][MultiSub].mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "multisub.mkv"})
	cache := trackprobe.NewCache(t.TempDir(), 0)
	s, _ := newLoggedAutoSelect()
	o := pf.options(linker, spanishSubs)
	o.Cache = cache

	res := pf.run(s, o)
	assert.Equal(t, "bbb", res.OriginalTorrent.InfoHash)
	assert.Equal(t, 2, linker.callCount())

	res = pf.run(s, o)
	assert.Equal(t, "bbb", res.OriginalTorrent.InfoHash)
	assert.Equal(t, 2, linker.callCount(), "the second run reads both results from the cache")
}

func TestPreferTracks_RateLimitStops(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[SubsPlease] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[Erai-raws] Show - 01 [1080p][MultiSub].mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "multisub.mkv"})
	linker.rate["aaa"] = true
	s, logs := newLoggedAutoSelect()
	res := pf.run(s, pf.options(linker, spanishSubs))
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash, "falls back to the name-based choice")
	assert.Equal(t, []string{"aaa"}, linker.calls, "no probe after the 429")
	assert.Contains(t, logs.String(), "Stopped checking track languages (rate limited)")
}

func TestPreferTracks_RateLimitFromFileHost(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[SubsPlease] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[Erai-raws] Show - 01 [1080p][MultiSub].mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "multisub.mkv"})
	s, _ := newLoggedAutoSelect()
	o := pf.options(linker, spanishSubs)
	o.Probe = func(ctx context.Context, link string) (*trackprobe.Tracks, error) {
		return nil, trackprobe.ErrRateLimited
	}
	res := pf.run(s, o)
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash)
	assert.Equal(t, []string{"aaa"}, linker.calls)
	assert.Equal(t, map[string]bool{"aaa": true}, linker.releases)
}

func TestPreferTracks_BudgetTimeout(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[A] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[B] Show - 01 (1080p).mkv"},
		[2]string{"ccc", "[C] Show - 01 (1080p).mkv"},
		[2]string{"ddd", "[D] Show - 01 (1080p).mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "eng-fre.mkv", "ccc": "eng-fre.mkv", "ddd": "multisub.mkv"})
	s, logs := newLoggedAutoSelect()
	o := pf.options(linker, spanishSubs)
	o.Budget = 300 * time.Millisecond
	o.PerCandidate = 200 * time.Millisecond
	o.Probe = func(ctx context.Context, link string) (*trackprobe.Tracks, error) {
		<-ctx.Done() // a host that never answers
		return nil, ctx.Err()
	}
	start := time.Now()
	res := pf.run(s, o)
	elapsed := time.Since(start)
	assert.Equal(t, "aaa", res.OriginalTorrent.InfoHash)
	assert.Less(t, elapsed, 600*time.Millisecond)
	assert.Less(t, linker.callCount(), 4)
	assert.Contains(t, logs.String(), "budget spent")
}

func TestPreferTracks_Pacing(t *testing.T) {
	pf := newProbeFixture(
		[2]string{"aaa", "[A] Show - 01 (1080p).mkv"},
		[2]string{"bbb", "[B] Show - 01 (1080p).mkv"},
		[2]string{"ccc", "[C] Show - 01 (1080p).mkv"},
	)
	linker := newFakeLinker(t, map[string]string{"aaa": "eng-fre.mkv", "bbb": "eng-fre.mkv", "ccc": "multisub.mkv"})
	s, _ := newLoggedAutoSelect()
	o := pf.options(linker, spanishSubs)
	o.Pace = 150 * time.Millisecond
	res := pf.run(s, o)
	assert.Equal(t, "ccc", res.OriginalTorrent.InfoHash)
	require.Len(t, linker.times, 3)
	for i := 1; i < 3; i++ {
		assert.GreaterOrEqual(t, linker.times[i].Sub(linker.times[i-1]), 140*time.Millisecond)
	}
}
