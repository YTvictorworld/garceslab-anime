// Package translate translates subtitle lines with a local LLM (Garceslab Anime:
// subtitle translation): batches with a little context, a JSON-schema answer,
// validation of every line and one retry, falling back to the source line so that no
// line is ever lost.
//
// Phase F1 (the evaluation tool) uses it as is; phase F2 adds the rest of the retry
// ladder (split batch, line by line, without inner tags) and the glossary memory.
package translate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"seanime/internal/subtranslate/lang"
	"seanime/internal/subtranslate/markers"
)

// Line is one subtitle line to translate.
type Line struct {
	ID      int
	Start   time.Duration
	End     time.Duration
	Speaker string
	Masked  markers.Masked
}

// Result is the outcome for one line.
type Result struct {
	ID int
	// Text is the accepted translation with placeholders (the source on fallback).
	Text string
	// Final is Text with the tags restored.
	Final string
	// Fallback: no attempt passed validation; the source line was kept.
	Fallback bool
	// FirstIssues are the validation issues of the first attempt (nil = passed).
	FirstIssues []string
	// FinalIssues are the issues of the last attempt when the line fell back.
	FinalIssues []string
	// TagsBrokenFirst: the first answer broke this line's placeholders.
	TagsBrokenFirst bool
	// Retried: the line was sent again.
	Retried bool
}

// Stats are the costs of a run.
type Stats struct {
	Requests         int
	Batches          int
	RetriedBatches   int
	FailedRequests   int
	PromptTokens     int
	CachedTokens     int
	CompletionTokens int
	PromptMS         float64
	PredictedMS      float64
	Wall             time.Duration
}

func (s *Stats) add(r ChatResponse) {
	s.Requests++
	s.PromptTokens += r.PromptTokens
	s.CachedTokens += r.CachedTokens
	s.CompletionTokens += r.CompletionTokens
	s.PromptMS += r.PromptMS
	s.PredictedMS += r.PredictedMS
	s.Wall += r.Wall
}

// Add merges other into s.
func (s *Stats) Add(o Stats) {
	s.Requests += o.Requests
	s.Batches += o.Batches
	s.RetriedBatches += o.RetriedBatches
	s.FailedRequests += o.FailedRequests
	s.PromptTokens += o.PromptTokens
	s.CachedTokens += o.CachedTokens
	s.CompletionTokens += o.CompletionTokens
	s.PromptMS += o.PromptMS
	s.PredictedMS += o.PredictedMS
	s.Wall += o.Wall
}

// Translator translates lines of one language pair with one model and preset.
type Translator struct {
	Client Completer
	Pair   lang.Pair
	Preset Preset
	Model  string
	Hint   ModelHint
	// Glossary and Series go into the system prompt.
	Glossary []GlossaryEntry
	Series   string
	Log      zerolog.Logger
	// DebugText allows subtitle text, prompts and answers in the logs. Off by default:
	// logs never contain subtitle text otherwise.
	DebugText bool

	det    *lang.Detector
	system string
	seq    int
}

func (t *Translator) init() error {
	if t.det != nil {
		return nil
	}
	if t.Pair.Source == nil || t.Pair.Target == nil {
		return errors.New("translator: language pair not set")
	}
	t.det = lang.NewDetector(t.Pair)
	if t.Hint.Mode == "" {
		t.Hint.Mode = "batch"
	}
	sys, err := SystemPrompt(PromptInput{
		Source:         t.Pair.Source,
		Target:         t.Pair.Target,
		MaxChars:       t.Preset.MaxChars,
		HonorificsRule: honorificsRule(t.Preset.Honorifics, t.Pair.Target),
		Glossary:       t.Glossary,
		Series:         t.Series,
	})
	t.system = sys
	return err
}

