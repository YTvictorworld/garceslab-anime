// Package eval is the evaluation harness of subtitle translation (Garceslab Anime,
// phase F1): it translates local subtitle files with a model and preset and measures
// the result against a reference translation (chrF/chrF++, broken tags, lines left in
// the source language, the blocking checks of the plan, speed), then writes JSON and
// Markdown reports and blind review sheets.
//
// The evaluation data (real subtitles and official translations) never goes into the
// repository: it lives in a directory outside it (see docs/subtitle-translation-eval.md).
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"seanime/internal/subtranslate/ass"
	"seanime/internal/subtranslate/lang"
	"seanime/internal/subtranslate/markers"
	"seanime/internal/subtranslate/translate"
)

// Env describes where a run happened; it goes into every report.
type Env struct {
	Endpoint    string  `json:"endpoint"`
	Backend     string  `json:"backend"`                // e.g. "vulkan", "cuda", "cpu"
	Machine     string  `json:"machine,omitempty"`      // free label, e.g. "desktop 3050 Ti"
	ServerBuild string  `json:"server_build,omitempty"` // llama-server build_info
	ModelFile   string  `json:"model_file,omitempty"`   // GGUF base name
	ServerCtx   int     `json:"server_ctx,omitempty"`
	Slots       int     `json:"slots,omitempty"`
	ColdLoadS   float64 `json:"cold_load_s,omitempty"`
	PeakVRAMMiB int     `json:"peak_vram_mib,omitempty"`
	MaxTempC    int     `json:"max_temp_c,omitempty"`
	// Throttled: the GPU slowed down for heat or power during the item; its speed
	// numbers are not representative.
	Throttled bool `json:"throttled,omitempty"`
}

// Options configure a run.
type Options struct {
	Client    translate.Completer
	Registry  *lang.Registry
	Preset    *translate.Preset
	Model     string
	Hint      translate.ModelHint
	OutDir    string
	Signs     string // "none" or "simple"
	Limit     int    // max lines per item (0 = all)
	ChunkSize int    // first-chunk size (default 8)
	Log       zerolog.Logger
	DebugText bool
	Env       Env
}

// CatResult are the per-category numbers of a line set.
type CatResult struct {
	Lines           int       `json:"lines"`
	Fallback        int       `json:"fallback"`
	SourceLang      int       `json:"source_lang"`
	TagLines        int       `json:"tag_lines"`
	TagsBrokenFirst int       `json:"tags_broken_first"`
	FirstFailed     int       `json:"first_failed"`
	ChrFppStats     []float64 `json:"chrfpp_stats,omitempty"`
}

