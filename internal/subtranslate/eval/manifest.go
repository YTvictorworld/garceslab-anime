package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"seanime/internal/subtranslate/translate"
)

// Manifest lists the evaluation items. It lives in the eval directory, outside the
// repository, next to the subtitle files it points to.
type Manifest struct {
	// Target is the default target language of items without a reference
	// language (default es-419).
	Target string `json:"target,omitempty"`
	Items  []Item `json:"items"`

	dir string
}

// Item is one source file to translate, with its reference.
type Item struct {
	ID string `json:"id"`
	// Source is the file to translate and its language ("en", "fr"…).
	Source FileRef `json:"source"`
	// Reference is the reference translation of an episode (same episode, same
	// timing family). Line sets (.jsonl) carry their references inline instead.
	Reference *FileRef `json:"reference,omitempty"`
	// Target overrides the target language (default: the reference's, else the
	// manifest's).
	Target string `json:"target,omitempty"`
	// Series (title and a one-line synopsis) and Glossary go into the prompt.
	Series   string                    `json:"series,omitempty"`
	Glossary []translate.GlossaryEntry `json:"glossary,omitempty"`
	// From/To limit an episode to a time window ("1:30", "12:00"), to keep runs short.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Tags are free labels for filtering (-only).
	Tags []string `json:"tags,omitempty"`
}

// FileRef is a subtitle file and its language.
type FileRef struct {
	File string `json:"file"`
	Lang string `json:"lang"`
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// LoadManifest reads and checks manifest.json.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	m.dir = filepath.Dir(path)
	seen := map[string]bool{}
	for i, it := range m.Items {
		if !idRe.MatchString(it.ID) {
			return nil, fmt.Errorf("item %d: id %q must be letters, digits, '.', '_' or '-'", i+1, it.ID)
		}
		if seen[it.ID] {
			return nil, fmt.Errorf("item %q: duplicate id", it.ID)
		}
		seen[it.ID] = true
		if it.Source.File == "" || it.Source.Lang == "" {
			return nil, fmt.Errorf("item %q: source.file and source.lang are required", it.ID)
		}
		if it.Reference == nil && !strings.HasSuffix(strings.ToLower(it.Source.File), ".jsonl") {
			return nil, fmt.Errorf("item %q: an episode needs a reference (only .jsonl line sets carry it inline)", it.ID)
		}
		for _, s := range []string{it.From, it.To} {
			if s != "" {
				if _, err := ParseClock(s); err != nil {
					return nil, fmt.Errorf("item %q: %w", it.ID, err)
				}
			}
		}
	}
	return &m, nil
}

// Path resolves a manifest-relative path.
func (m *Manifest) Path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(m.dir, p)
}

// TargetOf is the item's target language.
func (m *Manifest) TargetOf(it Item) string {
	switch {
	case it.Target != "":
		return it.Target
	case it.Reference != nil && it.Reference.Lang != "":
		return it.Reference.Lang
	case m.Target != "":
		return m.Target
	}
	return "es-419"
}

// ParseClock reads "90", "1:30", "0:01:30" or "1:30.5" as a duration.
func ParseClock(s string) (time.Duration, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) > 3 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	var total float64
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("bad time %q", s)
		}
		total = total*60 + v
	}
	return time.Duration(total * float64(time.Second)), nil
}

// Slug makes a model name safe for a file name ("qwen3:4b" -> "qwen3_4b").
func Slug(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "model"
	}
	return out
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
