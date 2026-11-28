package trackprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Synthetic MKV headers: EBML header, unknown-size Segment and a Tracks
// element, no media at all.

type synTrack struct {
	number  int
	typ     byte   // 1 video, 2 audio, 0x11 subtitle
	codecID string // "" = no CodecID element
	lang    string
	name    string
	forced  bool
}

func uintData(n int) []byte {
	return []byte{byte(n >> 8), byte(n)}
}

func synMKV(tracks ...synTrack) []byte {
	ebml := element([]byte{0x1A, 0x45, 0xDF, 0xA3}, bytes.Join([][]byte{
		element([]byte{0x42, 0x86}, []byte{1}),          // EBMLVersion
		element([]byte{0x42, 0x82}, []byte("matroska")), // DocType
		element([]byte{0x42, 0x87}, []byte{4}),          // DocTypeVersion
		element([]byte{0x42, 0x85}, []byte{2}),          // DocTypeReadVersion
	}, nil))
	var entries []byte
	for _, t := range tracks {
		var e []byte
		e = append(e, element([]byte{0xD7}, uintData(t.number))...)            // TrackNumber
		e = append(e, element([]byte{0x73, 0xC5}, uintData(1000+t.number))...) // TrackUID
		e = append(e, element([]byte{0x83}, []byte{t.typ})...)                 // TrackType
		if t.codecID != "" {
			e = append(e, element([]byte{0x86}, []byte(t.codecID))...)
		}
		if t.lang != "" {
			e = append(e, element([]byte{0x22, 0xB5, 0x9C}, []byte(t.lang))...)
		}
		if t.name != "" {
			e = append(e, element([]byte{0x53, 0x6E}, []byte(t.name))...)
		}
		if t.forced {
			e = append(e, element([]byte{0x55, 0xAA}, []byte{1})...)
		}
		entries = append(entries, element([]byte{0xAE}, e)...)
	}
	var file bytes.Buffer
	file.Write(ebml)
	file.Write([]byte{0x18, 0x53, 0x80, 0x67})
	file.Write([]byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	file.Write(element([]byte{0x16, 0x54, 0xAE, 0x6B}, entries))
	return file.Bytes()
}

var (
	synVideo = synTrack{number: 1, typ: 1, codecID: "V_MPEG4/ISO/AVC", lang: "und"}
	synAudio = synTrack{number: 2, typ: 2, codecID: "A_AAC", lang: "jpn"}
)

func sub(n int, codec, lang string) synTrack {
	return synTrack{number: n, typ: 0x11, codecID: codec, lang: lang}
}

func probeSyn(t *testing.T, tracks ...synTrack) *Tracks {
	t.Helper()
	tr, err := ProbeReader(bytes.NewReader(synMKV(tracks...)))
	require.NoError(t, err)
	require.False(t, tr.NotMatroska)
	return tr
}

type trackView struct {
	Number uint64
	Codec  string
	Kind   Kind
	Lang   string
}

func views(tracks []Track) []trackView {
	var out []trackView
	for _, t := range tracks {
		out = append(out, trackView{t.Number, t.CodecID, t.Kind, TrackLanguage(t)})
	}
	return out
}

func TestProbeReader_Codecs(t *testing.T) {
	cases := []struct {
		name    string
		tracks  []synTrack
		want    []trackView
		summary string
	}{
		{"ass", []synTrack{synVideo, synAudio, sub(3, "S_TEXT/ASS", "eng")},
			[]trackView{{3, "S_TEXT/ASS", KindText, "en"}}, "en:ass"},
		{"srt", []synTrack{synVideo, synAudio, sub(3, "S_TEXT/UTF8", "spa")},
			[]trackView{{3, "S_TEXT/UTF8", KindText, "es"}}, "es:srt"},
		{"pgs", []synTrack{synVideo, synAudio, sub(3, "S_HDMV/PGS", "fre")},
			[]trackView{{3, "S_HDMV/PGS", KindImage, "fr"}}, "fr:pgs"},
		{"vobsub", []synTrack{synVideo, synAudio, sub(3, "S_VOBSUB", "ger")},
			[]trackView{{3, "S_VOBSUB", KindImage, "de"}}, "de:vobsub"},
		{"mixed", []synTrack{synVideo, synAudio,
			sub(3, "S_TEXT/ASS", "eng"), sub(4, "S_TEXT/ASS", "spa"), sub(5, "S_HDMV/PGS", "fre"),
			sub(6, "S_TEXT/WEBVTT", "por"), sub(7, "S_DVBSUB", "ita")},
			[]trackView{
				{3, "S_TEXT/ASS", KindText, "en"}, {4, "S_TEXT/ASS", KindText, "es"},
				{5, "S_HDMV/PGS", KindImage, "fr"}, {6, "S_TEXT/WEBVTT", KindText, "pt"},
				{7, "S_DVBSUB", KindImage, "it"},
			}, "en:ass, es:ass, fr:pgs, pt:webvtt, it:dvbsub"},
		{"missing CodecID", []synTrack{synVideo, synAudio, sub(3, "", "eng")},
			[]trackView{{3, "", KindUnknown, "en"}}, "en:unknown"},
		{"unlisted codec", []synTrack{synVideo, synAudio, sub(3, "S_KATE", "und")},
			[]trackView{{3, "S_KATE", KindUnknown, "und"}}, "und:s_kate"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := probeSyn(t, c.tracks...)
			assert.Equal(t, c.want, views(tr.Subtitles))
			assert.Equal(t, c.summary, CodecSummary(tr.Subtitles))
			require.Len(t, tr.Audio, 1)
			assert.Equal(t, uint64(2), tr.Audio[0].Number)
			assert.Equal(t, "A_AAC", tr.Audio[0].CodecID)
			assert.Empty(t, tr.Audio[0].Kind, "kind is for subtitles only")
			assert.False(t, tr.CodecsUnknown)
		})
	}
}

