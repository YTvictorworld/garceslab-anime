package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Summary aggregates the item results of one pair, model and preset.
type Summary struct {
	Pair    string `json:"pair"`
	Model   string `json:"model"`
	Preset  string `json:"preset"`
	Mode    string `json:"mode"`
	Backend string `json:"backend"`
	Items   int    `json:"items"`
	Lines   int    `json:"lines"`

	ChrF   float64 `json:"chrf"`
	ChrFpp float64 `json:"chrfpp"`

	CountErrorsFirst   int     `json:"count_errors_first"`
	FirstFailedPct     float64 `json:"first_failed_pct"`
	TagLines           int     `json:"tag_lines"`
	TagsBrokenFirstPct float64 `json:"tags_broken_first_pct"`
	TagsBrokenFinalPct float64 `json:"tags_broken_final_pct"`
	SourceLangPct      float64 `json:"source_lang_pct"`
	FallbackPct        float64 `json:"fallback_pct"`
	EmptyFirst         int     `json:"empty_first"`
	BreaksOver         int     `json:"breaks_over"`
	LengthOutPct       float64 `json:"length_out_pct"`
	CPSOverPct         float64 `json:"cps_over_pct"`
	UntouchedChanged   int     `json:"untouched_changed"`
	StructureOK        bool    `json:"structure_ok"`
	RetriedBatchesPct  float64 `json:"retried_batches_pct"`
	FailedRequests     int     `json:"failed_requests"`

	PrefillTokS    float64               `json:"prefill_tok_s"`
	GenTokS        float64               `json:"gen_tok_s"`
	WallS          float64               `json:"wall_s"`
	RealtimeX      float64               `json:"realtime_x"`    // media seconds translated per wall second
	FirstChunkS    float64               `json:"first_chunk_s"` // mean
	FirstChunkMaxS float64               `json:"first_chunk_max_s"`
	PeakVRAMMiB    int                   `json:"peak_vram_mib,omitempty"`
	MaxTempC       int                   `json:"max_temp_c,omitempty"`
	Throttled      bool                  `json:"throttled,omitempty"`
	ColdLoadS      float64               `json:"cold_load_s,omitempty"`
	IssuesFirst    map[string]int        `json:"issues_first"`
	ByCat          map[string]CatSummary `json:"by_cat,omitempty"`
}

// CatSummary is a line-set category across items.
type CatSummary struct {
	Lines              int     `json:"lines"`
	ChrFpp             float64 `json:"chrfpp"`
	FirstFailedPct     float64 `json:"first_failed_pct"`
	TagsBrokenFirstPct float64 `json:"tags_broken_first_pct"`
	SourceLangPct      float64 `json:"source_lang_pct"`
	FallbackPct        float64 `json:"fallback_pct"`
}

// Report is report.json.
type Report struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Summaries   []Summary     `json:"summaries"`
	Items       []*ItemResult `json:"items"`
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

// LoadResults reads every item result under OutDir/runs.
func LoadResults(outDir string) ([]*ItemResult, error) {
	paths, _ := filepath.Glob(filepath.Join(outDir, "runs", "*", "*.json"))
	var out []*ItemResult
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var r ItemResult
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, &r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Pair != b.Pair {
			return a.Pair < b.Pair
		}
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Item < b.Item
	})
	return out, nil
}

