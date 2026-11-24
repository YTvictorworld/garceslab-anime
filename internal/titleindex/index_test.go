package titleindex

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"Bleach":                        "bleach",
		"  BLEACH:  Sennen Kessen-hen ": "bleach sennen kessen hen",
		"Pokémon":                       "pokemon",
		"JoJo's Bizarre Adventure":      "jojos bizarre adventure",
		"JoJo’s":                        "jojos",
		"Re:Zero kara":                  "re zero kara",
		"Steins;Gate 0":                 "steins gate 0",
		"Ｆｕｌｌ ｗｉｄｔｈ":                    "full width",
		"Hunter × Hunter (2011)":        "hunter hunter 2011",
		"Ōkami-san":                     "okami san",
		"!NVADE SHOW!":                  "nvade show",
		"...":                           "",
		"":                              "",
		"進撃の巨人":                         Normalize("進撃の巨人"), // stable for non-Latin scripts
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if Normalize("進撃の巨人") == "" {
		t.Error("Japanese titles must not normalize to nothing")
	}
}

func TestAnilistID(t *testing.T) {
	if id := AnilistID([]string{"https://myanimelist.net/anime/269", "https://anilist.co/anime/269"}); id != 269 {
		t.Errorf("got %d", id)
	}
	if id := AnilistID([]string{"https://kitsu.app/anime/244"}); id != 0 {
		t.Errorf("got %d, want 0", id)
	}
	if id := AnilistID([]string{"https://anilist.co/anime/abc", "https://anilist.co/anime/5/"}); id != 5 {
		t.Errorf("got %d, want 5", id)
	}
}

const sampleDB = `{
  "$schema": "x", "license": {"name": "ODbL"}, "repository": "r", "lastUpdate": "2026-07-04",
  "data": [
    {"sources": ["https://anilist.co/anime/269", "https://myanimelist.net/anime/269", "https://kitsu.app/anime/244", "https://anidb.net/anime/2369"],
     "title": "Bleach", "type": "TV", "episodes": 366, "animeSeason": {"season": "FALL", "year": 2004},
     "picture": "https://cdn.example/bleach.jpg", "thumbnail": "https://cdn.example/bleacht.jpg",
     "synonyms": ["BLEACH", "Bleach: Sennen Kessen", "ブリーチ"], "score": {"arithmeticMean": 7.9}},
    {"sources": ["https://anilist.co/anime/116674", "https://myanimelist.net/anime/41467", "https://kitsu.app/anime/1"],
     "title": "Bleach: Sennen Kessen-hen", "type": "TV", "episodes": 13, "animeSeason": {"year": 2022},
     "synonyms": ["Bleach: Thousand-Year Blood War"], "score": {"arithmeticMean": 9.0}},
    {"sources": ["https://anilist.co/anime/1020", "https://myanimelist.net/anime/1020"],
     "title": "Bleach Movie 1: Memories of Nobody", "type": "MOVIE", "animeSeason": {"year": 2006}, "synonyms": []},
    {"sources": ["https://anilist.co/anime/34", "https://myanimelist.net/anime/34"],
     "title": "Blend S", "type": "TV", "animeSeason": {"year": 2017}, "synonyms": ["Blend-S"]},
    {"sources": ["https://anilist.co/anime/99", "https://myanimelist.net/anime/99", "https://kitsu.app/anime/99"],
     "title": "Kimi to Bleed", "type": "TV", "synonyms": []},
    {"sources": ["https://anilist.co/anime/77"],
     "title": "Hyperblend", "type": "OVA", "synonyms": [],
     "picture": "https://raw.githubusercontent.com/manami-project/anime-offline-database/master/pics/no_pic.png"},
    {"sources": ["https://myanimelist.net/anime/5"],
     "title": "Bleach Not On AniList", "type": "TV", "synonyms": []},
    {"sources": ["https://anilist.co/anime/269"],
     "title": "Bleach duplicate", "type": "TV", "synonyms": []},
    {"sources": ["https://anilist.co/anime/16498", "https://myanimelist.net/anime/16498", "https://kitsu.app/anime/7442"],
     "title": "Shingeki no Kyojin", "type": "TV", "animeSeason": {"year": 2013},
     "synonyms": ["Attack on Titan", "AoT", "進撃の巨人"]},
    {"sources": ["https://anilist.co/anime/99147", "https://myanimelist.net/anime/35760", "https://kitsu.app/anime/13569"],
     "title": "Shingeki no Kyojin Season 3", "type": "TV", "animeSeason": {"year": 2018},
     "synonyms": ["Attack on Titan", "Attack on Titan Season 3", "AoT S3", "SnK 3"], "tags": ["action", "drama", "military"]}
  ]
}`

