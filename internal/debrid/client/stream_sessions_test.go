package debrid_client

// Garceslab Anime phase 2: direct debrid streams are per client. These tests use
// a fake provider (no debrid service is called) and check that concurrent
// direct streams of three clients don't interfere.

import (
	"context"
	"fmt"
	"seanime/internal/api/anilist"
	"seanime/internal/api/metadata"
	"seanime/internal/api/metadata_provider"
	"seanime/internal/database/models"
	"seanime/internal/debrid/debrid"
	"seanime/internal/events"
	hibiketorrent "seanime/internal/extension/hibike/torrent"
	"seanime/internal/hook"
	"seanime/internal/testmocks"
	"seanime/internal/util"
	"seanime/internal/util/result"
	"sync"
	"testing"
	"time"

	"github.com/samber/mo"
	"github.com/stretchr/testify/require"
)

// recordingWS records who each event went to ("*" = broadcast).
type recordingWS struct {
	*events.MockWSEventManager
	mu   sync.Mutex
	sent []recordedEvent
}

type recordedEvent struct {
	client  string
	kind    string
	payload interface{}
}

func (w *recordingWS) SendEvent(t string, payload interface{}) {
	w.mu.Lock()
	w.sent = append(w.sent, recordedEvent{"*", t, payload})
	w.mu.Unlock()
}

func (w *recordingWS) SendEventTo(clientId string, t string, payload interface{}, _ ...bool) {
	w.mu.Lock()
	w.sent = append(w.sent, recordedEvent{clientId, t, payload})
	w.mu.Unlock()
}

func (w *recordingWS) all() []recordedEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]recordedEvent(nil), w.sent...)
}

func (w *recordingWS) openURL(client string) (string, bool) {
	for _, e := range w.all() {
		if e.client == client && e.kind == events.ExternalPlayerOpenURL {
			v := fmt.Sprintf("%v", e.payload)
			return v, true
		}
	}
	return "", false
}

// gatedProvider resolves a torrent's link only when its gate opens.
type gatedProvider struct {
	fakeDebridProvider
	mu      sync.Mutex
	gates   map[string]chan struct{}
	deleted []string
	calls   map[string]int
}

func newGatedProvider() *gatedProvider {
	return &gatedProvider{gates: map[string]chan struct{}{}, calls: map[string]int{}}
}

func (p *gatedProvider) gate(id string) chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	g, ok := p.gates[id]
	if !ok {
		g = make(chan struct{})
		p.gates[id] = g
	}
	return g
}

func (p *gatedProvider) AddTorrent(opts debrid.AddTorrentOptions) (string, error) {
	p.mu.Lock()
	p.calls[opts.InfoHash]++
	p.mu.Unlock()
	return "item-" + opts.InfoHash, nil
}

func (p *gatedProvider) GetTorrentStreamUrl(ctx context.Context, opts debrid.StreamTorrentOptions, itemCh chan debrid.TorrentItem) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-p.gate(opts.ID):
		return "https://cdn.invalid/" + opts.ID + "/episode.mkv", nil
	}
}

func (p *gatedProvider) DeleteTorrent(id string) error {
	p.mu.Lock()
	p.deleted = append(p.deleted, id)
	p.mu.Unlock()
	return nil
}

type observed struct {
	client string
	ev     DirectStreamEvent
	info   DirectStreamInfo
}

