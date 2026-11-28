package eval

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"seanime/internal/subtranslate/lang"
	"seanime/internal/subtranslate/translate"
)

// Reference values computed with sacreBLEU 2.4.3 (CHRF() and CHRF(word_order=2)) on
// these invented sentences.
func TestCHRFMatchesSacreBLEU(t *testing.T) {
	hyp := []string{"¿Dónde estabas? ¡Te dije que esperaras!", "Lo siento, Kirito-kun.", "Vamos a la tienda (ahora)."}
	ref := []string{"¿Dónde estabas? ¡Te pedí que esperaras!", "Perdón, Kirito-kun.", "Vamos ahora a la tienda."}
	c, cpp := NewCHRF(), NewCHRFpp()
	for i := range hyp {
		c.Add(hyp[i], ref[i])
		cpp.Add(hyp[i], ref[i])
	}
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	if !near(c.Score(), 72.3336491315108) {
		t.Errorf("chrF = %.12f", c.Score())
	}
	if !near(cpp.Score(), 71.95383256882802) {
		t.Errorf("chrF++ = %.12f", cpp.Score())
	}
	one := NewCHRFpp()
	one.Add(hyp[0], ref[0])
	if !near(one.Score(), 80.23665147245063) {
		t.Errorf("sentence chrF++ = %.12f", one.Score())
	}
	zero := NewCHRFpp()
	zero.Add("abc", "xyz")
	if zero.Score() != 0 || NewCHRF().Score() != 0 {
		t.Error("no match must score 0")
	}
}

func TestAlign(t *testing.T) {
	s := func(a, b float64, text string) segment {
		return segment{start: dur(a), end: dur(b), text: text}
	}
	src := []segment{s(1, 3, "A"), s(3, 5, "B"), s(5, 6, "C"), s(20, 21, "lonely")}
	ref := []segment{s(1, 5, "AB"), s(5.1, 6, "C1"), s(5.5, 6.5, "C2"), s(30, 31, "sign")}
	comps := align(src, ref)
	if len(comps) != 2 {
		t.Fatalf("%+v", comps)
	}
	if comps[0].hyp != "A B" || comps[0].ref != "AB" || comps[1].hyp != "C" || comps[1].ref != "C1 C2" {
		t.Errorf("%+v", comps)
	}
}

func dur(sec float64) time.Duration { return time.Duration(sec * 1e9) }

// --- end to end with a fake model -----------------------------------------------

var spanish = map[string]string{
	"Where were you, Tomo? I waited for an hour!": "¿Dónde estabas, Tomo? ¡Te esperé una hora!",
	"I ⟦1⟧swear⟦2⟧ it wasn't⟦N⟧my fault!":         "Te ⟦1⟧juro⟦2⟧ que no fue⟦N⟧mi culpa!",
	"MOONLIGHT BAKERY":                           "PANADERÍA LUZ DE LUNA",
	"Let's go before the boss changes his mind.": "Vámonos antes de que el jefe cambie de opinión.",
	"T'étais où ? Je t'ai attendu une heure !":   "¿Dónde estabas? ¡Te esperé una hora!",
}

type fakeModel struct{ calls int }

func (f *fakeModel) Complete(_ context.Context, req translate.ChatRequest) (translate.ChatResponse, error) {
	f.calls++
	user := req.Messages[len(req.Messages)-1].Content
	if i := strings.Index(user, "\n\nYour previous answer"); i >= 0 {
		user = user[:i]
	}
	var in struct {
		ID    string `json:"id"`
		Lines []struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
		} `json:"lines"`
	}
	if err := json.Unmarshal([]byte(user), &in); err != nil {
		return translate.ChatResponse{}, err
	}
	var t [][]any
	for _, l := range in.Lines {
		out, ok := spanish[l.Text]
		if !ok {
			out = l.Text // untranslated: the checks must catch it
		}
		t = append(t, []any{l.ID, out})
	}
	data, _ := json.Marshal(map[string]any{"id": in.ID, "t": t})
	return translate.ChatResponse{Content: string(data), PromptTokens: 200, CompletionTokens: 40, PromptMS: 100, PredictedMS: 800}, nil
}