// Translate translates all lines in order and returns one result per line, in the same
// order. It only returns an error when ctx is cancelled; failed requests make their
// lines fall back to the source.
func (t *Translator) Translate(ctx context.Context, lines []Line) ([]Result, Stats, error) {
	if err := t.init(); err != nil {
		return nil, Stats{}, err
	}
	var stats Stats
	results := make([]Result, 0, len(lines))
	var history []contextPair
	for _, b := range t.batches(lines) {
		if err := ctx.Err(); err != nil {
			return results, stats, err
		}
		var after []string
		end := b.end
		for i := end; i < len(lines) && len(after) < t.Preset.ContextAfter; i++ {
			after = append(after, lines[i].Masked.Text)
		}
		before := history
		if n := t.Preset.ContextBefore; len(before) > n {
			before = before[len(before)-n:]
		}
		res, st := t.translateBatch(ctx, lines[b.start:b.end], before, after)
		stats.Add(st)
		for i, r := range res {
			results = append(results, r)
			if !r.Fallback {
				history = append(history, contextPair{Src: lines[b.start+i].Masked.Text, Tgt: r.Text})
			}
		}
		if len(history) > 32 {
			history = history[len(history)-32:]
		}
	}
	if err := ctx.Err(); err != nil {
		return results, stats, err
	}
	return results, stats, nil
}

type span struct{ start, end int }

// batches cuts lines into consecutive batches of at most BatchSize lines and
// MaxBatchChars characters (line mode: one line each).
func (t *Translator) batches(lines []Line) []span {
	var out []span
	if t.Hint.Mode == "line" {
		for i := range lines {
			out = append(out, span{i, i + 1})
		}
		return out
	}
	start, chars := 0, 0
	for i, l := range lines {
		n := utf8.RuneCountInString(l.Masked.Text)
		if i > start && (i-start >= t.Preset.BatchSize || chars+n > t.Preset.MaxBatchChars) {
			out = append(out, span{start, i})
			start, chars = i, 0
		}
		chars += n
	}
	if start < len(lines) {
		out = append(out, span{start, len(lines)})
	}
	return out
}

// translateBatch: one attempt, then one retry at RetryTemperature for the lines that
// failed; lines that still fail keep the source.
func (t *Translator) translateBatch(ctx context.Context, batch []Line, before []contextPair, after []string) ([]Result, Stats) {
	var stats Stats
	stats.Batches = 1
	ctxLines := map[string]bool{}
	for _, c := range before {
		ctxLines[normalize(markers.Plain(c.Src))] = true
		ctxLines[normalize(markers.Plain(c.Tgt))] = true
	}
	for _, a := range after {
		ctxLines[normalize(markers.Plain(a))] = true
	}

	results := make([]Result, len(batch))
	for i, l := range batch {
		results[i] = Result{ID: l.ID}
	}
	pending := make([]int, len(batch)) // indexes into batch still without a valid translation
	for i := range batch {
		pending[i] = i
	}
	for attempt := 0; attempt < 2 && len(pending) > 0; attempt++ {
		if ctx.Err() != nil {
			break
		}
		temp := t.Preset.Temperature
		if attempt > 0 {
			temp = t.Preset.RetryTemperature
			stats.RetriedBatches = 1
		}
		// The retry sends the same batch (same context, so the lines that passed give
		// the model the same scene) and only takes the lines that still need it.
		var feedback map[int][]string
		if attempt > 0 {
			feedback = map[int][]string{}
			for _, i := range pending {
				feedback[batch[i].ID] = results[i].FinalIssues
			}
		}
		outs, st, err := t.request(ctx, batch, before, after, temp, feedback)
		stats.Add(st)
		if err != nil {
			stats.FailedRequests++
			t.logRequestError(err, attempt)
		}
		if t.DebugText {
			for _, i := range pending {
				t.Log.Debug().Int("attempt", attempt+1).Int("id", batch[i].ID).Str("out", outs[batch[i].ID]).Msg("subtranslate: answer")
			}
		}
		var still []int
		for _, i := range pending {
			out, ok := outs[batch[i].ID]
			var issues []string
			if !ok {
				issues = []string{IssueMissing}
			} else {
				issues = checkLine(t.det, batch[i].Masked, out, ctxLines)
			}
			if attempt == 0 {
				results[i].FirstIssues = issues
				results[i].TagsBrokenFirst = ok && TagsBroken(batch[i].Masked, out)
			} else {
				results[i].Retried = true
			}
			if len(issues) == 0 {
				results[i].Text = out
				results[i].FinalIssues = nil
				continue
			}
			results[i].FinalIssues = issues
			still = append(still, i)
		}
		pending = still
	}
	// A model in a loop gives the same text for different lines.
	t.markRepeats(batch, results)
	for i := range results {
		if results[i].Text == "" {
			results[i].Fallback = true
			results[i].Text = batch[i].Masked.Text
		}
		results[i].Final = batch[i].Masked.Restore(results[i].Text)
	}
	if t.DebugText {
		for i, r := range results {
			t.Log.Debug().Int("id", r.ID).Str("src", batch[i].Masked.Text).Str("out", r.Text).Strs("issues", r.FirstIssues).Bool("fallback", r.Fallback).Msg("subtranslate: line")
		}
	}
	return results, stats
}