func newSessionTestRepo(t *testing.T, provider debrid.Provider) (*Repository, *recordingWS, func() []observed) {
	t.Helper()
	logger := util.NewLogger()
	ws := &recordingWS{MockWSEventManager: events.NewMockWSEventManager(logger)}

	oldManager := hook.GlobalHookManager
	hook.SetGlobalHookManager(hook.NewHookManager(hook.NewHookManagerOptions{Logger: logger}))
	t.Cleanup(func() { hook.SetGlobalHookManager(oldManager) })

	meta := &syncMetadata{FakeMetadataProvider: testmocks.NewFakeMetadataProviderBuilder().Build()}
	repo := &Repository{
		provider:              mo.Some(provider),
		logger:                logger,
		settings:              &models.DebridSettings{Enabled: true},
		wsEventManager:        ws,
		ctxMap:                result.NewMap[string, context.CancelFunc](),
		completeAnimeCache:    nil,
		metadataProviderRef:   util.NewRef[metadata_provider.Provider](meta),
		previousStreamOptions: mo.None[*StartStreamOptions](),
	}
	repo.completeAnimeCache = newAnimeCacheWith(1, 2, 3)
	repo.streamManager = NewStreamManager(repo)

	var mu sync.Mutex
	var obs []observed
	repo.SetDirectStreamObserver(func(clientId string, info DirectStreamInfo, ev DirectStreamEvent) {
		mu.Lock()
		obs = append(obs, observed{clientId, ev, info})
		mu.Unlock()
	})
	return repo, ws, func() []observed {
		mu.Lock()
		defer mu.Unlock()
		return append([]observed(nil), obs...)
	}
}

func directStart(client string, mediaID int, hash string) *StartStreamOptions {
	return &StartStreamOptions{
		MediaId:       mediaID,
		EpisodeNumber: 1,
		AniDBEpisode:  "1",
		Torrent:       &hibiketorrent.AnimeTorrent{Name: "torrent-" + hash, InfoHash: hash, MagnetLink: "magnet:?xt=urn:btih:" + hash},
		FileId:        "1",
		ClientId:      client,
		PlaybackType:  PlaybackTypeExternalPlayer,
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDirectStreamsOfThreeClientsDontInterfere(t *testing.T) {
	provider := newGatedProvider()
	repo, ws, obs := newSessionTestRepo(t, provider)

	clients := []string{"tv-a", "tv-b", "tv-c"}
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c string) {
			defer wg.Done()
			require.NoError(t, repo.StartStream(context.Background(), directStart(c, i+1, fmt.Sprintf("hash%d", i))))
		}(i, c)
	}
	wg.Wait()
	require.ElementsMatch(t, clients, repo.DirectStreamClientIds())

	// Resolve b first, then a, then c: each client gets only its own link.
	for _, i := range []int{1, 0, 2} {
		close(provider.gate(fmt.Sprintf("item-hash%d", i)))
		c := clients[i]
		waitFor(t, "link for "+c, func() bool { _, ok := ws.openURL(c); return ok })
	}
	for i, c := range clients {
		u, _ := ws.openURL(c)
		require.Contains(t, u, fmt.Sprintf("item-hash%d", i))
		got, ok := repo.GetStreamURLFor(c)
		require.True(t, ok)
		require.Contains(t, got, fmt.Sprintf("item-hash%d", i))
	}

	// Status events of direct streams are never broadcast, and each client only
	// hears about its own torrent.
	for _, e := range ws.all() {
		require.NotEqual(t, "*", e.client, "broadcast event %s", e.kind)
		if st, ok := e.payload.(StreamState); ok && st.TorrentName != "-" {
			idx := map[string]int{"tv-a": 0, "tv-b": 1, "tv-c": 2}[e.client]
			require.Equal(t, fmt.Sprintf("torrent-hash%d", idx), st.TorrentName)
		}
	}

	ready := map[string]bool{}
	for _, o := range obs() {
		if o.ev == DirectStreamReady {
			ready[o.client] = true
		}
	}
	require.Len(t, ready, 3)
	_, relayed := repo.GetStreamURL()
	require.False(t, relayed, "no relayed stream")
}

