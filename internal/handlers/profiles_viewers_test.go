package handlers

// Phase 2 (docs/phase2-direct-play.md): three profiles on three devices start
// direct debrid streams through the HTTP routes at the same time; a fourth gets
// LANES_FULL; events, cancellation and manual tracking stay per client. The
// debrid provider is a fake: no debrid service is called.

import (
	"context"
	"fmt"
	"net/http"
	"seanime/internal/api/metadata"
	"seanime/internal/api/metadata_provider"
	debrid_client "seanime/internal/debrid/client"
	"seanime/internal/debrid/debrid"
	"seanime/internal/events"
	"seanime/internal/profiles/sessions"
	"seanime/internal/testmocks"
	"seanime/internal/util"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

type viewerEvent struct {
	client string // "*" = broadcast
	kind   string
	body   string
}

// viewerWS records every event and who it went to.
type viewerWS struct {
	*events.MockWSEventManager
	mu   sync.Mutex
	sent []viewerEvent
}

func (w *viewerWS) SendEvent(t string, payload interface{}) {
	w.mu.Lock()
	w.sent = append(w.sent, viewerEvent{"*", t, fmt.Sprintf("%+v", payload)})
	w.mu.Unlock()
}

func (w *viewerWS) SendEventTo(clientId string, t string, payload interface{}, _ ...bool) {
	w.mu.Lock()
	w.sent = append(w.sent, viewerEvent{clientId, t, fmt.Sprintf("%+v", payload)})
	w.mu.Unlock()
}

func (w *viewerWS) events() []viewerEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]viewerEvent(nil), w.sent...)
}

func (w *viewerWS) has(client, kind, contains string) bool {
	for _, e := range w.events() {
		if e.client == client && e.kind == kind && strings.Contains(e.body, contains) {
			return true
		}
	}
	return false
}

// fakeDirectProvider resolves each torrent when its gate opens.
type fakeDirectProvider struct {
	mu    sync.Mutex
	gates map[string]chan struct{}
}

func (p *fakeDirectProvider) gate(id string) chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gates == nil {
		p.gates = map[string]chan struct{}{}
	}
	g, ok := p.gates[id]
	if !ok {
		g = make(chan struct{})
		p.gates[id] = g
	}
	return g
}

func (p *fakeDirectProvider) GetSettings() debrid.Settings {
	return debrid.Settings{ID: "fake", Name: "Fake"}
}
func (p *fakeDirectProvider) Authenticate(string) error { return nil }
func (p *fakeDirectProvider) AddTorrent(o debrid.AddTorrentOptions) (string, error) {
	return "item-" + o.InfoHash, nil
}
func (p *fakeDirectProvider) GetTorrentStreamUrl(ctx context.Context, o debrid.StreamTorrentOptions, _ chan debrid.TorrentItem) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-p.gate(o.ID):
		return "https://cdn.invalid/" + o.ID + "/episode.mkv", nil
	}
}
func (p *fakeDirectProvider) GetTorrentDownloadUrl(debrid.DownloadTorrentOptions) (string, error) {
	return "", nil
}
func (p *fakeDirectProvider) GetInstantAvailability([]string) map[string]debrid.TorrentItemInstantAvailability {
	return nil
}
func (p *fakeDirectProvider) GetTorrent(string) (*debrid.TorrentItem, error) { return nil, nil }
func (p *fakeDirectProvider) GetTorrentInfo(debrid.GetTorrentInfoOptions) (*debrid.TorrentInfo, error) {
	return nil, nil
}
func (p *fakeDirectProvider) GetTorrents() ([]*debrid.TorrentItem, error) { return nil, nil }
func (p *fakeDirectProvider) DeleteTorrent(string) error                  { return nil }

type syncMeta struct {
	mu sync.Mutex
	*testmocks.FakeMetadataProvider
}

func (m *syncMeta) GetAnimeMetadata(pl metadata.Platform, id int) (*metadata.AnimeMetadata, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.FakeMetadataProvider.GetAnimeMetadata(pl, id)
}

func newViewersEnv(t *testing.T) (*profilesTestEnv, *viewerWS, *fakeDirectProvider) {
	t.Helper()
	env, fake := newM2Env(t)
	for id := 1; id <= 4; id++ {
		fake.SetMedia(testmocks.NewBaseAnime(id, fmt.Sprintf("Show %d", id)))
	}
	logger := zerolog.Nop()
	ws := &viewerWS{MockWSEventManager: events.NewMockWSEventManager(&logger)}
	provider := &fakeDirectProvider{}
	meta := &syncMeta{FakeMetadataProvider: testmocks.NewFakeMetadataProviderBuilder().Build()}
	repo := debrid_client.NewRepository(&debrid_client.NewRepositoryOptions{
		Logger:              &logger,
		WSEventManager:      ws,
		Database:            env.app.Database,
		MetadataProviderRef: util.NewRef[metadata_provider.Provider](meta),
		PlatformRef:         env.app.AnilistPlatformRef,
	})
	repo.SetProviderForTesting(provider)
	env.app.DebridClientRepository = repo
	env.app.ProfilesRuntime.Viewers.SetEvents(ws)
	env.app.InstallProfilesRuntime() // again, now with the debrid repository
	return env, ws, provider
}

