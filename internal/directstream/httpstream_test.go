package directstream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"seanime/internal/mkvparser"
	"seanime/internal/player"
	"seanime/internal/util"
	"seanime/internal/util/result"

	"github.com/samber/mo"
	"github.com/stretchr/testify/require"
)

func newHTTPStreamTestManager() *Manager {
	return &Manager{
		Logger:      util.NewLogger(),
		playbackCtx: context.Background(),
	}
}

func newTestNakamaStream(manager *Manager, streamURL string, token string) *Nakama {
	return &Nakama{
		httpBaseStream: httpBaseStream{
			streamUrl: streamURL,
			requestHeaders: http.Header{
				"X-Seanime-Nakama-Token": []string{token},
			},
			headResponseHeaders: http.Header{
				"X-Seanime-Nakama-Token": []string{token},
			},
			BaseStream: BaseStream{
				manager:               manager,
				logger:                manager.Logger,
				subtitleEventCache:    result.NewMap[string, *mkvparser.SubtitleEvent](),
				activeSubtitleStreams: result.NewMap[string, *SubtitleStream](),
			},
		},
	}
}

func TestNakamaLoadContentTypeUsesSharedRequestHeaders(t *testing.T) {
	const token = "nakama-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, token, r.Header.Get("X-Seanime-Nakama-Token"))
		require.Equal(t, http.MethodHead, r.Method)

		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "6")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	manager := newHTTPStreamTestManager()
	stream := newTestNakamaStream(manager, server.URL+"/video.mp4", token)

	require.Equal(t, "video/mp4", stream.LoadContentType())
	require.Equal(t, int64(6), stream.contentLength)
}

func TestNakamaGetStreamHandlerPreservesHeadResponseToken(t *testing.T) {
	const token = "nakama-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, token, r.Header.Get("X-Seanime-Nakama-Token"))
		require.Equal(t, http.MethodHead, r.Method)

		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "6")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	manager := newHTTPStreamTestManager()
	stream := newTestNakamaStream(manager, server.URL+"/video.mp4", token)
	stream.playbackInfo = &player.PlaybackInfo{MkvMetadataParser: mo.None[*mkvparser.MetadataParser]()}

	require.Equal(t, "video/mp4", stream.LoadContentType())

	req := httptest.NewRequest(http.MethodHead, "/api/v1/directstream/stream", nil)
	rec := httptest.NewRecorder()

	stream.GetStreamHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, token, rec.Header().Get("X-Seanime-Nakama-Token"))
	require.Equal(t, "video/mp4", rec.Header().Get("Content-Type"))
	require.Equal(t, "6", rec.Header().Get("Content-Length"))
}

func TestNakamaGetStreamHandlerProxiesWithSharedRequestHeaders(t *testing.T) {
	const token = "nakama-secret"
	payload := []byte("abcdef")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, token, r.Header.Get("X-Seanime-Nakama-Token"))

		switch r.Method {
		case http.MethodHead:
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			require.Equal(t, "bytes=0-3", r.Header.Get("Range"))
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Content-Length", "4")
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-3/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(payload[:4])
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	manager := newHTTPStreamTestManager()
	stream := newTestNakamaStream(manager, server.URL+"/video.mp4", token)
	stream.playbackInfo = &player.PlaybackInfo{MkvMetadataParser: mo.None[*mkvparser.MetadataParser]()}

	require.Equal(t, "video/mp4", stream.LoadContentType())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/directstream/stream", nil)
	req.Header.Set("Range", "bytes=0-3")
	rec := httptest.NewRecorder()

	stream.GetStreamHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusPartialContent, rec.Code)
	require.Equal(t, "abcd", rec.Body.String())
	require.Equal(t, "video/mp4", rec.Header().Get("Content-Type"))
}

