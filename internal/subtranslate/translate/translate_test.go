package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"seanime/internal/subtranslate/lang"
	"seanime/internal/subtranslate/markers"
)

// fake is a deterministic model: it answers each batch with reply(attempt, line).
type fake struct {
	reqs  []ChatRequest
	reply func(call int, id int, text string) (string, bool) // text, include
	err   func(call int) error
}

func (f *fake) Complete(_ context.Context, req ChatRequest) (ChatResponse, error) {
	call := len(f.reqs)
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		if err := f.err(call); err != nil {
			return ChatResponse{}, err
		}
	}
	user := req.Messages[len(req.Messages)-1].Content
	if len(req.Schema) == 0 { // line mode: the text is the last paragraph
		parts := strings.Split(user, "\n\n")
		text, _ := f.reply(call, 0, parts[len(parts)-1])
		return ChatResponse{Content: text, CompletionTokens: 5, PredictedMS: 100}, nil
	}
	body := user
	if i := strings.Index(body, "\n\nYour previous answer"); i >= 0 {
		body = body[:i]
	}
	var in batchInput
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		return ChatResponse{}, err
	}
	var pairs [][]any
	for _, l := range in.Lines {
		if text, ok := f.reply(call, l.ID, l.Text); ok {
			pairs = append(pairs, []any{l.ID, text})
		}
	}
	out, _ := json.Marshal(map[string]any{"id": in.ID, "t": pairs})
	return ChatResponse{Content: string(out), PromptTokens: 100, CompletionTokens: 20, PromptMS: 50, PredictedMS: 400, Wall: time.Millisecond}, nil
}

// toSpanish is a fake translation: Spanish words, the same placeholders in the same
// order, the source's numbers, and the source length so that lines differ.
func toSpanish(text string) string {
	var b strings.Builder
	b.WriteString("Te digo que esto")
	for _, tok := range regexp.MustCompile(`⟦(\d+|N)⟧`).FindAllString(text, -1) {
		b.WriteString(" " + tok + "sí")
	}
	for _, n := range regexp.MustCompile(`\d+`).FindAllString(markers.Plain(text), -1) {
		b.WriteString(" " + n)
	}
	fmt.Fprintf(&b, " está bien, ¿verdad? %d", len(text))
	return b.String()
}

func lines(texts ...string) []Line {
	out := make([]Line, len(texts))
	for i, t := range texts {
		b := markers.NewBuilder(`\N`)
		// Minimal ASS-like masking for the test: {..} tags, \N breaks.
		for len(t) > 0 {
			switch {
			case strings.HasPrefix(t, "{"):
				j := strings.IndexByte(t, '}')
				b.Tag(t[:j+1])
				t = t[j+1:]
			case strings.HasPrefix(t, `\N`):
				b.LineBreak()
				t = t[2:]
			default:
				j := strings.IndexAny(t, "{\\")
				if j < 0 {
					j = len(t)
				}
				b.Text(t[:j])
				t = t[j:]
			}
		}
		out[i] = Line{ID: i, Start: time.Duration(i) * 2 * time.Second, End: time.Duration(i)*2*time.Second + 1500*time.Millisecond, Masked: b.Done()}
	}
	return out
}

func newTranslator(c Completer) *Translator {
	reg := lang.Default()
	pair, _ := reg.NewPair("en", "es-419")
	p, _ := DefaultConfig().Preset("")
	return &Translator{Client: c, Pair: pair, Preset: *p, Model: "fake", Hint: ModelHint{Mode: "batch"}}
}