func startDirect(env *profilesTestEnv, client, token string, mediaID int) (int, map[string]any) {
	hash := fmt.Sprintf("hash%d", mediaID)
	return env.call(http.MethodPost, "/api/v1/debrid/stream/start", client, token, map[string]any{
		"mediaId": mediaID, "episodeNumber": 1, "aniDBEpisode": "1", "fileId": "1",
		"playbackType": "externalPlayerLink", "clientId": client,
		"torrent": map[string]any{"name": "torrent-" + hash, "infoHash": hash, "magnetLink": "magnet:?xt=urn:btih:" + hash},
	})
}

func waitUntil(t *testing.T, what string, cond func() bool) {
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

func TestThreeViewersDirectPlayThroughTheRoutes(t *testing.T) {
	env, ws, provider := newViewersEnv(t)
	viewers := env.app.ViewerSessions()
	type viewer struct{ client, token, name string }
	var vs []viewer
	for _, name := range []string{"Ana", "Bob", "Carl", "Dana"} {
		client := "tv-" + strings.ToLower(name)
		_, token := env.session(name, client)
		vs = append(vs, viewer{client, token, name})
	}

	// Three devices start at the same time.
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = startDirect(env, vs[i].client, vs[i].token, i+1)
		}(i)
	}
	wg.Wait()
	require.Equal(t, []int{200, 200, 200}, codes)
	require.Equal(t, 3, viewers.Count())

	// The fourth gets LANES_FULL with who is watching.
	code, res := startDirect(env, vs[3].client, vs[3].token, 4)
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "LANES_FULL: All 3 viewing slots are in use (Ana, Bob and Carl)", res["error"])

	// Links resolve in another order; each goes to its client only.
	for _, i := range []int{2, 0, 1} {
		close(provider.gate(fmt.Sprintf("item-hash%d", i+1)))
	}
	for i := 0; i < 3; i++ {
		c := vs[i].client
		waitUntil(t, "link for "+c, func() bool {
			return ws.has(c, events.ExternalPlayerOpenURL, fmt.Sprintf("item-hash%d", i+1))
		})
	}
	for _, e := range ws.events() {
		require.NotEqual(t, "*", e.client, "direct play events are never broadcast: %s", e.kind)
		if e.kind == events.ExternalPlayerOpenURL || e.kind == events.DebridStreamState {
			idx := map[string]int{"tv-ana": 1, "tv-bob": 2, "tv-carl": 3}[e.client]
			require.NotZero(t, idx, "event for %s", e.client)
			require.NotContains(t, e.body, fmt.Sprintf("hash%d", 1+(idx%3)), "%s heard another stream", e.client)
		}
	}
	for _, s := range viewers.List() {
		require.Equal(t, sessions.StateReady, s.State)
		require.True(t, strings.HasPrefix(s.MediaTitle, "Show "))
	}

	// Manual tracking is per session: Bob's start and cancel don't touch Ana's.
	for i := 0; i < 2; i++ {
		code, _ := env.call(http.MethodPost, "/api/v1/playback-manager/manual-tracking/start", vs[i].client, vs[i].token,
			map[string]any{"mediaId": i + 1, "episodeNumber": 1, "clientId": vs[i].client})
		require.Equal(t, 200, code)
	}
	ta, ok := viewers.Tracking("tv-ana")
	require.True(t, ok)
	require.Equal(t, 1, ta.MediaID)
	code, _ = env.call(http.MethodPost, "/api/v1/playback-manager/manual-tracking/cancel", "tv-bob", vs[1].token,
		map[string]any{"mediaId": 2, "episodeNumber": 1})
	require.Equal(t, 200, code)
	_, ok = viewers.Tracking("tv-ana")
	require.True(t, ok, "Ana keeps her tracking")
	_, ok = viewers.Get("tv-bob")
	require.False(t, ok, "Bob's slot is free")
	require.True(t, ws.has("tv-bob", sessions.EventManualTrackingStopped, ""))
	require.False(t, ws.has("tv-ana", sessions.EventManualTrackingStopped, ""))

	// Dana now fits.
	code, _ = startDirect(env, vs[3].client, vs[3].token, 4)
	require.Equal(t, 200, code)

	// Cancelling Carl's stream frees only Carl's slot.
	code, _ = env.call(http.MethodPost, "/api/v1/debrid/stream/cancel", "tv-carl", vs[2].token, map[string]any{"options": map[string]any{"removeTorrent": false}})
	require.Equal(t, 200, code)
	_, ok = viewers.Get("tv-carl")
	require.False(t, ok)
	_, ok = viewers.Get("tv-dana")
	require.True(t, ok)
	require.ElementsMatch(t, []string{"tv-ana", "tv-dana"}, env.app.DebridClientRepository.DirectStreamClientIds())

	// "Now watching" lists the sessions; Stop ends Dana's and tells her device.
	code, res = env.call(http.MethodGet, "/api/v1/profiles/viewers", "tv-ana", vs[0].token, nil)
	require.Equal(t, 200, code)
	data := dataMap(t, res)
	require.EqualValues(t, 3, data["max"])
	list := data["sessions"].([]any)
	require.Len(t, list, 2)
	var danaID string
	for _, s := range list {
		m := s.(map[string]any)
		require.NotContains(t, m, "clientId", "client ids are not exposed")
		if m["profileName"] == "Dana" {
			danaID = m["id"].(string)
		}
	}
	require.NotEmpty(t, danaID)
	code, _ = env.call(http.MethodPost, "/api/v1/profiles/viewers/"+danaID+"/stop", "tv-ana", vs[0].token, nil)
	require.Equal(t, 200, code)
	require.True(t, ws.has("tv-dana", sessions.EventSessionEnded, "admin"))
	require.ElementsMatch(t, []string{"tv-ana"}, env.app.DebridClientRepository.DirectStreamClientIds(), "Dana's pending resolve is cancelled")
	code, _ = env.call(http.MethodPost, "/api/v1/profiles/viewers/"+danaID+"/stop", "tv-ana", vs[0].token, nil)
	require.Equal(t, http.StatusNotFound, code)
}