const header = "[Script Info]\nScriptType: v4.00+\nPlayResX: 1920\nPlayResY: 1080\n\n[V4+ Styles]\nFormat: Name, Fontname\nStyle: Default,Arial\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n"

const sourceASS = header +
	"Dialogue: 0,0:00:01.00,0:00:03.00,Default,Riko,0,0,0,,Where were you, Tomo? I waited for an hour!\n" +
	"Dialogue: 0,0:00:03.50,0:00:06.00,Default,Tomo,0,0,0,,I {\\i1}swear{\\i0} it wasn't\\Nmy fault!\n" +
	"Dialogue: 0,0:00:06.50,0:00:08.00,Sign,,0,0,0,,{\\an8\\pos(960,80)}MOONLIGHT BAKERY\n" +
	"Dialogue: 1,0:00:06.50,0:00:08.00,Sign,,0,0,0,,{\\an8\\pos(960,80)\\bord0}MOONLIGHT BAKERY\n" +
	"Dialogue: 0,0:00:09.00,0:00:12.00,OP,,0,0,0,,{\\k20}ha{\\k30}ru {\\k25}no\n" +
	"Dialogue: 0,0:00:12.50,0:00:14.00,Default,,0,0,0,,This line has no Spanish in the fake and stays English.\n"

const referenceASS = header +
	"Dialogue: 0,0:00:01.05,0:00:03.00,Default,,0,0,0,,¿Dónde estabas, Tomo? ¡Te esperé una hora!\n" +
	"Dialogue: 0,0:00:03.50,0:00:06.00,Default,,0,0,0,,¡Te {\\i1}juro{\\i0} que no fue mi culpa!\n" +
	"Dialogue: 0,0:00:06.50,0:00:08.00,Sign,,0,0,0,,{\\an8\\pos(960,80)}PANADERÍA LUZ DE LUNA\n" +
	"Dialogue: 0,0:00:12.50,0:00:14.00,Default,,0,0,0,,Esta línea no tiene español.\n"

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func options(t *testing.T, out, model string) Options {
	cfg := translate.DefaultConfig()
	p, _ := cfg.Preset("")
	return Options{Client: &fakeModel{}, Registry: lang.Default(), Preset: p, Model: model, Hint: cfg.Hint(model),
		OutDir: out, Signs: "simple", Env: Env{Backend: "test"}}
}