func TestCancelOneDirectStreamLeavesTheOthers(t *testing.T) {
	provider := newGatedProvider()
	repo, ws, obs := newSessionTestRepo(t, provider)
	for i, c := range []string{"tv-a", "tv-b", "tv-c"} {
		require.NoError(t, repo.StartStream(context.Background(), directStart(c, i+1, fmt.Sprintf("hash%d", i))))
	}

	repo.CancelStreamFor("tv-b", &CancelStreamOptions{RemoveTorrent: true})
	require.ElementsMatch(t, []string{"tv-a", "tv-c"}, repo.DirectStreamClientIds())
	provider.mu.Lock()
	require.Equal(t, []string{"item-hash1"}, provider.deleted, "only b's torrent is removed")
	provider.mu.Unlock()

	// The upstream (relayed) cancel never touches direct streams.
	repo.CancelStream(&CancelStreamOptions{RemoveTorrent: true})
	require.ElementsMatch(t, []string{"tv-a", "tv-c"}, repo.DirectStreamClientIds())

	close(provider.gate("item-hash0"))
	close(provider.gate("item-hash1"))
	close(provider.gate("item-hash2"))
	waitFor(t, "a and c", func() bool {
		_, a := ws.openURL("tv-a")
		_, c := ws.openURL("tv-c")
		return a && c
	})
	time.Sleep(100 * time.Millisecond)
	_, b := ws.openURL("tv-b")
	require.False(t, b, "the cancelled stream never sends its link")
	for _, o := range obs() {
		require.False(t, o.client == "tv-b" && o.ev == DirectStreamReady)
	}
}

func TestNewEpisodeReplacesOnlyTheSameClientsStream(t *testing.T) {
	provider := newGatedProvider()
	repo, ws, _ := newSessionTestRepo(t, provider)
	require.NoError(t, repo.StartStream(context.Background(), directStart("tv-a", 1, "hash0")))
	require.NoError(t, repo.StartStream(context.Background(), directStart("tv-b", 2, "hash1")))

	// tv-a moves to another episode: its first stream is cancelled, tv-b's is not.
	require.NoError(t, repo.StartStream(context.Background(), directStart("tv-a", 3, "hash9")))
	close(provider.gate("item-hash0"))
	close(provider.gate("item-hash1"))
	waitFor(t, "b", func() bool { _, ok := ws.openURL("tv-b"); return ok })
	time.Sleep(100 * time.Millisecond)
	_, a := ws.openURL("tv-a")
	require.False(t, a, "the replaced stream of tv-a never sends its link")
	close(provider.gate("item-hash9"))
	waitFor(t, "a's new episode", func() bool { u, ok := ws.openURL("tv-a"); return ok && len(u) > 0 })
	u, _ := ws.openURL("tv-a")
	require.Contains(t, u, "item-hash9")
}

func TestRelayedSlotDoesNotCancelDirectStreams(t *testing.T) {
	provider := newGatedProvider()
	repo, _, _ := newSessionTestRepo(t, provider)
	require.NoError(t, repo.StartStream(context.Background(), directStart("tv-a", 1, "hash0")))
	sm := repo.streamManager

	// A relayed stream of another client (the web) takes the relayed slot only.
	web := sm.takeSlot(&StartStreamOptions{ClientId: "web", PlaybackType: PlaybackTypeNativePlayer})
	require.False(t, web.isDirect)
	require.ElementsMatch(t, []string{"tv-a"}, repo.DirectStreamClientIds())

	// A second relayed stream replaces the first relayed one, not tv-a's.
	web2 := sm.takeSlot(&StartStreamOptions{ClientId: "web-2", PlaybackType: PlaybackTypeDefault})
	require.True(t, web.isStopped())
	require.False(t, web2.isStopped())
	require.ElementsMatch(t, []string{"tv-a"}, repo.DirectStreamClientIds())

	// A direct stream of the relayed client replaces its relayed stream.
	_ = sm.takeSlot(&StartStreamOptions{ClientId: "web-2", PlaybackType: PlaybackTypeExternalPlayer})
	require.True(t, web2.isStopped())
	require.ElementsMatch(t, []string{"tv-a", "web-2"}, repo.DirectStreamClientIds())

	// A relayed stream of tv-a replaces tv-a's direct stream ("Retry via server").
	_ = sm.takeSlot(&StartStreamOptions{ClientId: "tv-a", PlaybackType: PlaybackTypeNativePlayer})
	require.ElementsMatch(t, []string{"web-2"}, repo.DirectStreamClientIds())
}