func TestProbeReader_LanguagesUnchanged(t *testing.T) {
	// The fields auto-select reads are the same as before codecs were added.
	forced := sub(4, "S_TEXT/ASS", "spa")
	forced.forced, forced.name = true, "Signs & Songs"
	named := sub(5, "S_HDMV/PGS", "und")
	named.name = "Español (Latino)"
	tr := probeSyn(t, synVideo, synAudio, sub(3, "S_TEXT/ASS", "eng"), forced, named)
	assert.Equal(t, []string{"en", "es", "es"}, subtitleLangs(tr))
	assert.True(t, tr.Subtitles[1].Forced)
	assert.Equal(t, "Signs & Songs", tr.Subtitles[1].Name)
	assert.Equal(t, "en:ass, es:ass(forced), es:pgs", CodecSummary(tr.Subtitles))
}

func TestProbeURL_FixtureCodecs(t *testing.T) {
	srv := serveFixture(t, "multisub.mkv")
	tr, _, err := ProbeURL(context.Background(), srv.URL, Options{})
	require.NoError(t, err)
	for _, s := range tr.Subtitles {
		assert.Equal(t, "S_TEXT/ASS", s.CodecID)
		assert.Equal(t, KindText, s.Kind)
		assert.NotZero(t, s.Number)
	}
	srv = serveFixture(t, "eng-fre.mkv")
	tr, _, err = ProbeURL(context.Background(), srv.URL, Options{})
	require.NoError(t, err)
	assert.Equal(t, "en:srt, fr:srt", CodecSummary(tr.Subtitles))
	assert.Equal(t, "ja:aac", CodecSummary(tr.Audio))
}

func TestSubtitleKindAndLabel(t *testing.T) {
	kinds := map[string]Kind{
		"S_TEXT/ASS": KindText, "S_TEXT/SSA": KindText, "S_TEXT/UTF8": KindText, "S_TEXT/WEBVTT": KindText,
		"s_text/ass": KindText, "S_HDMV/TEXTST": KindText, "S_HDMV/PGS": KindImage, "S_VOBSUB": KindImage,
		"S_DVBSUB": KindImage, "": KindUnknown, "S_KATE": KindUnknown,
	}
	for id, want := range kinds {
		assert.Equal(t, want, SubtitleKind(id), id)
	}
	labels := map[string]string{
		"S_TEXT/ASS": "ass", "S_TEXT/UTF8": "srt", "S_HDMV/PGS": "pgs", "S_VOBSUB": "vobsub",
		"A_AAC": "aac", "A_AAC/MPEG4/LC": "aac", "A_OPUS": "opus", "A_FLAC": "flac", "": "unknown",
	}
	for id, want := range labels {
		assert.Equal(t, want, CodecLabel(id), id)
	}
	assert.Equal(t, "none", CodecSummary(nil))
}

