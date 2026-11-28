package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"seanime/internal/subtranslate/lang"
)

// The system prompt is the same for every batch of a job (pair, preset, glossary), so
// llama-server can reuse its KV cache between requests. It is in English because
// small models follow rules better in English; the language-specific rules come from
// the registry (internal/subtranslate/lang/languages.json).
var systemTmpl = template.Must(template.New("system").Parse(`You are a professional anime subtitle translator. Translate from {{.Source.Name}} into {{.Target.Name}}.
{{range .Target.TargetRules}}- {{.}}
{{end}}{{if .Source.SourceNotes}}
ABOUT THE SOURCE ({{.Source.Name}})
{{range .Source.SourceNotes}}- {{.}}
{{end}}{{end}}
OUTPUT
- Reply with ONLY this JSON: {"id":"<request id>","t":[[<id>,"<translation>"], ...]}
- Exactly as many pairs as input lines, the same ids as the input, in the same order. One input line = one output line. Never merge two lines, never split one, never skip one, never add one.
- Do not translate the "context_before" and "context_after" lines; they are only there so you understand the scene.

TAGS
- Tokens like ⟦1⟧, ⟦2⟧ are formatting tags (italics, colour). Copy each one exactly once, around the same words (an opening tag before the words it styles, its closing tag after them). Never invent new tags.
- Example: in "I ⟦1⟧really⟦2⟧ mean it!" the translation must put ⟦1⟧ before and ⟦2⟧ after the words that translate "really". A translation that drops a tag is rejected.
- ⟦N⟧ is a line break. Keep at most one; you may move it to a natural place.

STYLE
- Natural spoken {{.Target.Name}} as in a good fansub or a streaming subtitle: short, clear, idiomatic, not word for word. Keep each line about as long as the source (max ~{{.MaxChars}} characters per row).
- Keep the tone: casual stays casual, rude stays rude. Do not censor or soften insults and swearing, and do not add any that are not in the source.
- Honorifics: {{.HonorificsRule}}
- Never translate personal names; keep their order and romanization. Names of groups, places, techniques and titles: use the glossary; without a glossary entry keep them as in the source.
- English words the characters say inside Japanese speech ("Nice!", "OK", "Let's go!") are translated like the rest.
- Interjections and sounds (Huh?, Tch, Eh ?, Ah!): use the natural {{.Target.Name}} equivalent, or keep them if there is none.
- Puns and jokes that only work in the source language: translate the intent, with an equivalent joke if natural.
- No notes, no translator comments, no explanations, no romanization, no quotes around lines.
{{if .Glossary}}
GLOSSARY ({{.Source.Name}} → {{.Target.Name}})
{{range .Glossary}}- {{.Source}} → {{.Target}}
{{end}}{{end}}{{if .Series}}SERIES: {{.Series}}
{{end}}`))

// The line-mode prompts: one subtitle per request, plain text out. "translategemma"
// is the prompt from the TranslateGemma model card, plus the tag rule.
var lineTmpls = map[string]*template.Template{
	"generic": template.Must(template.New("generic").Parse(`Translate this anime subtitle line from {{.Source.Name}} into {{.Target.Name}}. Keep tokens like ⟦1⟧ and ⟦N⟧ exactly as they are. Reply with the translation only.

{{.Text}}`)),
	"translategemma": template.Must(template.New("translategemma").Parse(`You are a professional {{.Source.Name}} ({{.Source.Code}}) to {{.Target.Name}} ({{.Target.Code}}) translator. Your goal is to accurately convey the meaning and nuances of the original {{.Source.Name}} text while adhering to {{.Target.Name}} grammar, vocabulary, and cultural sensitivities. Keep tokens like ⟦1⟧ and ⟦N⟧ exactly as they are.
Produce only the {{.Target.Name}} translation, without any additional explanations or commentary. Please translate the following {{.Source.Name}} text into {{.Target.Name}}:


{{.Text}}`)),
}

// GlossaryEntry is a fixed translation of a name or term.
type GlossaryEntry struct {
	Source string `json:"src"`
	Target string `json:"tgt"`
}