func TestNakamaMetadataReaderCarriesHeadersAcrossRangeRequests(t *testing.T) {
	const token = "nakama-secret"
	payload := []byte("abcdef")
	requestCount := atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		require.Equal(t, token, r.Header.Get("X-Seanime-Nakama-Token"))

		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			return
		}

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
			return
		}

		startStr := strings.TrimPrefix(rangeHeader, "bytes=")
		startStr = strings.TrimSuffix(startStr, "-")
		start, err := strconv.Atoi(startStr)
		require.NoError(t, err)

		w.Header().Set("Content-Length", strconv.Itoa(len(payload)-start))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start:])
	}))
	defer server.Close()

	manager := newHTTPStreamTestManager()
	stream := newTestNakamaStream(manager, server.URL+"/video.mkv", token)

	reader, err := stream.newMetadataReader()
	require.NoError(t, err)
	defer reader.Close()
	require.Zero(t, requestCount.Load())

	first := make([]byte, 3)
	_, err = io.ReadFull(reader, first)
	require.NoError(t, err)
	require.Equal(t, "abc", string(first))

	_, err = reader.Seek(2, io.SeekStart)
	require.NoError(t, err)

	second := make([]byte, 3)
	_, err = io.ReadFull(reader, second)
	require.NoError(t, err)
	require.Equal(t, "cde", string(second))
}

func TestNakamaMetadataReaderStartsAtRequestedRange(t *testing.T) {
	const token = "nakama-secret"
	payload := []byte("abcdefgh")
	ranges := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, token, r.Header.Get("X-Seanime-Nakama-Token"))
		ranges <- r.Header.Get("Range")
		w.Header().Set("Content-Range", "bytes 4-7/8")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[4:])
	}))
	defer server.Close()

	manager := newHTTPStreamTestManager()
	stream := newTestNakamaStream(manager, server.URL+"/video.mkv", token)
	reader, err := stream.newMetadataReader()
	require.NoError(t, err)
	defer reader.Close()

	_, err = reader.Seek(4, io.SeekStart)
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(reader, buf)
	require.NoError(t, err)
	require.Equal(t, "efgh", string(buf))
	require.Equal(t, "bytes=4-", <-ranges)
}

func TestUrlStreamGetStreamHandlerServesFullAndOpenEndedRanges(t *testing.T) {
	payload := make([]byte, 256*1024)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if values, ok := r.Header["Range"]; ok && values[0] == "" {
			http.Error(w, "empty Range header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "video/x-matroska")
		http.ServeContent(w, r, "video.mkv", time.Time{}, bytes.NewReader(payload))
	}))
	defer cdn.Close()

	manager := newHTTPStreamTestManager()
	stream := &UrlStream{
		httpBaseStream: httpBaseStream{
			streamUrl: cdn.URL + "/video.mkv",
			BaseStream: BaseStream{
				manager:               manager,
				logger:                manager.Logger,
				subtitleEventCache:    result.NewMap[string, *mkvparser.SubtitleEvent](),
				activeSubtitleStreams: result.NewMap[string, *SubtitleStream](),
			},
		},
	}
	require.Equal(t, "video/x-matroska", stream.LoadContentType())
	defer stream.Close()

	server := httptest.NewServer(stream.GetStreamHandler())
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	size := int64(len(payload))
	tests := []struct {
		name         string
		rangeHeader  string
		start        int64
		status       int
		contentRange string
	}{
		{name: "no range", status: http.StatusOK},
		{name: "open-ended near end", rangeHeader: "bytes=200000-", start: 200000, status: http.StatusPartialContent, contentRange: fmt.Sprintf("bytes 200000-%d/%d", size-1, size)},
		{name: "open-ended from start", rangeHeader: "bytes=0-", status: http.StatusPartialContent, contentRange: fmt.Sprintf("bytes 0-%d/%d", size-1, size)},
		{name: "open-ended middle", rangeHeader: "bytes=1000-", start: 1000, status: http.StatusPartialContent, contentRange: fmt.Sprintf("bytes 1000-%d/%d", size-1, size)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/directstream/stream", nil)
			require.NoError(t, err)
			if tt.rangeHeader != "" {
				req.Header.Set("Range", tt.rangeHeader)
			}

			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tt.status, resp.StatusCode)
			require.Equal(t, tt.contentRange, resp.Header.Get("Content-Range"))
			require.Equal(t, strconv.Itoa(len(payload[tt.start:])), resp.Header.Get("Content-Length"))
			require.True(t, bytes.Equal(payload[tt.start:], body), "body mismatch")
		})
	}
}