// v1 entry as the previous build wrote it (no "version", no codecs).
const v1Entry = `{"key":%q,"probedAt":%d,"tracks":{"audio":[{"languages":["ja"]}],` +
	`"subtitles":[{"languages":["en"],"name":"English"},{"languages":["es"],"name":"Signs","forced":true}]}}`

func writeRawEntry(t *testing.T, c *Cache, key, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(c.dir, 0o700))
	require.NoError(t, os.WriteFile(c.path(key), []byte(body), 0o600))
}

func TestCache_V1EntryKeptWithCodecsUnknown(t *testing.T) {
	c := NewCache(t.TempDir(), time.Hour)
	key := Key("torbox", "abc", "/Show/Show - 01.mkv")
	writeRawEntry(t, c, key, fmt.Sprintf(v1Entry, key, time.Now().Unix()))

	got, ok := c.Get(key)
	require.True(t, ok, "a v1 entry is still a hit: no new probe for auto-select")
	assert.True(t, got.CodecsUnknown)
	assert.Equal(t, []string{"en", "es"}, subtitleLangs(got))
	assert.True(t, got.Subtitles[1].Forced)
	assert.Equal(t, "English", got.Subtitles[0].Name)
	assert.Empty(t, got.Subtitles[0].CodecID)
	assert.Empty(t, got.Subtitles[0].Kind)

	// Writing it back keeps it a v1 entry (it must not pass for v2).
	require.NoError(t, c.Put(key, got))
	e := readEntry(t, c, key)
	assert.Equal(t, 1, e.version())

	// A new probe (v2) replaces it.
	fresh := probeSyn(t, synVideo, synAudio, sub(3, "S_TEXT/ASS", "eng"))
	require.NoError(t, c.Put(key, fresh))
	e = readEntry(t, c, key)
	assert.Equal(t, CacheVersion, e.version())
	got, ok = c.Get(key)
	require.True(t, ok)
	assert.False(t, got.CodecsUnknown)
	assert.Equal(t, "S_TEXT/ASS", got.Subtitles[0].CodecID)
	assert.Equal(t, KindText, got.Subtitles[0].Kind)
	assert.Equal(t, uint64(3), got.Subtitles[0].Number)
}