func TestTranslateHappyPath(t *testing.T) {
	f := &fake{reply: func(_, _ int, text string) (string, bool) { return toSpanish(text), true }}
	tr := newTranslator(f)
	in := lines("Where were you? I told you to wait.", `I {\i1}swear{\i0} it wasn't\Nmy fault!`, "Huh?")
	res, st, err := tr.Translate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || st.Requests != 1 || st.Batches != 1 || st.RetriedBatches != 0 {
		t.Fatalf("res %d stats %+v", len(res), st)
	}
	if want := `Te digo que esto {\i1}sí {\i0}sí\Nsí está bien, ¿verdad? 47`; res[1].Final != want {
		t.Errorf("final %q, want %q", res[1].Final, want)
	}
	for _, r := range res {
		if r.Fallback || len(r.FirstIssues) > 0 {
			t.Errorf("line %d: %+v", r.ID, r)
		}
	}
	// The request has the system prompt, a strict schema with exactly the ids, and
	// the line durations.
	req := f.reqs[0]
	if req.Messages[0].Role != "system" || !strings.Contains(req.Messages[0].Content, "Latin American Spanish") {
		t.Error("system prompt missing the target rules")
	}
	var schema map[string]any
	if err := json.Unmarshal(req.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	tProp := schema["properties"].(map[string]any)["t"].(map[string]any)
	if tProp["minItems"].(float64) != 3 || tProp["maxItems"].(float64) != 3 {
		t.Errorf("schema items %v", tProp)
	}
	if !strings.Contains(req.Messages[1].Content, `"sec":1.5`) {
		t.Errorf("user message without durations: %s", req.Messages[1].Content)
	}
	if req.Temperature != 0.2 || req.Seed != 42 {
		t.Errorf("sampling %v %v", req.Temperature, req.Seed)
	}
}

func TestBrokenTagsRetryOnceThenAccept(t *testing.T) {
	f := &fake{reply: func(call, id int, text string) (string, bool) {
		if call == 0 && id == 1 {
			return "Yo juro que no fue mi culpa pues", true // tags dropped
		}
		return toSpanish(text), true
	}}
	tr := newTranslator(f)
	res, st, err := tr.Translate(context.Background(), lines("Where were you? I told you to wait.", `I {\i1}swear{\i0} it wasn't my fault!`))
	if err != nil {
		t.Fatal(err)
	}
	if st.Requests != 2 || st.RetriedBatches != 1 {
		t.Fatalf("stats %+v", st)
	}
	r := res[1]
	if r.Fallback || !r.Retried || !r.TagsBrokenFirst || len(r.FirstIssues) != 1 || r.FirstIssues[0] != IssueTags {
		t.Fatalf("result %+v", r)
	}
	if f.reqs[1].Temperature != 0.4 {
		t.Errorf("retry temperature %v", f.reqs[1].Temperature)
	}
	if !strings.Contains(f.reqs[1].Messages[1].Content, "id 1: keep every tag token") {
		t.Errorf("retry has no feedback: %s", f.reqs[1].Messages[1].Content)
	}
	if res[0].Retried {
		t.Error("a line that passed is not retried")
	}
}

func TestFallbackKeepsSourceLine(t *testing.T) {
	f := &fake{reply: func(_, id int, text string) (string, bool) {
		switch id {
		case 0:
			return text, true // untranslated English
		case 1:
			return "", false // missing from the answer
		}
		return "Nota: no sé traducir esto", true
	}}
	tr := newTranslator(f)
	in := lines("Where were you? I told you to wait for me.", `I {\i1}swear{\i0} it wasn't my fault!`, "We have 3 hours left.")
	res, _, err := tr.Translate(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	wantIssue := []string{IssueLanguage, IssueMissing, IssueNumbers}
	for i, r := range res {
		if !r.Fallback {
			t.Errorf("line %d should fall back", i)
		}
		if r.Final != in[i].Masked.Restore(in[i].Masked.Text) {
			t.Errorf("line %d: fallback must be the source, got %q", i, r.Final)
		}
		if !containsStr(r.FirstIssues, wantIssue[i]) {
			t.Errorf("line %d issues %v, want %s", i, r.FirstIssues, wantIssue[i])
		}
	}
}

func TestRequestErrorFallsBack(t *testing.T) {
	f := &fake{
		reply: func(_, _ int, text string) (string, bool) { return toSpanish(text), true },
		err:   func(int) error { return &HTTPError{Status: "500 Internal Server Error", Detail: "SECRET LINE TEXT"} },
	}
	var buf bytes.Buffer
	tr := newTranslator(f)
	tr.Log = zerolog.New(&buf)
	res, st, err := tr.Translate(context.Background(), lines("Where were you?"))
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].Fallback || st.FailedRequests != 2 {
		t.Fatalf("res %+v stats %+v", res[0], st)
	}
	if strings.Contains(buf.String(), "SECRET LINE TEXT") || strings.Contains(buf.String(), "Where were you") {
		t.Errorf("logs contain subtitle text without the debug flag: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "500") {
		t.Errorf("logs miss the status: %s", buf.String())
	}
}

func TestLogsHaveNoTextUnlessDebug(t *testing.T) {
	src := "Where were you? I told you to wait."
	for _, debug := range []bool{false, true} {
		var buf bytes.Buffer
		f := &fake{reply: func(_, _ int, text string) (string, bool) { return toSpanish(text), true }}
		tr := newTranslator(f)
		tr.Log = zerolog.New(&buf).Level(zerolog.DebugLevel)
		tr.DebugText = debug
		if _, _, err := tr.Translate(context.Background(), lines(src)); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(buf.String(), "Where were you"); got != debug {
			t.Errorf("debug=%v: text in logs = %v", debug, got)
		}
	}
}

func TestBatchesAndContext(t *testing.T) {
	var texts []string
	for i := 0; i < 70; i++ {
		texts = append(texts, fmt.Sprintf("Line number %d is here, I think.", i))
	}
	f := &fake{reply: func(_, _ int, text string) (string, bool) {
		return strings.Replace(strings.Replace(text, "Line number", "La línea", 1), "is here, I think.", "está aquí, creo.", 1), true
	}}
	tr := newTranslator(f)
	res, st, err := tr.Translate(context.Background(), lines(texts...))
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 70 || st.Batches != 3 { // 30 + 30 + 10
		t.Fatalf("results %d batches %d", len(res), st.Batches)
	}
	var second batchInput
	_ = json.Unmarshal([]byte(f.reqs[1].Messages[1].Content), &second)
	if len(second.Lines) != 30 || second.Lines[0].ID != 30 {
		t.Errorf("second batch %d lines from %d", len(second.Lines), second.Lines[0].ID)
	}
	if len(second.ContextBefore) != 6 || second.ContextBefore[5].Src != texts[29] || !strings.HasPrefix(second.ContextBefore[5].Tgt, "La línea 29") {
		t.Errorf("context before %+v", second.ContextBefore)
	}
	if len(second.ContextAfter) != 3 || second.ContextAfter[0] != texts[60] {
		t.Errorf("context after %+v", second.ContextAfter)
	}
	for _, r := range res {
		if r.Fallback {
			t.Fatalf("line %d fell back: %v", r.ID, r.FinalIssues)
		}
	}
}

func TestRepeatedOutputIsRejected(t *testing.T) {
	f := &fake{reply: func(_, _ int, _ string) (string, bool) { return "Esto es lo mismo siempre", true }}
	tr := newTranslator(f)
	res, _, _ := tr.Translate(context.Background(), lines("Where were you?", "I told you to wait.", "Let's go home now."))
	for _, r := range res {
		if !r.Fallback || !containsStr(r.FinalIssues, IssueRepeat) {
			t.Errorf("line %d: %+v", r.ID, r)
		}
	}
}

func TestLineMode(t *testing.T) {
	f := &fake{reply: func(_, _ int, text string) (string, bool) { return "\"" + toSpanish(text) + "\"\n", true }}
	tr := newTranslator(f)
	tr.Hint = DefaultConfig().Hint("translategemma-4b-it.Q4_K_M")
	res, st, err := tr.Translate(context.Background(), lines(`I {\i1}swear{\i0} it wasn't my fault!`, "Where were you? I told you."))
	if err != nil {
		t.Fatal(err)
	}
	if st.Requests != 2 || len(f.reqs[0].Schema) != 0 || f.reqs[0].Messages[0].Role != "user" {
		t.Fatalf("line mode requests %+v", f.reqs[0])
	}
	if !strings.Contains(f.reqs[0].Messages[0].Content, "professional English (en) to Spanish (es-419) translator") {
		t.Errorf("TranslateGemma prompt: %s", f.reqs[0].Messages[0].Content)
	}
	// Its chat template only takes structured content: raw /completion in Gemma's turns.
	if raw := f.reqs[0].RawPrompt; !strings.HasPrefix(raw, "<start_of_turn>user\nYou are a professional") || !strings.HasSuffix(raw, "<end_of_turn>\n<start_of_turn>model\n") {
		t.Errorf("raw prompt %q", raw)
	}
	if res[0].Fallback || !strings.HasPrefix(res[0].Final, `Te digo que esto {\i1}sí {\i0}sí está bien`) {
		t.Errorf("result %+v", res[0])
	}
}

func TestContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fake{reply: func(_, _ int, text string) (string, bool) { return toSpanish(text), true }}
	_, _, err := newTranslator(f).Translate(ctx, lines("Where were you?"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func TestParseBatchReply(t *testing.T) {
	id, out, err := parseBatchReply("<think>\nhmm\n</think>\n```json\n{\"id\":\"b1\",\"t\":[[0,\"Hola\"],[1,\"Adiós\"],[1,\"dup\"],[\"x\",\"bad\"]]}\n```")
	if err != nil || id != "b1" || len(out) != 2 || out[0] != "Hola" || out[1] != "Adiós" {
		t.Fatalf("%q %v %v", id, out, err)
	}
	if _, _, err := parseBatchReply("Sorry, I can't."); err == nil {
		t.Error("non-JSON must fail")
	}
}

func TestConfig(t *testing.T) {
	c := DefaultConfig()
	if c.DefaultPreset != "equilibrado" {
		t.Errorf("default preset %s", c.DefaultPreset)
	}
	for _, name := range []string{"rapido", "equilibrado", "calidad"} {
		if _, err := c.Preset(name); err != nil {
			t.Error(err)
		}
	}
	if _, err := c.Preset("nope"); err == nil {
		t.Error("unknown preset")
	}
	cases := map[string]ModelHint{
		"Qwen_Qwen3-4B-Instruct-2507-Q4_K_M": {Mode: "batch"},
		"Qwen3.5-4B.Q5_K_M":                  {Mode: "batch", NoThink: true},
		"qwen3-8b":                           {Mode: "batch", NoThink: true},
		"translategemma-4b-it.Q4_K_M":        {Mode: "line", LineTemplate: "translategemma", RawTemplate: "gemma"},
		"gemma-3-4b-it-qat-Q4_0":             {Mode: "batch"},
	}
	for model, want := range cases {
		got := c.Hint(model)
		if got.Mode != want.Mode || got.NoThink != want.NoThink || got.LineTemplate != want.LineTemplate || got.RawTemplate != want.RawTemplate {
			t.Errorf("Hint(%s) = %+v, want %+v", model, got, want)
		}
	}
	if _, err := ParseConfig([]byte(`{"default_preset":"x","presets":{}}`)); err == nil {
		t.Error("missing default preset must fail")
	}
}

func TestSystemPromptPerPair(t *testing.T) {
	reg := lang.Default()
	for _, c := range []struct {
		src, tgt string
		want     []string
		not      []string
	}{
		{"fr", "es-419", []string{"from French into Latin American Spanish", "ABOUT THE SOURCE (French)", "never 'vosotros'", "Keep Japanese honorifics"}, nil},
		{"en", "es-ES", []string{"Castilian Spanish", "'vosotros'"}, []string{"ABOUT THE SOURCE"}},
		{"en", "fr", []string{"into French", "« »"}, nil},
	} {
		p, err := reg.NewPair(c.src, c.tgt)
		if err != nil {
			t.Fatal(err)
		}
		s, err := SystemPrompt(PromptInput{Source: p.Source, Target: p.Target, MaxChars: 42, HonorificsRule: honorificsRule("keep", p.Target),
			Glossary: []GlossaryEntry{{"Lantern Guild", "Lantern Guild"}}})
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range append(c.want, "Lantern Guild → Lantern Guild") {
			if !strings.Contains(s, w) {
				t.Errorf("%s: prompt lacks %q", p, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(s, w) {
				t.Errorf("%s: prompt has %q", p, w)
			}
		}
	}
	p, _ := reg.NewPair("en", "es-419")
	if r := honorificsRule("adapt", p.Target); !strings.Contains(r, "señor/señora") {
		t.Errorf("adapt rule %q", r)
	}
}

// A model that thinks until max_tokens answers with an empty content.
type thinker struct{}

func (thinker) Complete(context.Context, ChatRequest) (ChatResponse, error) {
	return ChatResponse{Content: "<think>\nThe user wants", CompletionTokens: 600}, nil
}

func TestThinkingModelIsReported(t *testing.T) {
	var buf bytes.Buffer
	tr := newTranslator(thinker{})
	tr.Log = zerolog.New(&buf)
	res, st, _ := tr.Translate(context.Background(), lines("Where were you?"))
	if !res[0].Fallback || st.FailedRequests != 2 || !strings.Contains(buf.String(), "probably thinking") {
		t.Fatalf("res %+v stats %+v log %s", res[0], st, buf.String())
	}
}