func TestFailedDirectStreamIsReported(t *testing.T) {
	provider := newGatedProvider()
	repo, ws, obs := newSessionTestRepo(t, provider)
	opts := directStart("tv-a", 1, "hash0")
	opts.Torrent = nil // no torrent: fails before adding anything
	require.Error(t, repo.StartStream(context.Background(), opts))
	failed := false
	for _, o := range obs() {
		failed = failed || (o.client == "tv-a" && o.ev == DirectStreamFailed)
	}
	require.True(t, failed)
	for _, e := range ws.all() {
		require.Equal(t, "tv-a", e.client)
	}
}

func TestIsDirect(t *testing.T) {
	require.True(t, PlaybackTypeExternalPlayer.IsDirect())
	require.True(t, PlaybackTypeNone.IsDirect())
	require.True(t, PlaybackTypeNoneAndAwait.IsDirect())
	require.False(t, PlaybackTypeNativePlayer.IsDirect())
	require.False(t, PlaybackTypeDefault.IsDirect())
}

func newAnimeCacheWith(ids ...int) *anilist.CompleteAnimeCache {
	c := anilist.NewCompleteAnimeCache()
	for _, id := range ids {
		c.Set(id, testmocks.NewBaseAnime(id, fmt.Sprintf("Show %d", id)).ToCompleteAnime())
	}
	for _, id := range []int{9} {
		c.Set(id, testmocks.NewBaseAnime(id, "Show 9").ToCompleteAnime())
	}
	return c
}

// syncMetadata makes the fake metadata provider safe for concurrent stream starts.
type syncMetadata struct {
	mu sync.Mutex
	*testmocks.FakeMetadataProvider
}

func (m *syncMetadata) GetAnimeMetadata(p metadata.Platform, mediaID int) (*metadata.AnimeMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.FakeMetadataProvider.GetAnimeMetadata(p, mediaID)
}

// A TV whose direct link failed asks again for the same release and file: the
// link event names them (also for auto-select), and the retry reuses the
// client's own direct slot.
func TestDirectLinkNamesTheReleaseAndARetryReusesTheSlot(t *testing.T) {
	provider := newGatedProvider()
	repo, ws, _ := newSessionTestRepo(t, provider)
	require.NoError(t, repo.StartStream(context.Background(), directStart("tv-a", 1, "hash0")))
	require.NoError(t, repo.StartStream(context.Background(), directStart("tv-b", 2, "hash1")))
	close(provider.gate("item-hash0"))
	waitFor(t, "a's link", func() bool { _, ok := ws.openURL("tv-a"); return ok })

	var payload ExternalPlayerLinkPayload
	for _, e := range ws.all() {
		if e.client == "tv-a" && e.kind == events.ExternalPlayerOpenURL {
			payload = e.payload.(ExternalPlayerLinkPayload)
		}
	}
	require.NotNil(t, payload.Torrent)
	require.Equal(t, "hash0", payload.Torrent.InfoHash)
	require.Equal(t, "1", payload.FileId)

	// The retry: the same torrent and file, for a fresh link.
	retry := directStart("tv-a", 1, "hash0")
	retry.Torrent = payload.Torrent
	retry.FileId = payload.FileId
	require.NoError(t, repo.StartStream(context.Background(), retry))
	waitFor(t, "a's second link", func() bool {
		n := 0
		for _, e := range ws.all() {
			if e.client == "tv-a" && e.kind == events.ExternalPlayerOpenURL {
				n++
			}
		}
		return n == 2
	})
	require.ElementsMatch(t, []string{"tv-a", "tv-b"}, repo.DirectStreamClientIds(), "one slot per client, retries included")
}
