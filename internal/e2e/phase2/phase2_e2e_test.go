//go:build e2e

// Package phase2 is an end-to-end check of phase 2 (docs/phase2-direct-play.md)
// against a running server binary: four fake devices (websocket + HTTP), each
// with its own profile, start debrid streams at the same time on the Dummy
// Debrid provider (a local file served by the server itself; no debrid service
// is called). Run it with scripts/e2e-phase2.sh, which starts a throwaway
// server; SEANIME_E2E_URL points the test at it.
package phase2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// Anime ids (AniList) the devices "watch"; metadata comes from AniList anonymously.
var mediaIDs = []int{1535, 5114, 20, 21}

type device struct {
	t       *testing.T
	base    string
	id      string
	name    string
	token   string
	conn    *websocket.Conn
	mu      sync.Mutex
	events  []wsEvent
	closed  chan struct{}
	mediaID int
}

type wsEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

func baseURL(t *testing.T) string {
	u := os.Getenv("SEANIME_E2E_URL")
	if u == "" {
		t.Skip("SEANIME_E2E_URL not set (run scripts/e2e-phase2.sh)")
	}
	return strings.TrimRight(u, "/")
}

func call(t *testing.T, base, method, path, clientID, token string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, rdr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36 e2e")
	if clientID != "" {
		req.Header.Set("X-Seanime-Client-Id", clientID)
		req.Header.Set("X-Seanime-Client-Platform", "web")
	}
	if token != "" {
		req.Header.Set("X-Seanime-Profile", token)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func newDevice(t *testing.T, base, name string, mediaID int) *device {
	t.Helper()
	d := &device{t: t, base: base, id: "e2e-tv-" + strings.ToLower(name), name: name, closed: make(chan struct{}), mediaID: mediaID}
	code, res := call(t, base, http.MethodPost, "/api/v1/profiles", d.id, "", map[string]any{"name": name, "avatarId": "gl-lorelei-01"})
	require.Equal(t, 200, code, "create profile: %v", res)
	pid := int64(res["data"].(map[string]any)["id"].(float64))
	code, res = call(t, base, http.MethodPost, fmt.Sprintf("/api/v1/profiles/%d/select", pid), d.id, "", map[string]any{})
	require.Equal(t, 200, code, "select profile: %v", res)
	d.token = res["data"].(map[string]any)["profileToken"].(string)
	d.connect()
	return d
}

func (d *device) connect() {
	u, _ := url.Parse(d.base)
	u.Scheme = strings.Replace(u.Scheme, "http", "ws", 1)
	u.Path = "/events"
	q := u.Query()
	q.Set("id", d.id)
	q.Set("platform", "web")
	q.Set("profile", d.token)
	u.RawQuery = q.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	require.NoError(d.t, err)
	d.conn = conn
	d.closed = make(chan struct{})
	go func() {
		defer close(d.closed)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var ev wsEvent
			if json.Unmarshal(msg, &ev) == nil {
				d.mu.Lock()
				d.events = append(d.events, ev)
				d.mu.Unlock()
			}
		}
	}()
}

func (d *device) close() {
	_ = d.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = d.conn.Close()
	<-d.closed
}

func (d *device) got(kind string) []wsEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ret []wsEvent
	for _, e := range d.events {
		if e.Type == kind {
			ret = append(ret, e)
		}
	}
	return ret
}

func (d *device) start() (int, map[string]any) {
	hash := fmt.Sprintf("%040d", d.mediaID)
	return call(d.t, d.base, http.MethodPost, "/api/v1/debrid/stream/start", d.id, d.token, map[string]any{
		"mediaId": d.mediaID, "episodeNumber": 1, "aniDBEpisode": "1", "fileId": "1",
		"playbackType": "externalPlayerLink", "clientId": d.id,
		"torrent": map[string]any{"name": "e2e-torrent-" + d.name, "infoHash": hash, "magnetLink": "magnet:?xt=urn:btih:" + hash},
	})
}