// PromptInput is everything the system prompt depends on.
type PromptInput struct {
	Source, Target *lang.Language
	MaxChars       int
	HonorificsRule string
	Glossary       []GlossaryEntry
	Series         string
}

func honorificsRule(mode string, target *lang.Language) string {
	if mode == "adapt" {
		eq := target.HonorificsAdapt
		if eq == "" {
			eq = "the usual equivalents"
		}
		return "Drop honorifics or adapt them naturally (" + eq + ") when they carry meaning."
	}
	return "Keep Japanese honorifics and kinship terms as written (-san, -kun, -chan, -sama, -senpai, -sensei, onii-chan, nee-san)."
}

// SystemPrompt renders the batch-mode system prompt.
func SystemPrompt(in PromptInput) (string, error) {
	var b bytes.Buffer
	if err := systemTmpl.Execute(&b, in); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

// LinePrompt renders a line-mode prompt.
func LinePrompt(name string, pair lang.Pair, text string) (string, error) {
	t := lineTmpls[name]
	if t == nil {
		t = lineTmpls["generic"]
	}
	var b bytes.Buffer
	err := t.Execute(&b, struct {
		Source, Target *lang.Language
		Text           string
	}{pair.Source, pair.Target, text})
	return b.String(), err
}

// batchInput is the user message of a batch.
type batchInput struct {
	ID            string        `json:"id"`
	Source        string        `json:"source"`
	Target        string        `json:"target"`
	ContextBefore []contextPair `json:"context_before,omitempty"`
	Lines         []inputLine   `json:"lines"`
	ContextAfter  []string      `json:"context_after,omitempty"`
}

type contextPair struct {
	Src string `json:"src"`
	Tgt string `json:"tgt"`
}

type inputLine struct {
	ID   int     `json:"id"`
	Who  string  `json:"who,omitempty"`
	Sec  float64 `json:"sec"`
	Text string  `json:"text"`
}

func userMessage(in batchInput) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(in); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

// batchSchema is the JSON schema of a batch answer: {"id": <request id>, "t": [[id,
// "text"], …]} with exactly the ids of the batch, in order. llama-server compiles it
// into a grammar, so the model cannot add, drop or reorder lines. ("items": false is
// not used: llama-server's schema converter rejects boolean schemas; maxItems already
// closes the arrays.)
func batchSchema(requestID string, ids []int) json.RawMessage {
	items := make([]any, len(ids))
	for i, id := range ids {
		items[i] = map[string]any{
			"type":        "array",
			"prefixItems": []any{map[string]any{"const": id}, map[string]any{"type": "string"}},
			"minItems":    2,
			"maxItems":    2,
		}
	}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"const": requestID},
			"t": map[string]any{
				"type":        "array",
				"prefixItems": items,
				"minItems":    len(ids),
				"maxItems":    len(ids),
			},
		},
		"required":             []string{"id", "t"},
		"additionalProperties": false,
	}
	data, _ := json.Marshal(schema)
	return data
}

// parseBatchReply reads a batch answer, tolerating <think> blocks and code fences.
func parseBatchReply(content string) (string, map[int]string, error) {
	s := stripThink(content)
	if i := strings.IndexByte(s, '{'); i >= 0 {
		s = s[i:]
	}
	if j := strings.LastIndexByte(s, '}'); j >= 0 {
		s = s[:j+1]
	}
	var reply struct {
		ID string            `json:"id"`
		T  []json.RawMessage `json:"t"`
	}
	if err := json.Unmarshal([]byte(s), &reply); err != nil {
		return "", nil, fmt.Errorf("reply is not the expected JSON: %w", err)
	}
	out := make(map[int]string, len(reply.T))
	for _, raw := range reply.T {
		var pair []any
		if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
			continue
		}
		id, ok1 := pair[0].(float64)
		text, ok2 := pair[1].(string)
		if ok1 && ok2 {
			if _, dup := out[int(id)]; !dup {
				out[int(id)] = text
			}
		}
	}
	return reply.ID, out, nil
}

func stripThink(s string) string {
	for {
		i := strings.Index(s, "<think>")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "</think>")
		if j < 0 {
			s = s[:i]
			break
		}
		s = s[:i] + s[i+j+len("</think>"):]
	}
	return strings.TrimSpace(s)
}
