package trackprobe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"seanime/internal/matroska"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"spa": "es", "es": "es", "es-419": "es", "es-ES": "es", "ES_mx": "es", "Español": "es",
		"castellano": "es", "Latino": "es", "eng": "en", "en-US": "en", "fre": "fr", "fra": "fr",
		"fr-CA": "fr", "por": "pt", "pt-BR": "pt", "jpn": "ja", "jp": "ja", "ger": "de", "deu": "de",
		"chi": "zh", "zh-Hant": "zh", "und": "", "": "", " ": "", "tlh": "tlh",
		// ISO 639-2 "lat" is Latin, not "Latino".
		"lat": "lat",
	}
	for in, want := range cases {
		assert.Equal(t, want, Normalize(in), in)
	}
}

func TestTrackLanguages(t *testing.T) {
	assert.Equal(t, []string{"es"}, TrackLanguages("spa", "es-419", "Spanish (Latin America)"))
	assert.Equal(t, []string{"es"}, TrackLanguages("spa", "", ""))
	// Language defaulted to eng by Matroska, the name says Spanish.
	assert.Equal(t, []string{"en", "es"}, TrackLanguages("eng", "", "Español (Latino)"))
	// IETF wins over Language.
	assert.Equal(t, []string{"pt"}, TrackLanguages("eng", "pt-BR", ""))
	assert.Equal(t, []string{"es"}, TrackLanguages("und", "", "Castellano"))
	// Codes are not looked for inside names ("it" in "Signs (it)" stays out).
	assert.Equal(t, []string{"en"}, TrackLanguages("eng", "", "Signs it de"))
	assert.Nil(t, TrackLanguages("und", "", ""))
}

func TestParseList(t *testing.T) {
	assert.Equal(t, []string{"es", "en"}, ParseList("spa, es, spanish, eng, en, english"))
	assert.Equal(t, []string{"ja"}, ParseList("jpn, jp, ja, japanese"))
	assert.Empty(t, ParseList(" , "))
	assert.Equal(t, "spa", Code3("es"))
	assert.Equal(t, "fre", Code3("fr"))
	assert.Equal(t, "eng", Code3("en"))
}

// rangeServer serves a file with Range support and counts what it sent.
type rangeServer struct {
	*httptest.Server
	requests atomic.Int32
	bytes    atomic.Int64
}

type countingWriter struct {
	http.ResponseWriter
	n *atomic.Int64
}

func (w countingWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.n.Add(int64(n))
	return n, err
}

func serveBytes(t *testing.T, name string, data []byte) *rangeServer {
	t.Helper()
	rs := &rangeServer{}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.requests.Add(1)
		http.ServeContent(countingWriter{w, &rs.bytes}, r, name, time.Time{}, bytes.NewReader(data))
	}))
	t.Cleanup(rs.Close)
	return rs
}

func serveFixture(t *testing.T, name string) *rangeServer {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return serveBytes(t, name, data)
}

func subtitleLangs(tr *Tracks) []string {
	var out []string
	for _, s := range tr.Subtitles {
		out = append(out, strings.Join(s.Languages, "+"))
	}
	return out
}

func TestProbeURL_MultiSub(t *testing.T) {
	srv := serveFixture(t, "multisub.mkv")
	tr, stats, err := ProbeURL(context.Background(), srv.URL+"/multisub.mkv?token=secret", Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"en", "es", "es", "pt", "fr", "de", "it", "ar", "ru"}, subtitleLangs(tr))
	require.Len(t, tr.Audio, 1)
	assert.Equal(t, []string{"ja"}, tr.Audio[0].Languages)
	assert.Equal(t, "Spanish (Latin America)", tr.Subtitles[1].Name)
	assert.LessOrEqual(t, stats.Bytes, DefaultMaxBytes)
	assert.LessOrEqual(t, srv.bytes.Load(), DefaultMaxBytes)
	assert.Equal(t, "en, es, pt, fr, de, it, ar, ru", LanguageSummary(tr.Subtitles))
}

func TestProbeURL_SkipsAttachments(t *testing.T) {
	srv := serveFixture(t, "eng-fre.mkv") // 3 MiB file, mostly a font attachment
	tr, _, err := ProbeURL(context.Background(), srv.URL, Options{})
	require.NoError(t, err)
	assert.Equal(t, []string{"en", "fr"}, subtitleLangs(tr))
	assert.Less(t, srv.bytes.Load(), int64(256<<10))
}

func TestProbeURL_ForcedAndDub(t *testing.T) {
	srv := serveFixture(t, "spa-signs.mkv")
	tr, _, err := ProbeURL(context.Background(), srv.URL, Options{})
	require.NoError(t, err)
	require.Len(t, tr.Subtitles, 2)
	assert.False(t, tr.Subtitles[0].Forced)
	assert.True(t, tr.Subtitles[1].Forced)
	assert.Equal(t, "Signs & Songs", tr.Subtitles[1].Name)

	srv = serveFixture(t, "spa-dub.mkv")
	tr, _, err = ProbeURL(context.Background(), srv.URL, Options{})
	require.NoError(t, err)
	require.Len(t, tr.Audio, 2)
	assert.Contains(t, tr.Audio[1].Languages, "es")
}

func TestProbeURL_NotMatroska(t *testing.T) {
	srv := serveFixture(t, "video.mp4")
	tr, _, err := ProbeURL(context.Background(), srv.URL, Options{})
	require.NoError(t, err)
	assert.True(t, tr.NotMatroska)
}