func (t *Translator) markRepeats(batch []Line, results []Result) {
	count := map[string]map[string]bool{}
	for i, r := range results {
		if r.Text == "" {
			continue
		}
		k := normalize(markers.Plain(r.Text))
		if len(lang.Words(k)) < 3 {
			continue
		}
		if count[k] == nil {
			count[k] = map[string]bool{}
		}
		count[k][normalize(markers.Plain(batch[i].Masked.Text))] = true
	}
	for i, r := range results {
		if r.Text == "" {
			continue
		}
		if len(count[normalize(markers.Plain(r.Text))]) >= 3 {
			results[i].FinalIssues = append(results[i].FinalIssues, IssueRepeat)
			results[i].Text = ""
		}
	}
}

func (t *Translator) logRequestError(err error, attempt int) {
	ev := t.Log.Warn().Int("attempt", attempt+1).Str("model", t.Model).Str("pair", t.Pair.String())
	var he *HTTPError
	if errors.As(err, &he) {
		ev = ev.Str("status", he.Status)
		if t.DebugText {
			ev = ev.Str("detail", he.Detail)
		}
	} else if t.DebugText || !errors.Is(err, context.Canceled) {
		// Transport and JSON errors never contain subtitle text.
		ev = ev.Str("error", err.Error())
	}
	ev.Msg("subtranslate: request failed")
}