func sampleIndex(t *testing.T) *Index {
	t.Helper()
	ix, err := Parse(strings.NewReader(sampleDB))
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func ids(s []Suggestion) []int {
	out := make([]int, len(s))
	for i, x := range s {
		out[i] = x.MediaID
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParse(t *testing.T) {
	ix := sampleIndex(t)
	if ix.Len() != 8 { // the non-AniList entry and the duplicate id are skipped
		t.Fatalf("Len = %d, want 8", ix.Len())
	}
	if ix.LastUpdate() != "2026-07-04" {
		t.Errorf("LastUpdate = %q", ix.LastUpdate())
	}
	got := ix.Search("bleach", 1)[0]
	want := Suggestion{MediaID: 269, Title: "Bleach", Year: 2004, Format: "TV", Episodes: 366,
		Picture: "https://cdn.example/bleach.jpg", Thumbnail: "https://cdn.example/bleacht.jpg"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if s := ix.Search("hyperblend", 1)[0]; s.Picture != "" || s.Format != "OVA" {
		t.Errorf("placeholder picture should be dropped: %+v", s)
	}
}

func TestSearchRanking(t *testing.T) {
	ix := sampleIndex(t)
	cases := []struct {
		q    string
		want []int
	}{
		// Prefixes first (TV before the movie, then the more popular: more sources, tags
		// and synonyms), then the word prefix ("Kimi to Bleed"), then the substring ("Hyperblend").
		{"ble", []int{269, 116674, 34, 1020, 99, 77}},
		// The exact title beats longer titles with the same prefix.
		{"Bleach", []int{269, 116674, 1020}},
		// A synonym prefix counts like a title prefix, then popularity decides.
		{"BLEACH: s", []int{269, 116674}},
		{"blend", []int{34, 77}},
		// A synonym prefix finds the anime and reports the synonym.
		// Several seasons share the synonym "Attack on Titan": they stay together, ranked by
		// the group's best popularity (season 3 here), and the first season comes first.
		{"attack on", []int{16498, 99147}},
		{"attack on titan s", []int{99147}},
		{"thousand", []int{116674}},
		// Two letters don't match inside words, only at word starts.
		{"le", []int{}},
		{"lend", []int{34, 77}},
		{"ブリ", []int{269}},
		{"", []int{}},
		{"  !!  ", []int{}},
	}
	for _, c := range cases {
		if got := ids(ix.Search(c.q, 10)); !equalInts(got, c.want) {
			t.Errorf("Search(%q) = %v, want %v", c.q, got, c.want)
		}
	}
	if s := ix.Search("attack", 1)[0]; s.Matched != "Attack on Titan" || s.Title != "Shingeki no Kyojin" {
		t.Errorf("matched synonym not reported: %+v", s)
	}
	if s := ix.Search("bleach", 1)[0]; s.Matched != "" {
		t.Errorf("Matched should be empty when the title matched: %+v", s)
	}
	if got := ix.Search("ble", 2); len(got) != 2 {
		t.Errorf("limit not applied: %d", len(got))
	}
	var nilIndex *Index
	if got := nilIndex.Search("ble", 5); got == nil || len(got) != 0 {
		t.Errorf("nil index must return an empty list, got %v", got)
	}
}

func compress(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestManagerRefresh(t *testing.T) {
	body := compress(t, sampleDB)
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "nope", http.StatusBadGateway)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	m := NewManager(Options{Dir: dir, URL: srv.URL, MinEntries: 1})
	if got := m.Search("ble", 5); len(got) != 0 {
		t.Fatalf("empty manager must return no suggestions, got %v", got)
	}
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ids(m.Search("ble", 1)); !equalInts(got, []int{269}) {
		t.Fatalf("got %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("cached file missing: %v", err)
	}

	// A failed download keeps the previous index and file.
	fail = true
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if m.Index().Len() != 8 {
		t.Fatal("previous index lost")
	}

	// A new manager loads the cached file without downloading.
	m2 := NewManager(Options{Dir: dir, URL: srv.URL, MinEntries: 1, MaxAge: time.Hour})
	m2.Start()
	defer m2.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for m2.Index() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m2.Index().Len() != 8 {
		t.Fatal("cached index not loaded")
	}
}

func TestManagerRejectsBrokenDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(compress(t, `{"data": [`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	m := NewManager(Options{Dir: dir, URL: srv.URL, MinEntries: 1})
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("expected a parse error")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); !os.IsNotExist(err) {
		t.Fatal("a broken download must not replace the cached file")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("temporary file left behind: %v", entries)
	}

	// Too few entries is rejected as well.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(compress(t, sampleDB))
	}))
	defer srv2.Close()
	m = NewManager(Options{Dir: dir, URL: srv2.URL})
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("expected the minimum entry check to fail")
	}
}