// ItemResult is the outcome of one item with one model.
type ItemResult struct {
	Item          string    `json:"item"`
	Pair          string    `json:"pair"`
	Model         string    `json:"model"`
	Preset        string    `json:"preset"`
	Mode          string    `json:"mode"`
	PromptVersion int       `json:"prompt_version"`
	StartedAt     time.Time `json:"started_at"`
	Env           Env       `json:"env"`

	Events  int            `json:"events"`
	Skipped map[string]int `json:"skipped"` // events not translated, by kind
	Lines   int            `json:"lines"`   // lines sent to the model (after merging duplicate layers)

	// Blocking checks.
	CountErrorsFirst int            `json:"count_errors_first"` // items missing from the first reply
	FirstFailed      int            `json:"first_failed"`       // lines failing any check on the first try
	IssuesFirst      map[string]int `json:"issues_first"`
	IssuesFinal      map[string]int `json:"issues_final"`
	Fallback         int            `json:"fallback"`
	SourceLangLines  int            `json:"source_lang_lines"`
	TagLines         int            `json:"tag_lines"` // lines with inner tags or breaks
	TagsBrokenFirst  int            `json:"tags_broken_first"`
	TagsBrokenFinal  int            `json:"tags_broken_final"`
	BreaksOver       int            `json:"breaks_over"` // more ⟦N⟧ than allowed (first try)
	EmptyFirst       int            `json:"empty_first"`
	LengthOut        int            `json:"length_out"` // final lines outside 0.5–2.2×
	CPSOver          int            `json:"cps_over"`   // final lines above 20 characters/s
	UntouchedChanged int            `json:"untouched_changed"`
	StructureOK      bool           `json:"structure_ok"` // same events, same timings

	Batches        int `json:"batches"`
	RetriedBatches int `json:"retried_batches"`
	Requests       int `json:"requests"`
	FailedRequests int `json:"failed_requests"`

	// Indicative quality.
	Aligned     int       `json:"aligned_segments"`
	ChrF        float64   `json:"chrf"`
	ChrFpp      float64   `json:"chrfpp"`
	ChrFStats   []float64 `json:"chrf_stats,omitempty"`
	ChrFppStats []float64 `json:"chrfpp_stats,omitempty"`

	ByCat map[string]*CatResult `json:"by_cat,omitempty"`

	// Speed.
	PromptTokens     int     `json:"prompt_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	PromptMS         float64 `json:"prompt_ms"`
	PredictedMS      float64 `json:"predicted_ms"`
	WallS            float64 `json:"wall_s"`
	SpanS            float64 `json:"span_s"` // media time covered by the translated lines
	FirstChunkLines  int     `json:"first_chunk_lines"`
	FirstChunkS      float64 `json:"first_chunk_s"`
}

// LineRecord is one translated line, saved privately next to the results for the
// blind review. It contains subtitle text: it never leaves the output directory.
type LineRecord struct {
	Item      string   `json:"item"`
	Pair      string   `json:"pair"`
	N         int      `json:"n"` // event index
	LineID    string   `json:"line_id,omitempty"`
	Cat       string   `json:"cat,omitempty"`
	Source    string   `json:"source"`
	Output    string   `json:"output"`
	Reference string   `json:"reference,omitempty"`
	Fallback  bool     `json:"fallback,omitempty"`
	Issues    []string `json:"issues,omitempty"`
}

// RunItem translates one item and writes its result, its line records and the
// translated file under OutDir.
func RunItem(ctx context.Context, m *Manifest, it Item, o Options) (*ItemResult, error) {
	pair, err := o.Registry.NewPair(it.Source.Lang, m.TargetOf(it))
	if err != nil {
		return nil, fmt.Errorf("item %s: %w", it.ID, err)
	}
	src, err := loadDoc(m.Path(it.Source.File))
	if err != nil {
		return nil, fmt.Errorf("item %s: %w", it.ID, err)
	}
	var ref doc
	if it.Reference != nil {
		if ref, err = loadDoc(m.Path(it.Reference.File)); err != nil {
			return nil, fmt.Errorf("item %s reference: %w", it.ID, err)
		}
	}
	res := &ItemResult{
		Item: it.ID, Pair: pair.String(), Model: o.Model, Preset: o.Preset.Name, Mode: o.Hint.Mode,
		PromptVersion: translate.PromptVersion, StartedAt: time.Now().UTC(), Env: o.Env,
		Skipped: map[string]int{}, IssuesFirst: map[string]int{}, IssuesFinal: map[string]int{},
	}
	var from, to time.Duration = 0, -1
	if it.From != "" {
		from, _ = ParseClock(it.From)
	}
	if it.To != "" {
		to, _ = ParseClock(it.To)
	}

	units := src.units()
	res.Events = len(units)
	// Pick the lines to translate and merge duplicate layers (same text, same time).
	type group struct {
		members []int // unit indexes
		scene   string
	}
	var groups []*group
	byKey := map[string]*group{}
	for i, u := range units {
		if !translatable(u, o.Signs) {
			res.Skipped[u.kind]++
			continue
		}
		if u.start < from || (to >= 0 && u.start >= to) {
			res.Skipped["window"]++
			continue
		}
		key := fmt.Sprintf("%d|%d|%s", u.start, u.end, u.masked.Text)
		if g := byKey[key]; g != nil {
			g.members = append(g.members, i)
			continue
		}
		if o.Limit > 0 && len(groups) >= o.Limit {
			res.Skipped["limit"]++
			continue
		}
		g := &group{members: []int{i}, scene: u.scene}
		byKey[key] = g
		groups = append(groups, g)
	}
	res.Lines = len(groups)
	if len(groups) == 0 {
		return nil, fmt.Errorf("item %s: nothing to translate", it.ID)
	}
	lines := make([]translate.Line, len(groups))
	for gi, g := range groups {
		u := units[g.members[0]]
		lines[gi] = translate.Line{ID: gi, Start: u.start, End: u.end, Speaker: u.speaker, Masked: u.masked}
	}
	newTr := func() *translate.Translator {
		return &translate.Translator{Client: o.Client, Pair: pair, Preset: *o.Preset, Model: o.Model, Hint: o.Hint,
			Glossary: it.Glossary, Series: it.Series, Log: o.Log, DebugText: o.DebugText}
	}

	// First chunk with the model warm: one tiny request, then the first lines alone.
	chunk := o.ChunkSize
	if chunk <= 0 {
		chunk = 8
	}
	chunk = min(chunk, len(lines))
	warm := newTr()
	if _, _, err := warm.Translate(ctx, lines[:1]); err != nil {
		return nil, err
	}
	_, cst, err := newTr().Translate(ctx, lines[:chunk])
	if err != nil {
		return nil, err
	}
	res.FirstChunkLines, res.FirstChunkS = chunk, cst.Wall.Seconds()

	// The full run. Line sets restart the context at each scene.
	tr := newTr()
	results := make([]translate.Result, 0, len(lines))
	var stats translate.Stats
	start := time.Now()
	for s := 0; s < len(lines); {
		e := s + 1
		for e < len(lines) && groups[e].scene == groups[s].scene {
			e++
		}
		r, st, err := tr.Translate(ctx, lines[s:e])
		stats.Add(st)
		if err != nil {
			return nil, err
		}
		results = append(results, r...)
		s = e
	}
	if stats.Requests == 0 && stats.FailedRequests > 0 {
		// Every request failed (server down, crashed GPU driver): no result rather
		// than a result full of fallbacks that would look like a bad model.
		return nil, fmt.Errorf("item %s: every request failed; is the server still up?", it.ID)
	}
	res.WallS = time.Since(start).Seconds()
	res.Batches, res.RetriedBatches, res.Requests, res.FailedRequests = stats.Batches, stats.RetriedBatches, stats.Requests, stats.FailedRequests
	res.PromptTokens, res.CachedTokens, res.CompletionTokens = stats.PromptTokens, stats.CachedTokens, stats.CompletionTokens
	res.PromptMS, res.PredictedMS = stats.PromptMS, stats.PredictedMS
	res.SpanS = (lines[len(lines)-1].End - lines[0].Start).Seconds()

	// Write the translations into the document.
	det := lang.NewDetector(pair)
	finals := make(map[int]string) // unit index -> final text
	for gi, g := range groups {
		r := results[gi]
		for _, ui := range g.members {
			finals[ui] = units[ui].masked.Restore(r.Text)
		}
	}
	for i, u := range units {
		if f, ok := finals[i]; ok {
			src.setText(i, f)
		} else {
			src.setText(i, u.raw)
		}
	}
	out := src.bytes()

	// Blocking checks on the result.
	again, err := parseDoc(out, "out"+src.ext())
	if err != nil {
		return nil, fmt.Errorf("item %s: the translated file does not parse: %w", it.ID, err)
	}
	after := again.units()
	res.StructureOK = len(after) == len(units)
	for i := range units {
		if !res.StructureOK {
			break
		}
		if after[i].start != units[i].start || after[i].end != units[i].end {
			res.StructureOK = false
		}
		if _, translated := finals[i]; !translated && after[i].raw != units[i].raw {
			res.UntouchedChanged++
		}
	}

	byCat := map[string]*CatResult{}
	cat := func(c string) *CatResult {
		if c == "" {
			return nil
		}
		if byCat[c] == nil {
			byCat[c] = &CatResult{}
		}
		return byCat[c]
	}
	var records []LineRecord
	for gi, g := range groups {
		r := results[gi]
		u := units[g.members[0]]
		final := finals[g.members[0]]
		plainFinal := markers.Plain(src.mask(final).Text)
		c := cat(u.cat)
		if c != nil {
			c.Lines++
		}
		for _, is := range r.FirstIssues {
			res.IssuesFirst[is]++
			if is == translate.IssueMissing {
				res.CountErrorsFirst++
			}
			if is == translate.IssueBreaks {
				res.BreaksOver++
			}
			if is == translate.IssueEmpty {
				res.EmptyFirst++
			}
		}
		if len(r.FirstIssues) > 0 {
			res.FirstFailed++
			if c != nil {
				c.FirstFailed++
			}
		}
		for _, is := range r.FinalIssues {
			if r.Fallback {
				res.IssuesFinal[is]++
			}
		}
		if r.Fallback {
			res.Fallback++
			if c != nil {
				c.Fallback++
			}
		}
		if det.Detect(plainFinal) == lang.Source ||
			(len(lang.Words(plainFinal)) > 3 && strings.EqualFold(strings.Join(lang.Words(plainFinal), " "), strings.Join(lang.Words(u.plain()), " "))) {
			res.SourceLangLines++
			if c != nil {
				c.SourceLang++
			}
		}
		if u.masked.HasTags() || u.masked.Breaks > 0 {
			res.TagLines++
			if c != nil {
				c.TagLines++
			}
			if r.TagsBrokenFirst {
				res.TagsBrokenFirst++
				if c != nil {
					c.TagsBrokenFirst++
				}
			}
			fm := src.mask(final)
			if fm.Prefix != u.masked.Prefix || strings.Join(fm.Tags, "\x00") != strings.Join(u.masked.Tags, "\x00") {
				res.TagsBrokenFinal++
			}
		}
		ns, no := utf8.RuneCountInString(u.plain()), utf8.RuneCountInString(plainFinal)
		if ns >= 12 && !r.Fallback {
			if ratio := float64(no) / float64(ns); ratio < 0.5 || ratio > 2.2 {
				res.LengthOut++
			}
		}
		if d := (u.end - u.start).Seconds(); d > 0 && float64(no)/d > 20 {
			res.CPSOver++
		}
		issues := r.FirstIssues
		if r.Fallback {
			issues = append(append([]string{}, r.FirstIssues...), r.FinalIssues...)
		}
		records = append(records, LineRecord{Item: it.ID, Pair: res.Pair, N: u.idx, LineID: u.lineID, Cat: u.cat,
			Source: u.raw, Output: final, Fallback: r.Fallback, Issues: dedupe(issues)})
	}

	// chrF / chrF++ against the reference.
	chrf, chrfpp := NewCHRF(), NewCHRFpp()
	if ref == nil {
		for gi, g := range groups {
			u := units[g.members[0]]
			if u.ref == "" {
				continue
			}
			hyp := markers.Plain(src.mask(finals[g.members[0]]).Text)
			rp := markers.Plain(ass.Mask(u.ref).Text)
			chrf.Add(hyp, rp)
			chrfpp.Add(hyp, rp)
			res.Aligned++
			records[gi].Reference = u.ref
			if c := byCat[u.cat]; c != nil {
				one := NewCHRFpp()
				one.stats = c.ChrFppStats
				one.Add(hyp, rp)
				c.ChrFppStats = one.stats
			}
		}
	} else {
		srcSegs := make([]segment, len(groups))
		for gi, g := range groups {
			u := units[g.members[0]]
			srcSegs[gi] = segment{start: u.start, end: u.end, text: markers.Plain(src.mask(finals[g.members[0]]).Text)}
		}
		var refSegs []segment
		for _, u := range ref.units() {
			if u.kind == string(ass.KindDialogue) || u.kind == string(ass.KindSign) {
				refSegs = append(refSegs, segment{start: u.start, end: u.end, text: u.plain()})
			}
		}
		for _, comp := range align(srcSegs, refSegs) {
			chrf.Add(comp.hyp, comp.ref)
			chrfpp.Add(comp.hyp, comp.ref)
			res.Aligned++
			for _, gi := range comp.srcIdx {
				records[gi].Reference = comp.ref
			}
		}
	}
	res.ChrF, res.ChrFpp = chrf.Score(), chrfpp.Score()
	res.ChrFStats, res.ChrFppStats = chrf.stats, chrfpp.stats
	if len(byCat) > 0 {
		res.ByCat = byCat
	}

	// Files: result (metrics only), line records and translated copy (private text).
	dir := RunDir(o.OutDir, o.Model, o.Preset.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := SaveResult(o.OutDir, res); err != nil {
		return nil, err
	}
	if err := writeJSONL(filepath.Join(dir, it.ID+".lines.jsonl"), records); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, it.ID+".translated"+src.ext()), out, 0o644); err != nil {
		return nil, err
	}
	return res, nil
}

// RunDir is where the files of one model and preset go.
func RunDir(outDir, model, preset string) string {
	return filepath.Join(outDir, "runs", Slug(model)+"__"+preset)
}

// SaveResult writes (or rewrites) an item result.
func SaveResult(outDir string, res *ItemResult) error {
	dir := RunDir(outDir, res.Model, res.Preset)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, res.Item+".json"), res)
}

func translatable(u unit, signs string) bool {
	switch u.kind {
	case string(ass.KindDialogue):
		return true
	case string(ass.KindSign):
		return signs == "simple" && u.simple
	}
	return false
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func writeJSONL[T any](path string, rows []T) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			f.Close()
			return err
		}
	}
	return f.Close()
}
