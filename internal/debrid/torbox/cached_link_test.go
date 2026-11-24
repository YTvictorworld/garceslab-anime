package torbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"seanime/internal/debrid/debrid"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/mo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTorBox is a tiny TorBox API: one account list, cached hashes, 429 switch.
type fakeTorBox struct {
	mu        sync.Mutex
	list      []map[string]any
	cached    map[string]bool
	rate      bool
	created   []string
	deleted   []int
	nextID    int
	onlyCache []string
}

func (f *fakeTorBox) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.rate {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		ok := func(data any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
		}
		switch r.URL.Path {
		case "/torrents/mylist":
			if id := r.URL.Query().Get("id"); id != "" {
				for _, tr := range f.list {
					if jsonInt(tr["id"]) == atoi(id) {
						ok(tr)
						return
					}
				}
				http.NotFound(w, r)
				return
			}
			ok(f.list)
		case "/torrents/createtorrent":
			require.NoError(t, r.ParseMultipartForm(1<<20))
			f.onlyCache = append(f.onlyCache, r.FormValue("add_only_if_cached"))
			magnet := r.FormValue("magnet")
			hash := strings.TrimPrefix(magnet, "magnet:?xt=urn:btih:")
			if !f.cached[hash] {
				_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "detail": "not cached"})
				return
			}
			f.nextID++
			f.created = append(f.created, hash)
			f.list = append(f.list, torrentJSON(f.nextID, hash))
			ok(map[string]any{"torrent_id": f.nextID})
		case "/torrents/requestdl":
			if r.URL.Query().Get("token") != "apikey" || r.URL.Query().Get("file_id") != "7" {
				http.Error(w, "bad", http.StatusBadRequest)
				return
			}
			ok("https://cdn.example/dl/ep01.mkv?token=signed")
		case "/torrents/controltorrent":
			var b struct {
				ID int `json:"torrent_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			f.deleted = append(f.deleted, b.ID)
			ok(nil)
		default:
			http.NotFound(w, r)
		}
	})
}

func torrentJSON(id int, hash string) map[string]any {
	return map[string]any{
		"id": id, "hash": hash, "download_present": true, "download_finished": true,
		"files": []map[string]any{{"id": 7, "name": "Show/ep01.mkv", "short_name": "ep01.mkv"}},
	}
}

func jsonInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return -1
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func newTestTorBox(t *testing.T, f *fakeTorBox) *TorBox {
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	logger := zerolog.Nop()
	tb := NewTorBox(&logger).(*TorBox)
	tb.baseUrl = srv.URL
	tb.apiKey = mo.Some("apikey")
	return tb
}

func TestCachedFileLink_AddsOnlyIfCachedAndRemovesOnRelease(t *testing.T) {
	f := &fakeTorBox{cached: map[string]bool{"abc": true}}
	tb := newTestTorBox(t, f)

	link, err := tb.CachedFileLink(context.Background(), debrid.CachedFileLinkOptions{InfoHash: "abc", FileId: "ep01.mkv"})
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example/dl/ep01.mkv?token=signed", link.URL)
	assert.Equal(t, []string{"true"}, f.onlyCache)
	link.Release(false)
	link.Release(false) // once
	assert.Eventually(t, func() bool { return f.deletedIDs() == "1" }, time.Second, 10*time.Millisecond)

	// Kept: not removed.
	f.cached["def"] = true
	link, err = tb.CachedFileLink(context.Background(), debrid.CachedFileLinkOptions{InfoHash: "def", FileId: "ep01.mkv"})
	require.NoError(t, err)
	link.Release(true)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, "1", f.deletedIDs())
}

func (f *fakeTorBox) deletedIDs() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, id := range f.deleted {
		ids = append(ids, strconv.Itoa(id))
	}
	return strings.Join(ids, ",")
}

func TestCachedFileLink_ExistingTorrentIsNeverRemoved(t *testing.T) {
	f := &fakeTorBox{list: []map[string]any{torrentJSON(42, "abc")}}
	tb := newTestTorBox(t, f)
	link, err := tb.CachedFileLink(context.Background(), debrid.CachedFileLinkOptions{InfoHash: "ABC", FileId: "ep01.mkv"})
	require.NoError(t, err)
	link.Release(false)
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, f.created)
	assert.Empty(t, f.deletedIDs())
}

func TestCachedFileLink_NotCached(t *testing.T) {
	f := &fakeTorBox{cached: map[string]bool{}}
	tb := newTestTorBox(t, f)
	_, err := tb.CachedFileLink(context.Background(), debrid.CachedFileLinkOptions{InfoHash: "zzz", FileId: "ep01.mkv"})
	assert.ErrorIs(t, err, debrid.ErrNotCached)
	assert.Empty(t, f.created)
}

func TestCachedFileLink_RateLimited(t *testing.T) {
	f := &fakeTorBox{rate: true}
	tb := newTestTorBox(t, f)
	_, err := tb.CachedFileLink(context.Background(), debrid.CachedFileLinkOptions{InfoHash: "abc", FileId: "ep01.mkv"})
	assert.ErrorIs(t, err, debrid.ErrRateLimited)
}

func TestCachedFileLink_ErrorsHaveNoToken(t *testing.T) {
	logger := zerolog.Nop()
	tb := NewTorBox(&logger).(*TorBox)
	tb.baseUrl = "http://127.0.0.1:1"
	tb.apiKey = mo.Some("supersecret")
	_, err := tb.CachedFileLink(context.Background(), debrid.CachedFileLinkOptions{InfoHash: "abc", FileId: "ep01.mkv"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecret")
	assert.NotContains(t, err.Error(), "/torrents/") // the host may appear, the request URL not
}
