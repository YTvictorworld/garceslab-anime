package trackprobe

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

// Garceslab Anime: a read-only report over the track-probe cache, to see how
// many releases carry only image subtitles (PGS, VobSub), which would need OCR
// before they could be machine translated. Run inside the image:
//
//	docker exec garceslab-anime /app/seanime trackprobe-stats [--json] [--dir DIR]

// StatsCommand is the first argument that runs the report instead of the server.
const StatsCommand = "trackprobe-stats"

type (
	// CodecCount counts the tracks with one CodecID and the files that have one.
	CodecCount struct {
		Tracks int `json:"tracks"`
		Files  int `json:"files"`
	}

	// ProbedFile is one cached file in the report.
	ProbedFile struct {
		Provider  string `json:"provider"`
		InfoHash  string `json:"infoHash"`
		File      string `json:"file"`
		Subtitles string `json:"subtitles"` // CodecSummary
	}

	// CacheStats is the report.
	CacheStats struct {
		Dir string `json:"dir"`
		// Files is the number of probed files (readable cache entries, expired
		// ones included: a file never changes).
		Files int `json:"files"`
		// Unreadable entries (not JSON, no tracks) are skipped.
		Unreadable  int `json:"unreadable"`
		NotMatroska int `json:"notMatroska"`
		// The subtitle classes of the MKV files; they add up to Files - NotMatroska.
		WithText     int `json:"withText"`     // at least one text subtitle track
		ImageOnly    int `json:"imageOnly"`    // subtitles, all of them images
		NoSubtitles  int `json:"noSubtitles"`  // no subtitle track
		UnknownCodec int `json:"unknownCodec"` // subtitles, none text, some of unknown kind
		// CodecNotRecorded: v1 entries with subtitles (probed before codecs were
		// recorded). Their tracks are left out of the tables below.
		CodecNotRecorded int `json:"codecNotRecorded"`

		SubtitleCodecs map[string]CodecCount `json:"subtitleCodecs"` // by CodecID ("" = missing)
		AudioCodecs    map[string]CodecCount `json:"audioCodecs"`
		// LanguageKinds counts subtitle tracks by language (TrackLanguage) and kind.
		LanguageKinds  map[string]map[Kind]int `json:"languageKinds"`
		ImageOnlyFiles []ProbedFile            `json:"imageOnlyFiles"`
	}
)