func TestCache_V2RoundTrip(t *testing.T) {
	c := NewCache(t.TempDir(), time.Hour)
	key := Key("torbox", "abc", "/Show/Show - 01.mkv")
	tr := probeSyn(t, synVideo, synAudio, sub(3, "S_HDMV/PGS", "eng"), sub(4, "", "spa"))
	require.NoError(t, c.Put(key, tr))
	got, ok := c.Get(key)
	require.True(t, ok)
	assert.Equal(t, views(tr.Subtitles), views(got.Subtitles))
	assert.Equal(t, tr.Audio, got.Audio)
	raw, err := os.ReadFile(c.path(key))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"version":2`)
	assert.NotContains(t, string(raw), "CodecsUnknown")
}

func readEntry(t *testing.T, c *Cache, key string) cacheEntry {
	t.Helper()
	b, err := os.ReadFile(c.path(key))
	require.NoError(t, err)
	var e cacheEntry
	require.NoError(t, json.Unmarshal(b, &e))
	return e
}

func TestCollectStats(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir, time.Hour)
	put := func(file string, tr *Tracks) {
		require.NoError(t, c.Put(Key("torbox", "hash-"+file, "/Show/"+file), tr))
	}
	put("ass.mkv", probeSyn(t, synVideo, synAudio, sub(3, "S_TEXT/ASS", "eng"), sub(4, "S_TEXT/ASS", "spa")))
	put("srt.mkv", probeSyn(t, synVideo, synAudio, sub(3, "S_TEXT/UTF8", "eng")))
	put("mixed.mkv", probeSyn(t, synVideo, synAudio, sub(3, "S_HDMV/PGS", "eng"), sub(4, "S_TEXT/ASS", "fre")))
	put("pgs.mkv", probeSyn(t, synVideo, synAudio, sub(3, "S_HDMV/PGS", "eng"), sub(4, "S_HDMV/PGS", "spa")))
	put("vobsub.mkv", probeSyn(t, synVideo, synAudio, sub(3, "S_VOBSUB", "eng")))
	put("nosub.mkv", probeSyn(t, synVideo, synAudio))
	put("nocodec.mkv", probeSyn(t, synVideo, synAudio, sub(3, "", "eng"), sub(4, "S_HDMV/PGS", "eng")))
	put("video.mp4", &Tracks{NotMatroska: true})
	v1 := Key("torbox", "hash-old", "/Show/old.mkv")
	writeRawEntry(t, c, v1, fmt.Sprintf(v1Entry, v1, time.Now().Unix()))
	v1none := Key("torbox", "hash-old2", "/Show/old-nosub.mkv")
	writeRawEntry(t, c, v1none, fmt.Sprintf(`{"key":%q,"probedAt":%d,"tracks":{"audio":[{"languages":["ja"]}]}}`, v1none, time.Now().Unix()))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.json.tmp"), []byte("{"), 0o600))

	st, err := CollectStats(dir)
	require.NoError(t, err)
	assert.Equal(t, 10, st.Files)
	assert.Equal(t, 1, st.Unreadable)
	assert.Equal(t, 1, st.NotMatroska)
	assert.Equal(t, 3, st.WithText, "ass, srt, mixed")
	assert.Equal(t, 2, st.ImageOnly, "pgs, vobsub")
	assert.Equal(t, 2, st.NoSubtitles, "nosub + the v1 entry without subtitles")
	assert.Equal(t, 1, st.UnknownCodec, "image + missing CodecID")
	assert.Equal(t, 1, st.CodecNotRecorded)
	assert.Equal(t, st.Files-st.NotMatroska, st.WithText+st.ImageOnly+st.NoSubtitles+st.UnknownCodec+st.CodecNotRecorded)

	assert.Equal(t, map[string]CodecCount{
		"S_TEXT/ASS":  {Tracks: 3, Files: 2},
		"S_TEXT/UTF8": {Tracks: 1, Files: 1},
		"S_HDMV/PGS":  {Tracks: 4, Files: 3},
		"S_VOBSUB":    {Tracks: 1, Files: 1},
		"":            {Tracks: 1, Files: 1},
	}, st.SubtitleCodecs)
	assert.Equal(t, map[string]CodecCount{"A_AAC": {Tracks: 7, Files: 7}}, st.AudioCodecs)
	assert.Equal(t, map[string]map[Kind]int{
		"en": {KindText: 2, KindImage: 4, KindUnknown: 1},
		"es": {KindText: 1, KindImage: 1},
		"fr": {KindText: 1},
	}, st.LanguageKinds)

	require.Len(t, st.ImageOnlyFiles, 2)
	assert.Equal(t, ProbedFile{Provider: "torbox", InfoHash: "hash-pgs.mkv", File: "/Show/pgs.mkv", Subtitles: "en:pgs, es:pgs"}, st.ImageOnlyFiles[0])
	assert.Equal(t, "/Show/vobsub.mkv", st.ImageOnlyFiles[1].File)

	var out bytes.Buffer
	require.NoError(t, st.WriteText(&out))
	text := out.String()
	assert.Contains(t, text, "Probed files:")
	assert.Contains(t, text, "S_HDMV/PGS")
	assert.Contains(t, text, "(no CodecID)")
	assert.Contains(t, text, "Files with image subtitles only (2):")
	assert.Contains(t, text, "/Show/pgs.mkv [torbox hash-pgs] en:pgs, es:pgs")

	// The report never writes to the cache.
	before, _ := filepath.Glob(filepath.Join(dir, "*"))
	_, err = CollectStats(dir)
	require.NoError(t, err)
	after, _ := filepath.Glob(filepath.Join(dir, "*"))
	assert.Equal(t, before, after)
}

func TestRunStatsCommand(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(filepath.Join(dir, "track-probe"), time.Hour)
	require.NoError(t, c.Put(Key("torbox", "h", "/a.mkv"), probeSyn(t, synVideo, sub(2, "S_HDMV/PGS", "eng"))))

	var out, errOut bytes.Buffer
	code := RunStatsCommand([]string{"--datadir", dir, "--json"}, &out, &errOut)
	require.Equal(t, 0, code, errOut.String())
	var st CacheStats
	require.NoError(t, json.Unmarshal(out.Bytes(), &st))
	assert.Equal(t, 1, st.Files)
	assert.Equal(t, 1, st.ImageOnly)

	out.Reset()
	t.Setenv("SEANIME_DATA_DIR", dir)
	require.Equal(t, 0, RunStatsCommand(nil, &out, &errOut), errOut.String())
	assert.Contains(t, out.String(), "image subtitles only:")

	errOut.Reset()
	assert.Equal(t, 1, RunStatsCommand([]string{"--dir", filepath.Join(dir, "missing")}, &out, &errOut))
	assert.Contains(t, errOut.String(), "no track-probe cache")
}