func TestDirectSessionDisconnectGraceThroughTheWebsocketHooks(t *testing.T) {
	env, ws, _ := newViewersEnv(t)
	// A registry with no grace, so the disconnect frees the slot right away.
	env.app.ProfilesRuntime.Viewers = sessions.New(sessions.Options{DisconnectGrace: -1, Events: ws})
	env.app.InstallProfilesRuntime()
	_, token := env.session("Ana", "tv-ana")
	code, _ := startDirect(env, "tv-ana", token, 1)
	require.Equal(t, 200, code)
	h := &Handler{App: env.app}
	h.viewerWebsocketDisconnected("tv-ana")
	_, ok := env.app.ViewerSessions().Get("tv-ana")
	require.False(t, ok)
	require.True(t, ws.has("tv-ana", sessions.EventSessionEnded, "disconnected"))
}

func TestRelayedStartKeepsDirectSessionsAndCountsAsAViewer(t *testing.T) {
	env, _, _ := newViewersEnv(t)
	var tokens []string
	for i, name := range []string{"Ana", "Bob", "Carl"} {
		_, token := env.session(name, fmt.Sprintf("tv-%d", i))
		tokens = append(tokens, token)
	}
	for i := 0; i < 2; i++ {
		code, _ := startDirect(env, fmt.Sprintf("tv-%d", i), tokens[i], i+1)
		require.Equal(t, 200, code)
	}
	// A relayed playback takes the lane (as /torrentstream/start does in the middleware).
	require.NoError(t, env.app.ProfilesRuntime.Lane.AcquireFor(nil, "browser"))
	code, res := startDirect(env, "tv-2", tokens[2], 3)
	require.Equal(t, http.StatusConflict, code, "two direct + one relayed = full")
	require.Contains(t, res["error"], "LANES_FULL")
	require.Equal(t, 2, env.app.ViewerSessions().Count(), "the relayed playback cancelled no direct session")
}

// Direct play retries (the TV asks for a fresh link to the same file up to
// three times) restart the stream as the same client: they replace its own
// viewer session and never need a free slot.
func TestDirectPlayRetryReusesTheDevicesSession(t *testing.T) {
	env, ws, provider := newViewersEnv(t)
	viewers := env.app.ViewerSessions()
	var tokens []string
	for _, name := range []string{"Ana", "Bob", "Carl"} {
		_, token := env.session(name, "tv-"+strings.ToLower(name))
		tokens = append(tokens, token)
	}
	for i, name := range []string{"ana", "bob", "carl"} {
		code, _ := startDirect(env, "tv-"+name, tokens[i], i+1)
		require.Equal(t, 200, code)
	}
	require.Equal(t, 3, viewers.Count())
	first, ok := viewers.Get("tv-ana")
	require.True(t, ok)

	// All three slots are in use; Ana's TV retries its link three times.
	for retry := 0; retry < 3; retry++ {
		code, res := startDirect(env, "tv-ana", tokens[0], 1)
		require.Equal(t, 200, code, "retry %d: %v", retry, res)
		require.Equal(t, 3, viewers.Count())
	}
	s, ok := viewers.Get("tv-ana")
	require.True(t, ok)
	require.Equal(t, first.MediaID, s.MediaID)
	require.False(t, ws.has("tv-ana", sessions.EventSessionEnded, ""), "a retry doesn't end the device's session")
	require.ElementsMatch(t, []string{"tv-ana", "tv-bob", "tv-carl"}, env.app.DebridClientRepository.DirectStreamClientIds())

	close(provider.gate("item-hash1"))
	waitUntil(t, "Ana's link", func() bool { return ws.has("tv-ana", events.ExternalPlayerOpenURL, "item-hash1") })
	require.True(t, ws.has("tv-ana", events.ExternalPlayerOpenURL, "FileId:1"), "the link names the file for the next retry")
}