// splitKey undoes Key: provider, info hash and file path.
func splitKey(key string) (provider, infoHash, file string) {
	parts := strings.SplitN(key, "|", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// CollectStats reads every entry of the cache directory dir. It never writes.
// A missing directory is an empty report (the command reports it as an error).
func CollectStats(dir string) (*CacheStats, error) {
	st := &CacheStats{
		Dir:            dir,
		SubtitleCodecs: map[string]CodecCount{},
		AudioCodecs:    map[string]CodecCount{},
		LanguageKinds:  map[string]map[Kind]int{},
		ImageOnlyFiles: []ProbedFile{},
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			st.Unreadable++
			continue
		}
		var e cacheEntry
		if json.Unmarshal(b, &e) != nil || e.Tracks == nil {
			st.Unreadable++
			continue
		}
		st.add(e)
	}
	sort.Slice(st.ImageOnlyFiles, func(i, j int) bool {
		a, b := st.ImageOnlyFiles[i], st.ImageOnlyFiles[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.InfoHash < b.InfoHash
	})
	return st, nil
}

func (st *CacheStats) add(e cacheEntry) {
	tr := e.Tracks
	st.Files++
	if tr.NotMatroska {
		st.NotMatroska++
		return
	}
	if e.version() < 2 {
		// No codecs: only "no subtitles" can be told without them.
		if len(tr.Subtitles) == 0 {
			st.NoSubtitles++
		} else {
			st.CodecNotRecorded++
		}
		return
	}

	countCodecs(st.AudioCodecs, tr.Audio)
	countCodecs(st.SubtitleCodecs, tr.Subtitles)
	text, image, unknown := 0, 0, 0
	for _, t := range tr.Subtitles {
		kind := t.Kind
		if kind == "" {
			kind = SubtitleKind(t.CodecID)
		}
		switch kind {
		case KindText:
			text++
		case KindImage:
			image++
		default:
			kind = KindUnknown
			unknown++
		}
		lang := TrackLanguage(t)
		if st.LanguageKinds[lang] == nil {
			st.LanguageKinds[lang] = map[Kind]int{}
		}
		st.LanguageKinds[lang][kind]++
	}
	switch {
	case len(tr.Subtitles) == 0:
		st.NoSubtitles++
	case text > 0:
		st.WithText++
	case unknown == 0 && image > 0:
		st.ImageOnly++
		provider, hash, file := splitKey(e.Key)
		st.ImageOnlyFiles = append(st.ImageOnlyFiles, ProbedFile{
			Provider: provider, InfoHash: hash, File: file, Subtitles: CodecSummary(tr.Subtitles),
		})
	default:
		st.UnknownCodec++
	}
}

func countCodecs(m map[string]CodecCount, tracks []Track) {
	seen := map[string]bool{}
	for _, t := range tracks {
		c := m[t.CodecID]
		c.Tracks++
		if !seen[t.CodecID] {
			seen[t.CodecID] = true
			c.Files++
		}
		m[t.CodecID] = c
	}
}

func pct(n, of int) string {
	if of == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(of))
}

// WriteText prints the report for people.
func (st *CacheStats) WriteText(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	mkv := st.Files - st.NotMatroska
	fmt.Fprintf(tw, "Track-probe cache:\t%s\n", st.Dir)
	fmt.Fprintf(tw, "Probed files:\t%d\n", st.Files)
	if st.Unreadable > 0 {
		fmt.Fprintf(tw, "Unreadable entries (skipped):\t%d\n", st.Unreadable)
	}
	fmt.Fprintf(tw, "Not MKV:\t%d\n", st.NotMatroska)
	fmt.Fprintf(tw, "MKV files:\t%d\n", mkv)
	fmt.Fprintf(tw, "  with a text subtitle:\t%d\t%s\n", st.WithText, pct(st.WithText, mkv))
	fmt.Fprintf(tw, "  image subtitles only:\t%d\t%s\n", st.ImageOnly, pct(st.ImageOnly, mkv))
	fmt.Fprintf(tw, "  no subtitles:\t%d\t%s\n", st.NoSubtitles, pct(st.NoSubtitles, mkv))
	fmt.Fprintf(tw, "  unknown subtitle codec:\t%d\t%s\n", st.UnknownCodec, pct(st.UnknownCodec, mkv))
	fmt.Fprintf(tw, "  codec not recorded (v1 entry):\t%d\t%s\n", st.CodecNotRecorded, pct(st.CodecNotRecorded, mkv))

	writeCodecs := func(title string, m map[string]CodecCount) {
		fmt.Fprintf(tw, "\n%s\ttracks\tfiles\n", title)
		ids := make([]string, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			if m[ids[i]].Tracks != m[ids[j]].Tracks {
				return m[ids[i]].Tracks > m[ids[j]].Tracks
			}
			return ids[i] < ids[j]
		})
		if len(ids) == 0 {
			fmt.Fprintf(tw, "  (none)\t\t\n")
		}
		for _, id := range ids {
			name := id
			if name == "" {
				name = "(no CodecID)"
			}
			fmt.Fprintf(tw, "  %s\t%d\t%d\n", name, m[id].Tracks, m[id].Files)
		}
	}
	writeCodecs("Subtitle codecs", st.SubtitleCodecs)
	writeCodecs("Audio codecs", st.AudioCodecs)

	fmt.Fprintf(tw, "\nSubtitle tracks by language\ttext\timage\tunknown\n")
	langs := make([]string, 0, len(st.LanguageKinds))
	for l := range st.LanguageKinds {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	if len(langs) == 0 {
		fmt.Fprintf(tw, "  (none)\t\t\t\n")
	}
	for _, l := range langs {
		k := st.LanguageKinds[l]
		fmt.Fprintf(tw, "  %s\t%d\t%d\t%d\n", l, k[KindText], k[KindImage], k[KindUnknown])
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	fmt.Fprintf(w, "\nFiles with image subtitles only (%d):\n", len(st.ImageOnlyFiles))
	for _, f := range st.ImageOnlyFiles {
		hash := f.InfoHash
		if len(hash) > 8 {
			hash = hash[:8]
		}
		fmt.Fprintf(w, "  %s [%s %s] %s\n", f.File, f.Provider, hash, f.Subtitles)
	}
	return nil
}

// DefaultStatsDir is the cache directory of a server started with the same
// environment: $SEANIME_DATA_DIR/track-probe, else <user config dir>/Seanime/track-probe
// (/home/seanime/.config/Seanime/track-probe in the image).
func DefaultStatsDir() string {
	if d := strings.TrimSpace(os.Getenv("SEANIME_DATA_DIR")); d != "" {
		return filepath.Join(os.ExpandEnv(d), "track-probe")
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "Seanime", "track-probe")
	}
	return "track-probe"
}

// RunStatsCommand runs "seanime trackprobe-stats [flags]" and returns the exit code.
func RunStatsCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(StatsCommand, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "track-probe cache directory (default: the server's, "+DefaultStatsDir()+")")
	dataDir := fs.String("datadir", "", "server data directory; the cache is <datadir>/track-probe")
	asJSON := fs.Bool("json", false, "print JSON instead of text")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: seanime %s [--dir DIR | --datadir DIR] [--json]\n\n", StatsCommand)
		fmt.Fprintf(stderr, "Read-only report of the subtitle codecs in the track-probe cache.\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	d := *dir
	if d == "" && *dataDir != "" {
		d = filepath.Join(*dataDir, "track-probe")
	}
	if d == "" {
		d = DefaultStatsDir()
	}
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		fmt.Fprintf(stderr, "%s: no track-probe cache at %s\n", StatsCommand, d)
		return 1
	}
	st, err := CollectStats(d)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", StatsCommand, err)
		return 1
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		err = enc.Encode(st)
	} else {
		err = st.WriteText(stdout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", StatsCommand, err)
		return 1
	}
	return 0
}