func eventually(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func viewers(t *testing.T, d *device) []map[string]any {
	code, res := call(t, d.base, http.MethodGet, "/api/v1/profiles/viewers", d.id, d.token, nil)
	require.Equal(t, 200, code, "%v", res)
	var ret []map[string]any
	for _, s := range res["data"].(map[string]any)["sessions"].([]any) {
		ret = append(ret, s.(map[string]any))
	}
	return ret
}

func TestThreeViewersAtOnce(t *testing.T) {
	base := baseURL(t)
	setupDummyDebrid(t, base)

	var devs []*device
	for i, name := range []string{"Ana", "Bob", "Carl", "Dana"} {
		devs = append(devs, newDevice(t, base, name, mediaIDs[i]))
	}
	defer func() {
		for _, d := range devs {
			_ = d.conn.Close()
		}
	}()

	// 1. Three devices start at the same time.
	var wg sync.WaitGroup
	codes := make([]int, 3)
	bodies := make([]map[string]any, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], bodies[i] = devs[i].start()
		}(i)
	}
	wg.Wait()
	for i := 0; i < 3; i++ {
		require.Equal(t, 200, codes[i], "%s: %v", devs[i].name, bodies[i])
	}

	// 2. The fourth is refused with who is watching.
	code, res := devs[3].start()
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "LANES_FULL: All 3 viewing slots are in use (Ana, Bob and Carl)", res["error"])

	// 3. Each device gets its own link, and only its own stream events.
	for i := 0; i < 3; i++ {
		d := devs[i]
		eventually(t, "link for "+d.name, 60*time.Second, func() bool { return len(d.got("external-player-open-url")) > 0 })
		var p struct {
			URL     string `json:"url"`
			MediaID int    `json:"mediaId"`
		}
		require.NoError(t, json.Unmarshal(d.got("external-player-open-url")[0].Payload, &p))
		require.Equal(t, d.mediaID, p.MediaID)
		require.Len(t, d.got("external-player-open-url"), 1)
		// The dummy link serves the fixture with Range, as a CDN would.
		req, _ := http.NewRequest(http.MethodGet, p.URL, nil)
		req.Header.Set("Range", "bytes=0-3")
		r, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		head, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		require.Equal(t, http.StatusPartialContent, r.StatusCode)
		require.Equal(t, []byte{0x1a, 0x45, 0xdf, 0xa3}, head, "an MKV")
		for _, e := range d.got("debrid-stream-state") {
			var st struct {
				TorrentName string `json:"torrentName"`
			}
			_ = json.Unmarshal(e.Payload, &st)
			// The dummy names its progress items after its profile ("e2e"); the
			// stream's own events carry the torrent's name.
			if strings.HasPrefix(st.TorrentName, "e2e-torrent-") {
				require.Equal(t, "e2e-torrent-"+d.name, st.TorrentName, "%s heard another device's stream", d.name)
			}
		}
	}
	require.Empty(t, devs[3].got("debrid-stream-state"), "the refused device hears nothing")
	require.Empty(t, devs[3].got("external-player-open-url"))

	// 4. The players start: manual tracking per session.
	for i := 0; i < 3; i++ {
		code, res := call(t, base, http.MethodPost, "/api/v1/playback-manager/manual-tracking/start", devs[i].id, devs[i].token,
			map[string]any{"mediaId": devs[i].mediaID, "episodeNumber": 1, "clientId": devs[i].id})
		require.Equal(t, 200, code, "%v", res)
	}
	list := viewers(t, devs[0])
	require.Len(t, list, 3)
	for _, s := range list {
		require.Equal(t, "playing", s["state"])
		require.Equal(t, "LG TV", s["device"])
	}

	// 5. Bob closes his player: only his slot frees; Dana now fits.
	code, _ = call(t, base, http.MethodPost, "/api/v1/playback-manager/manual-tracking/cancel", devs[1].id, devs[1].token,
		map[string]any{"mediaId": devs[1].mediaID, "episodeNumber": 1})
	require.Equal(t, 200, code)
	require.Len(t, viewers(t, devs[0]), 2)
	require.NotEmpty(t, devs[1].got("playback-manager-manual-tracking-stopped"))
	require.Empty(t, devs[0].got("playback-manager-manual-tracking-stopped"), "Ana keeps watching")
	code, res = devs[3].start()
	require.Equal(t, 200, code, "%v", res)

	// 6. Stop from Settings ends Dana's session and tells her device only.
	var danaID string
	for _, s := range viewers(t, devs[0]) {
		if s["profileName"] == "Dana" {
			danaID = s["id"].(string)
		}
	}
	require.NotEmpty(t, danaID)
	code, _ = call(t, base, http.MethodPost, "/api/v1/profiles/viewers/"+danaID+"/stop", devs[0].id, devs[0].token, nil)
	require.Equal(t, 200, code)
	eventually(t, "Dana told", 5*time.Second, func() bool { return len(devs[3].got("viewer-session-ended")) > 0 })
	require.Empty(t, devs[0].got("viewer-session-ended"))
	require.Empty(t, devs[2].got("viewer-session-ended"))

	// 7. Carl's TV turns off (websocket closes): his slot frees after the grace.
	devs[2].close()
	eventually(t, "Carl's slot freed after the disconnect grace", 30*time.Second, func() bool {
		for _, s := range viewers(t, devs[0]) {
			if s["profileName"] == "Carl" {
				return false
			}
		}
		return true
	})
	list = viewers(t, devs[0])
	require.Len(t, list, 1)
	require.Equal(t, "Ana", list[0]["profileName"])
}

func setupDummyDebrid(t *testing.T, base string) {
	t.Helper()
	fixture := os.Getenv("SEANIME_E2E_FIXTURE")
	require.NotEmpty(t, fixture, "SEANIME_E2E_FIXTURE (an MKV file on the server)")
	code, res := call(t, base, http.MethodPatch, "/api/v1/debrid/dummy/settings", "e2e-admin", "", map[string]any{"settings": map[string]any{
		"enabled": true, "profileName": "e2e", "fallbackFilePath": fixture, "cached": true, "readyDelayMs": 1500, "progressIntervalMs": 250,
	}})
	require.Equal(t, 200, code, "dummy settings (is [experimental] dummyDebrid on?): %v", res)
	code, res = call(t, base, http.MethodPatch, "/api/v1/debrid/settings", "e2e-admin", "", map[string]any{"settings": map[string]any{
		"enabled": true, "provider": "dummy",
	}})
	require.Equal(t, 200, code, "debrid settings: %v", res)
}