func TestProbeURL_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, _, err := ProbeURL(context.Background(), srv.URL, Options{})
	assert.ErrorIs(t, err, ErrRateLimited)
}

func TestProbeURL_Budget(t *testing.T) {
	srv := serveFixture(t, "multisub.mkv") // Tracks is ~400 KiB
	_, stats, err := ProbeURL(context.Background(), srv.URL, Options{MaxBytes: 128 << 10})
	assert.ErrorIs(t, err, ErrBudgetExceeded)
	assert.LessOrEqual(t, stats.Bytes, int64(128<<10))
}

func TestProbeURL_ErrorsHaveNoURL(t *testing.T) {
	_, _, err := ProbeURL(context.Background(), "http://127.0.0.1:1/file.mkv?token=supersecret", Options{})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecret")
	assert.NotContains(t, err.Error(), "127.0.0.1:1/file")
}

func TestProbeURL_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err := ProbeURL(ctx, srv.URL, Options{})
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "%v", err)
}

// EBML helpers for a synthetic file whose Tracks come after the first Cluster.
func vsize(n int) []byte {
	return []byte{0x01, byte(n >> 48), byte(n >> 40), byte(n >> 32), byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
}

func element(id []byte, data []byte) []byte {
	out := append([]byte{}, id...)
	out = append(out, vsize(len(data))...)
	return append(out, data...)
}

func TestProbeReader_FollowsSeekHead(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "spa-signs.mkv"))
	require.NoError(t, err)
	// Copy the EBML header and the Tracks element out of a real file.
	tracksID := []byte{0x16, 0x54, 0xAE, 0x6B}
	segID := []byte{0x18, 0x53, 0x80, 0x67}
	ebml, tracksRaw := splitFixture(t, data)
	tr0, err := ProbeReader(bytes.NewReader(data))
	require.NoError(t, err)

	cluster := element([]byte{0x1F, 0x43, 0xB6, 0x75}, make([]byte, 300<<10))
	seekEntry := func(pos int) []byte {
		return element([]byte{0x4D, 0xBB}, append(
			element([]byte{0x53, 0xAB}, tracksID),
			element([]byte{0x53, 0xAC}, []byte{byte(pos >> 24), byte(pos >> 16), byte(pos >> 8), byte(pos)})...))
	}
	seekHeadLen := len(element([]byte{0x11, 0x4D, 0x9B, 0x74}, seekEntry(0)))
	tracksPos := seekHeadLen + len(cluster) // relative to the segment data
	seekHead := element([]byte{0x11, 0x4D, 0x9B, 0x74}, seekEntry(tracksPos))

	var file bytes.Buffer
	file.Write(ebml)
	file.Write(segID)
	file.Write([]byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	file.Write(seekHead)
	file.Write(cluster)
	file.Write(tracksRaw)

	srv := serveBytes(t, "late.mkv", file.Bytes())
	tr, _, err := ProbeURL(context.Background(), srv.URL, Options{})
	require.NoError(t, err)
	assert.Equal(t, subtitleLangs(tr0), subtitleLangs(tr))
	assert.Less(t, srv.bytes.Load(), int64(200<<10), "the cluster must be skipped")

	// Without the SeekHead, Tracks after a Cluster is not found.
	var noSeek bytes.Buffer
	noSeek.Write(ebml)
	noSeek.Write(segID)
	noSeek.Write([]byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	noSeek.Write(cluster)
	noSeek.Write(tracksRaw)
	_, err = ProbeReader(bytes.NewReader(noSeek.Bytes()))
	assert.ErrorIs(t, err, ErrTracksNotFound)
}

// splitFixture returns the EBML header and the Tracks element of an MKV.
func splitFixture(t *testing.T, data []byte) (ebml, tracks []byte) {
	t.Helper()
	er := matroska.NewEBMLReader(bytes.NewReader(data))
	_, size, err := er.ReadElementHeader()
	require.NoError(t, err)
	_, err = er.Seek(er.Position()+int64(size), io.SeekStart)
	require.NoError(t, err)
	ebml = data[:er.Position()]
	id, _, err := er.ReadElementHeader()
	require.NoError(t, err)
	require.Equal(t, uint32(matroska.IDSegment), id)
	for {
		start := er.Position()
		id, size, err := er.ReadElementHeader()
		require.NoError(t, err)
		end := er.Position() + int64(size)
		if id == matroska.IDTracks {
			return ebml, data[start:end]
		}
		_, err = er.Seek(end, io.SeekStart)
		require.NoError(t, err)
	}
}

func TestCache(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir, time.Hour)
	key := Key("torbox", "ABCDEF", "/Show/Show - 01.mkv")
	_, ok := c.Get(key)
	assert.False(t, ok)
	require.NoError(t, c.Put(key, &Tracks{Subtitles: []Track{{Languages: []string{"es"}}}}))
	got, ok := c.Get(key)
	require.True(t, ok)
	assert.Equal(t, []string{"es"}, got.Subtitles[0].Languages)
	// Same key, other case of hash.
	_, ok = c.Get(Key("TorBox", "abcdef", "/Show/Show - 01.mkv"))
	assert.True(t, ok)
	// Expired.
	c.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	_, ok = c.Get(key)
	assert.False(t, ok)
	// No dir: no-op.
	var nc *Cache
	_, ok = nc.Get(key)
	assert.False(t, ok)
	assert.NoError(t, NewCache("", 0).Put(key, &Tracks{}))
}