func TestRunItemEpisode(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"ep/e01.en.ass":     sourceASS,
		"ep/e01.es-419.ass": referenceASS,
		"manifest.json": `{"items":[{"id":"e01","source":{"file":"ep/e01.en.ass","lang":"eng"},
			"reference":{"file":"ep/e01.es-419.ass","lang":"es-419"}}]}`,
	})
	m, err := LoadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "results")
	res, err := RunItem(context.Background(), m, m.Items[0], options(t, out, "model-a"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Pair != "en→es-419" || res.Events != 6 || res.Lines != 4 { // two sign layers merged
		t.Fatalf("pair %s events %d lines %d", res.Pair, res.Events, res.Lines)
	}
	if res.Skipped["karaoke"] != 1 {
		t.Errorf("skipped %v", res.Skipped)
	}
	if res.Fallback != 1 || res.SourceLangLines != 1 || res.IssuesFirst[translate.IssueLanguage] != 1 {
		t.Errorf("fallback %d source-language %d issues %v", res.Fallback, res.SourceLangLines, res.IssuesFirst)
	}
	if res.TagLines != 1 || res.TagsBrokenFirst != 0 || res.TagsBrokenFinal != 0 {
		t.Errorf("tags %d broken %d/%d", res.TagLines, res.TagsBrokenFirst, res.TagsBrokenFinal)
	}
	if !res.StructureOK || res.UntouchedChanged != 0 {
		t.Errorf("structure %v untouched %d", res.StructureOK, res.UntouchedChanged)
	}
	if res.Aligned != 4 || res.ChrFpp < 50 || res.ChrFpp > 100 {
		t.Errorf("aligned %d chrF++ %.1f", res.Aligned, res.ChrFpp)
	}
	if res.FirstChunkLines != 4 || res.RetriedBatches != 1 {
		t.Errorf("first chunk %d retried %d", res.FirstChunkLines, res.RetriedBatches)
	}
	runDir := RunDir(out, "model-a", "equilibrado")
	translated, err := os.ReadFile(filepath.Join(runDir, "e01.translated.ass"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Dialogue: 0,0:00:03.50,0:00:06.00,Default,Tomo,0,0,0,,Te {\\i1}juro{\\i0} que no fue\\Nmi culpa!\n",
		"Dialogue: 0,0:00:06.50,0:00:08.00,Sign,,0,0,0,,{\\an8\\pos(960,80)}PANADERÍA LUZ DE LUNA\n",
		"Dialogue: 1,0:00:06.50,0:00:08.00,Sign,,0,0,0,,{\\an8\\pos(960,80)\\bord0}PANADERÍA LUZ DE LUNA\n",
		"Dialogue: 0,0:00:09.00,0:00:12.00,OP,,0,0,0,,{\\k20}ha{\\k30}ru {\\k25}no\n",
		"This line has no Spanish in the fake and stays English.",
	} {
		if !strings.Contains(string(translated), want) {
			t.Errorf("translated file lacks %q", want)
		}
	}
	// The result JSON has metrics only, no subtitle text.
	data, _ := os.ReadFile(filepath.Join(runDir, "e01.json"))
	if strings.Contains(string(data), "Tomo") || strings.Contains(string(data), "juro") {
		t.Error("the result file must not contain subtitle text")
	}
	recs, err := readRecords(filepath.Join(runDir, "e01.lines.jsonl"))
	if err != nil || len(recs) != 4 || recs[0].Reference == "" {
		t.Fatalf("records %+v %v", recs, err)
	}
}

func TestRunItemLineSetsAndReport(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteSample(dir); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "results")
	for _, model := range []string{"model-a", "model-b"} {
		for _, it := range m.Items {
			res, err := RunItem(context.Background(), m, it, options(t, out, model))
			if err != nil {
				t.Fatal(err)
			}
			if res.ByCat == nil || res.ByCat["tags"] == nil {
				t.Fatalf("%s: categories %v", it.ID, res.ByCat)
			}
			if it.ID == "sample-lines-en" && (res.Skipped["karaoke"] != 1 || res.Lines != 9) {
				t.Errorf("en sample: lines %d skipped %v", res.Lines, res.Skipped)
			}
			if it.ID == "sample-lines-fr" && res.Pair != "fr→es-419" {
				t.Errorf("fr pair %s", res.Pair)
			}
		}
	}
	rep, err := WriteReport(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Summaries) != 4 || len(rep.Items) != 4 { // 2 pairs × 2 models
		t.Fatalf("summaries %d items %d", len(rep.Summaries), len(rep.Items))
	}
	md, _ := os.ReadFile(filepath.Join(out, "report.md"))
	for _, want := range []string{"| en→es-419 | model-a |", "| fr→es-419 | model-b |", "## Speed", "Line-set categories"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("report.md lacks %q", want)
		}
	}

	// Blind review: same lines, hidden models, separate key; then scoring.
	revDir, err := Review(ReviewOptions{OutDir: out, Lines: 5, Seed: 7, Reference: "visible"})
	if err != nil {
		t.Fatal(err)
	}
	sheet, _ := os.ReadFile(filepath.Join(revDir, "sheet.csv"))
	if strings.Contains(string(sheet), "model-a") || strings.Contains(string(sheet), "model-b") {
		t.Error("the sheet must not name the models")
	}
	if _, err := os.Stat(filepath.Join(out, "review-key.json")); err != nil {
		t.Fatal("key missing")
	}
	f, _ := os.Open(filepath.Join(revDir, "sheet.csv"))
	rows, _ := csv.NewReader(f).ReadAll()
	f.Close()
	if len(rows) != 1+5*2 {
		t.Fatalf("%d rows", len(rows))
	}
	var key ReviewKey
	data, _ := os.ReadFile(filepath.Join(out, "review-key.json"))
	_ = json.Unmarshal(data, &key)
	// Score model-a 5 and model-b 3, with a flag on model-b.
	for _, r := range rows[1:] {
		if key.Lines[r[0]][r[1]] == "model-a__equilibrado" {
			r[5] = "5"
		} else {
			r[5] = "3"
			r[10] = "x" // meaning
		}
	}
	filled := filepath.Join(dir, "filled.csv")
	fw, _ := os.Create(filled)
	w := csv.NewWriter(fw)
	_ = w.WriteAll(rows)
	fw.Close()
	scores, err := ScoreReview(filled, filepath.Join(out, "review-key.json"))
	if err != nil {
		t.Fatal(err)
	}
	byRun := map[string]float64{}
	for _, s := range scores {
		byRun[s.Run] += s.Mean * float64(s.Rated)
		if s.Run == "model-b__equilibrado" && s.Severe != 100 {
			t.Errorf("severe %v", s.Severe)
		}
	}
	if byRun["model-a__equilibrado"] != 25 || byRun["model-b__equilibrado"] != 15 {
		t.Errorf("scores %v", byRun)
	}
}