// request sends one batch (or one line in line mode) and returns id -> text.
// feedback (retry only) lists the issues of the previous answer per line id; it is
// added to the request so the model knows what to fix (issue codes, no text).
func (t *Translator) request(ctx context.Context, batch []Line, before []contextPair, after []string, temp float64, feedback map[int][]string) (map[int]string, Stats, error) {
	var stats Stats
	if t.Hint.Mode == "line" {
		out := map[int]string{}
		for _, l := range batch {
			prompt, err := LinePrompt(t.Hint.LineTemplate, t.Pair, l.Masked.Text)
			if err != nil {
				return out, stats, err
			}
			req := ChatRequest{
				Model:       t.Model,
				Messages:    []Message{{Role: "user", Content: prompt}},
				Temperature: temp, TopP: t.Preset.TopP, Seed: t.Preset.Seed,
				MaxTokens: 64 + 4*utf8.RuneCountInString(l.Masked.Text),
				NoThink:   t.Hint.NoThink,
			}
			if wrap := rawPrompts[t.Hint.RawTemplate]; wrap != nil {
				req.RawPrompt = wrap(prompt)
			}
			resp, err := t.Client.Complete(ctx, req)
			if err != nil {
				return out, stats, err
			}
			stats.add(resp)
			out[l.ID] = cleanLineReply(resp.Content)
		}
		return out, stats, nil
	}

	t.seq++
	reqID := "b" + strconv.Itoa(t.seq)
	in := batchInput{ID: reqID, Source: t.Pair.Source.Code, Target: t.Pair.Target.Code, ContextBefore: before, ContextAfter: after}
	ids := make([]int, len(batch))
	chars := 0
	for i, l := range batch {
		ids[i] = l.ID
		sec := (l.End - l.Start).Seconds()
		in.Lines = append(in.Lines, inputLine{ID: l.ID, Who: l.Speaker, Sec: float64(int(sec*10)) / 10, Text: l.Masked.Text})
		chars += utf8.RuneCountInString(l.Masked.Text)
	}
	user, err := userMessage(in)
	if err != nil {
		return nil, stats, err
	}
	if note := retryNote(batch, feedback); note != "" {
		user += "\n\n" + note
	}
	resp, err := t.Client.Complete(ctx, ChatRequest{
		Model:       t.Model,
		Messages:    []Message{{Role: "system", Content: t.system}, {Role: "user", Content: user}},
		Temperature: temp, TopP: t.Preset.TopP, Seed: t.Preset.Seed,
		// Spanish is ~1.2× English; JSON adds ~6 tokens per line.
		MaxTokens: 256 + chars + 12*len(batch),
		Schema:    batchSchema(reqID, ids),
		NoThink:   t.Hint.NoThink,
	})
	if err != nil {
		return nil, stats, err
	}
	stats.add(resp)
	if strings.TrimSpace(stripThink(resp.Content)) == "" && resp.CompletionTokens > 0 {
		return nil, stats, errors.New("empty answer after generating tokens: the model is probably thinking (start llama-server with --reasoning-budget 0)")
	}
	gotID, out, err := parseBatchReply(resp.Content)
	if err != nil {
		return nil, stats, err
	}
	if gotID != reqID {
		return nil, stats, fmt.Errorf("reply id %q does not match the request", strings.TrimSpace(gotID))
	}
	return out, stats, nil
}

// cleanLineReply trims what line-mode models add around a translation.
func cleanLineReply(s string) string {
	s = stripThink(s)
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 && !strings.Contains(s[:i], markers.Break) {
		// Multi-line answers: a subtitle is one line; ⟦N⟧ carries breaks.
		rest := strings.TrimSpace(s[i+1:])
		if rest != "" {
			s = s[:i] + markers.Break + strings.ReplaceAll(rest, "\n", " ")
		}
	}
	return strings.Trim(s, "\"“”")
}

var issueHints = map[string]string{
	IssueMissing:      "the line was missing from your answer",
	IssueEmpty:        "the translation was empty",
	IssueTags:         "keep every tag token exactly once, in the same order",
	IssueBreaks:       "use at most one ⟦N⟧",
	IssueLanguage:     "it was not translated into the target language",
	IssueUntranslated: "it was copied without translating",
	IssueLength:       "keep about the same length as the source",
	IssueNumbers:      "keep the numbers of the source",
	IssueNote:         "no notes or explanations",
	IssueContext:      "it repeated a context line instead of translating this one",
	IssueRepeat:       "several lines got the same text",
}

// retryNote tells the model what was wrong with its previous answer.
func retryNote(batch []Line, feedback map[int][]string) string {
	if len(feedback) == 0 {
		return ""
	}
	var parts []string
	for _, l := range batch {
		issues, ok := feedback[l.ID]
		if !ok {
			continue
		}
		var hints []string
		for _, is := range issues {
			if h := issueHints[is]; h != "" {
				hints = append(hints, h)
			}
		}
		if is := l.Masked.Tags; len(is) > 0 && containsStr(issues, IssueTags) {
			var toks []string
			for n := range is {
				toks = append(toks, "⟦"+strconv.Itoa(n+1)+"⟧")
			}
			hints = append(hints, "required tags: "+strings.Join(toks, " "))
		}
		parts = append(parts, fmt.Sprintf("id %d: %s", l.ID, strings.Join(hints, "; ")))
	}
	return "Your previous answer was rejected for these lines; translate all lines again and fix them: " + strings.Join(parts, ". ") + "."
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