// Summarize groups results by pair, model and preset.
func Summarize(results []*ItemResult) []Summary {
	type key struct{ pair, model, preset string }
	groups := map[key][]*ItemResult{}
	var keys []key
	for _, r := range results {
		k := key{r.Pair, r.Model, r.Preset}
		if groups[k] == nil {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], r)
	}
	var out []Summary
	for _, k := range keys {
		rs := groups[k]
		s := Summary{Pair: k.pair, Model: k.model, Preset: k.preset, Mode: rs[0].Mode, Backend: rs[0].Env.Backend,
			Items: len(rs), StructureOK: true, IssuesFirst: map[string]int{}}
		chrf, chrfpp := NewCHRF(), NewCHRFpp()
		var firstFailed, tbFirst, tbFinal, srcLang, fallback, lengthOut, cps, batches, retried int
		var promptTok, cached, genTok int
		var promptMS, genMS, span, chunkSum float64
		cats := map[string]*CatResult{}
		for _, r := range rs {
			s.Lines += r.Lines
			chrf.merge(r.ChrFStats)
			chrfpp.merge(r.ChrFppStats)
			s.CountErrorsFirst += r.CountErrorsFirst
			firstFailed += r.FirstFailed
			s.TagLines += r.TagLines
			tbFirst += r.TagsBrokenFirst
			tbFinal += r.TagsBrokenFinal
			srcLang += r.SourceLangLines
			fallback += r.Fallback
			s.EmptyFirst += r.EmptyFirst
			s.BreaksOver += r.BreaksOver
			lengthOut += r.LengthOut
			cps += r.CPSOver
			s.UntouchedChanged += r.UntouchedChanged
			s.StructureOK = s.StructureOK && r.StructureOK
			batches += r.Batches
			retried += r.RetriedBatches
			s.FailedRequests += r.FailedRequests
			promptTok += r.PromptTokens
			cached += r.CachedTokens
			genTok += r.CompletionTokens
			promptMS += r.PromptMS
			genMS += r.PredictedMS
			s.WallS += r.WallS
			span += r.SpanS
			chunkSum += r.FirstChunkS
			s.FirstChunkMaxS = max(s.FirstChunkMaxS, r.FirstChunkS)
			s.PeakVRAMMiB = max(s.PeakVRAMMiB, r.Env.PeakVRAMMiB)
			s.MaxTempC = max(s.MaxTempC, r.Env.MaxTempC)
			s.Throttled = s.Throttled || r.Env.Throttled
			s.ColdLoadS = max(s.ColdLoadS, r.Env.ColdLoadS)
			for is, n := range r.IssuesFirst {
				s.IssuesFirst[is] += n
			}
			for name, c := range r.ByCat {
				acc := cats[name]
				if acc == nil {
					acc = &CatResult{}
					cats[name] = acc
				}
				acc.Lines += c.Lines
				acc.Fallback += c.Fallback
				acc.SourceLang += c.SourceLang
				acc.TagLines += c.TagLines
				acc.TagsBrokenFirst += c.TagsBrokenFirst
				acc.FirstFailed += c.FirstFailed
				m := NewCHRFpp()
				m.stats = acc.ChrFppStats
				m.merge(c.ChrFppStats)
				acc.ChrFppStats = m.stats
			}
		}
		s.ChrF, s.ChrFpp = chrf.Score(), chrfpp.Score()
		s.FirstFailedPct = pct(firstFailed, s.Lines)
		s.TagsBrokenFirstPct = pct(tbFirst, s.TagLines)
		s.TagsBrokenFinalPct = pct(tbFinal, s.TagLines)
		s.SourceLangPct = pct(srcLang, s.Lines)
		s.FallbackPct = pct(fallback, s.Lines)
		s.LengthOutPct = pct(lengthOut, s.Lines)
		s.CPSOverPct = pct(cps, s.Lines)
		s.RetriedBatchesPct = pct(retried, batches)
		if promptMS > 0 {
			s.PrefillTokS = float64(promptTok-cached) / promptMS * 1000
		}
		if genMS > 0 {
			s.GenTokS = float64(genTok) / genMS * 1000
		}
		if s.WallS > 0 {
			s.RealtimeX = span / s.WallS
		}
		s.FirstChunkS = chunkSum / float64(len(rs))
		if len(cats) > 0 {
			s.ByCat = map[string]CatSummary{}
			for name, c := range cats {
				m := NewCHRFpp()
				m.stats = c.ChrFppStats
				s.ByCat[name] = CatSummary{Lines: c.Lines, ChrFpp: m.Score(), FirstFailedPct: pct(c.FirstFailed, c.Lines),
					TagsBrokenFirstPct: pct(c.TagsBrokenFirst, c.TagLines), SourceLangPct: pct(c.SourceLang, c.Lines),
					FallbackPct: pct(c.Fallback, c.Lines)}
			}
		}
		out = append(out, s)
	}
	return out
}

func (c *CHRF) merge(stats []float64) {
	if len(stats) == 0 {
		return
	}
	if c.stats == nil {
		c.stats = make([]float64, len(stats))
	}
	if len(c.stats) != len(stats) {
		return
	}
	for i, v := range stats {
		c.stats[i] += v
	}
}

// WriteReport writes report.json and report.md in outDir from every saved result.
func WriteReport(outDir string) (*Report, error) {
	results, err := LoadResults(outDir)
	if err != nil {
		return nil, err
	}
	rep := &Report{GeneratedAt: time.Now().UTC(), Summaries: Summarize(results), Items: results}
	if err := writeJSON(filepath.Join(outDir, "report.json"), rep); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "report.md"), []byte(Markdown(rep)), 0o644); err != nil {
		return nil, err
	}
	return rep, nil
}