func TestManifestErrors(t *testing.T) {
	dir := t.TempDir()
	for name, doc := range map[string]string{
		"bad id":       `{"items":[{"id":"a b","source":{"file":"x.ass","lang":"en"},"reference":{"file":"y.ass","lang":"es"}}]}`,
		"duplicate":    `{"items":[{"id":"a","source":{"file":"x.jsonl","lang":"en"}},{"id":"a","source":{"file":"x.jsonl","lang":"en"}}]}`,
		"no reference": `{"items":[{"id":"a","source":{"file":"x.ass","lang":"en"}}]}`,
		"bad window":   `{"items":[{"id":"a","source":{"file":"x.jsonl","lang":"en"},"from":"abc"}]}`,
	} {
		p := filepath.Join(dir, "m.json")
		_ = os.WriteFile(p, []byte(doc), 0o644)
		if _, err := LoadManifest(p); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if d, _ := ParseClock("1:30.5"); d.Seconds() != 90.5 {
		t.Errorf("ParseClock %v", d)
	}
}

func TestRepoRootGuard(t *testing.T) {
	if _, ok := RepoRoot("."); !ok {
		t.Error("this package is inside the repository")
	}
	if _, ok := RepoRoot(t.TempDir()); ok {
		t.Error("a temp dir is not inside the repository")
	}
}

func TestSlug(t *testing.T) {
	if Slug("qwen3:4b/instruct") != "qwen3_4b_instruct" || Slug("") != "model" {
		t.Errorf("%q", Slug("qwen3:4b/instruct"))
	}
}

func TestParseGPUSample(t *testing.T) {
	mem, temp, reasons := parseGPUSample("3393, 89, 0x0000000000000020")
	if mem != 3393 || temp != 89 || reasons&slowdownMask == 0 {
		t.Errorf("%d %d %x", mem, temp, reasons)
	}
	if _, _, r := parseGPUSample("100, 50, 0x0000000000000001"); r&slowdownMask != 0 {
		t.Error("idle (0x1) is not a slowdown")
	}
}

type downModel struct{}

func (downModel) Complete(context.Context, translate.ChatRequest) (translate.ChatResponse, error) {
	return translate.ChatResponse{}, &translate.HTTPError{Status: "502 Bad Gateway"}
}

// A dead server must not produce a result that looks like a bad model.
func TestRunItemServerDown(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteSample(dir); err != nil {
		t.Fatal(err)
	}
	m, _ := LoadManifest(filepath.Join(dir, "manifest.json"))
	o := options(t, filepath.Join(dir, "results"), "model-a")
	o.Client = downModel{}
	if _, err := RunItem(context.Background(), m, m.Items[0], o); err == nil {
		t.Fatal("expected an error")
	}
	if results, _ := LoadResults(filepath.Join(dir, "results")); len(results) != 0 {
		t.Errorf("%d results saved", len(results))
	}
}
