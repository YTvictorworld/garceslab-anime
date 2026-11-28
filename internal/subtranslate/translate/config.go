package translate

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// PromptVersion identifies the prompt and output format. It becomes part of the
// result key in phase F4; bump it with every accepted prompt change (F1 fixes 1).
const PromptVersion = 1

// Preset is a named set of batching and sampling options.
type Preset struct {
	Name string `json:"-"`
	// Model is the default model name for this preset; the -model flag wins.
	Model            string  `json:"model,omitempty"`
	BatchSize        int     `json:"batch_size"`
	MaxBatchChars    int     `json:"max_batch_chars"`
	ContextBefore    int     `json:"context_before"`
	ContextAfter     int     `json:"context_after"`
	Temperature      float64 `json:"temperature"`
	RetryTemperature float64 `json:"retry_temperature"`
	TopP             float64 `json:"top_p"`
	Seed             int     `json:"seed"`
	MaxChars         int     `json:"max_chars"`
	// Honorifics is "keep" (default) or "adapt".
	Honorifics string `json:"honorifics"`
}

// ModelHint adapts the request to a model family.
type ModelHint struct {
	Match string `json:"match"`
	// Mode is "batch" (JSON, default) or "line" (one plain-text line per request).
	Mode string `json:"mode,omitempty"`
	// LineTemplate names the prompt of line mode ("generic" or "translategemma").
	LineTemplate string `json:"line_template,omitempty"`
	// NoThink sends chat_template_kwargs.enable_thinking=false and strips <think>.
	NoThink bool `json:"no_think,omitempty"`
}

// Config is presets.json.
type Config struct {
	DefaultPreset string             `json:"default_preset"`
	Presets       map[string]*Preset `json:"presets"`
	ModelHints    []ModelHint        `json:"model_hints"`
}

//go:embed presets.json
var embeddedPresets []byte

// DefaultConfig returns the embedded presets.
func DefaultConfig() *Config {
	c, err := ParseConfig(embeddedPresets)
	if err != nil {
		panic(fmt.Sprintf("subtranslate/translate: embedded presets.json: %v", err))
	}
	return c
}

// LoadConfig reads a presets file of the same shape as presets.json.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseConfig(data)
}

// ParseConfig parses and checks a presets document.
func ParseConfig(data []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	for name, p := range c.Presets {
		p.Name = name
		if p.BatchSize < 1 || p.MaxBatchChars < 100 {
			return nil, fmt.Errorf("preset %q: batch_size and max_batch_chars are required", name)
		}
		if p.Honorifics == "" {
			p.Honorifics = "keep"
		}
		if p.Honorifics != "keep" && p.Honorifics != "adapt" {
			return nil, fmt.Errorf("preset %q: honorifics must be keep or adapt", name)
		}
		if p.MaxChars == 0 {
			p.MaxChars = 42
		}
	}
	if c.Presets[c.DefaultPreset] == nil {
		return nil, fmt.Errorf("default_preset %q is not defined", c.DefaultPreset)
	}
	for _, h := range c.ModelHints {
		if h.Mode != "" && h.Mode != "batch" && h.Mode != "line" {
			return nil, fmt.Errorf("model hint %q: mode must be batch or line", h.Match)
		}
	}
	return &c, nil
}

// Preset returns a preset by name ("" = the default).
func (c *Config) Preset(name string) (*Preset, error) {
	if name == "" {
		name = c.DefaultPreset
	}
	p := c.Presets[name]
	if p == nil {
		names := make([]string, 0, len(c.Presets))
		for n := range c.Presets {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("unknown preset %q (have %s)", name, strings.Join(names, ", "))
	}
	cp := *p
	return &cp, nil
}

// Hint returns the first hint matching the model name (zero value: batch mode).
func (c *Config) Hint(model string) ModelHint {
	m := strings.ToLower(model)
	for _, h := range c.ModelHints {
		if h.Match != "" && strings.Contains(m, strings.ToLower(h.Match)) {
			if h.Mode == "" {
				h.Mode = "batch"
			}
			return h
		}
	}
	return ModelHint{Mode: "batch"}
}