func f1(v float64) string { return fmt.Sprintf("%.1f", v) }

// Markdown renders the report.
func Markdown(rep *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Subtitle translation evaluation\n\nGenerated %s. Prompt version, backend and server are per row in report.json. ", rep.GeneratedAt.Format(time.RFC3339))
	b.WriteString("Speed numbers depend on the backend and the machine: compare models only within one backend and machine, and ignore rows where the GPU throttled.\n\n")

	b.WriteString("## Quality and blocking checks (per language pair and model)\n\n")
	b.WriteString("| Pair | Model | Preset | Mode | Items | Lines | chrF++ | chrF | Count errors (1st try) | Lines failing a check (1st try) % | Broken tags 1st try % | Broken tags final % | Source-language lines % | Fallback % | Length out % | CPS > 20 % | Untouched changed | Structure | Retried batches % |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range rep.Summaries {
		structure := "ok"
		if !s.StructureOK {
			structure = "**BROKEN**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %d | %s | %s | %d | %s | %s (of %d) | %s | %s | %s | %s | %s | %d | %s | %s |\n",
			s.Pair, s.Model, s.Preset, s.Mode, s.Items, s.Lines, f1(s.ChrFpp), f1(s.ChrF), s.CountErrorsFirst, f1(s.FirstFailedPct),
			f1(s.TagsBrokenFirstPct), s.TagLines, f1(s.TagsBrokenFinalPct), f1(s.SourceLangPct), f1(s.FallbackPct), f1(s.LengthOutPct),
			f1(s.CPSOverPct), s.UntouchedChanged, structure, f1(s.RetriedBatchesPct))
	}

	b.WriteString("\n## Speed\n\n")
	b.WriteString("| Pair | Model | Backend | Prefill tok/s | Generation tok/s | First chunk s (mean / max) | Total wall s | × real time | Peak VRAM MiB | Cold load s | GPU max °C | Throttled |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range rep.Summaries {
		vram, cold := "–", "–"
		if s.PeakVRAMMiB > 0 {
			vram = fmt.Sprint(s.PeakVRAMMiB)
		}
		if s.ColdLoadS > 0 {
			cold = f1(s.ColdLoadS)
		}
		temp, thr := "–", "no"
		if s.MaxTempC > 0 {
			temp = fmt.Sprint(s.MaxTempC)
		}
		if s.Throttled {
			thr = "**yes**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %.2f / %.2f | %s | %s | %s | %s | %s | %s |\n", s.Pair, s.Model, s.Backend,
			f1(s.PrefillTokS), f1(s.GenTokS), s.FirstChunkS, s.FirstChunkMaxS, f1(s.WallS), f1(s.RealtimeX), vram, cold, temp, thr)
	}

	b.WriteString("\n## First-try issues\n\n| Pair | Model | Issues |\n|---|---|---|\n")
	for _, s := range rep.Summaries {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", s.Pair, s.Model, issueList(s.IssuesFirst))
	}

	for _, s := range rep.Summaries {
		if len(s.ByCat) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## Line-set categories: %s, %s\n\n", s.Pair, s.Model)
		b.WriteString("| Category | Lines | chrF++ | Failing a check 1st try % | Broken tags 1st try % | Source-language % | Fallback % |\n|---|---|---|---|---|---|---|\n")
		names := make([]string, 0, len(s.ByCat))
		for n := range s.ByCat {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			c := s.ByCat[n]
			fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s | %s |\n", n, c.Lines, f1(c.ChrFpp), f1(c.FirstFailedPct),
				f1(c.TagsBrokenFirstPct), f1(c.SourceLangPct), f1(c.FallbackPct))
		}
	}

	b.WriteString("\n## Per file\n\n| Item | Pair | Model | Lines | chrF++ | Aligned segments | Broken tags 1st % | Source-language % | Fallback | Retried batches | Wall s | First chunk s |\n|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rep.Items {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s | %d | %s | %s | %d | %d/%d | %s | %.2f |\n", r.Item, r.Pair, r.Model, r.Lines, f1(r.ChrFpp),
			r.Aligned, f1(pct(r.TagsBrokenFirst, r.TagLines)), f1(pct(r.SourceLangLines, r.Lines)), r.Fallback,
			r.RetriedBatches, r.Batches, f1(r.WallS), r.FirstChunkS)
	}
	return b.String()
}

func issueList(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, m[k])
	}
	return strings.Join(parts, ", ")
}
