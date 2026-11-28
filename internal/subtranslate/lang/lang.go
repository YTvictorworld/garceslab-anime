// Package lang is the language registry of subtitle translation (Garceslab Anime).
//
// Languages are data, not code: every source and target language is an entry of
// languages.json (embedded), optionally extended with an extra JSON file of the same
// shape. An entry has a canonical tag (ISO 639-1, plus a region for variants such as
// "es-419"), the canonical key it shares with internal/torrents/trackprobe ("es"), the
// rules that go into the prompt when it is the target, the notes that go into the
// prompt when it is the source, and a few function words used to tell whether a line
// is written in it.
package lang

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"seanime/internal/torrents/trackprobe"
)

// DefaultTarget is the target language when none is given: Latin American Spanish.
const DefaultTarget = "es-419"

// Language is one registry entry.
type Language struct {
	// Code is the canonical tag: ISO 639-1, with a region or script subtag for
	// variants ("es-419", "es-ES", "pt-BR").
	Code string `json:"code"`
	// Base is the canonical key of trackprobe.Normalize ("es" for every Spanish).
	Base string `json:"base"`
	// Name is the English name used in prompts and reports.
	Name       string `json:"name"`
	NativeName string `json:"native_name,omitempty"`
	// DefaultForBase picks this entry when only the base language is known
	// ("spa", "Spanish" -> es-419).
	DefaultForBase bool `json:"default_for_base,omitempty"`
	// Aliases are other tags or names that mean exactly this variant (lowercase).
	Aliases []string `json:"aliases,omitempty"`
	// TargetRules go into the system prompt when this is the target language:
	// register, vocabulary, punctuation.
	TargetRules []string `json:"target_rules,omitempty"`
	// SourceNotes go into the system prompt when this is the source language:
	// typography and register cues of subtitles written in it.
	SourceNotes []string `json:"source_notes,omitempty"`
	// HonorificsAdapt lists the target-language equivalents used when
	// honorifics are adapted instead of kept.
	HonorificsAdapt string `json:"honorifics_adapt,omitempty"`
	// Marks are characters typical of this language (¿¡ñ for Spanish).
	Marks string `json:"marks,omitempty"`
	// FunctionWords are frequent short words of the language, lowercase.
	FunctionWords []string `json:"function_words,omitempty"`
}

// Registry holds the known languages.
type Registry struct {
	byCode map[string]*Language // lowercased code and aliases
	byBase map[string]*Language // default entry per base key
	list   []*Language
}

//go:embed languages.json
var embedded []byte

type registryFile struct {
	Languages []Language `json:"languages"`
}

// Default returns the embedded registry. It panics only if languages.json is
// invalid, which TestEmbeddedRegistry catches.
func Default() *Registry {
	r, err := Parse(embedded)
	if err != nil {
		panic(fmt.Sprintf("subtranslate/lang: embedded languages.json: %v", err))
	}
	return r
}

// Parse builds a registry from a languages.json document.
func Parse(data []byte) (*Registry, error) {
	r := &Registry{byCode: map[string]*Language{}, byBase: map[string]*Language{}}
	if err := r.merge(data); err != nil {
		return nil, err
	}
	return r, nil
}

// MergeFile adds (or replaces, by code) the entries of an extra languages file.
func (r *Registry) MergeFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return r.merge(data)
}

func (r *Registry) merge(data []byte) error {
	var f registryFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	for i := range f.Languages {
		l := f.Languages[i]
		if err := l.validate(); err != nil {
			return err
		}
		for j := range l.FunctionWords {
			l.FunctionWords[j] = strings.ToLower(l.FunctionWords[j])
		}
		r.add(&l)
	}
	for _, l := range r.list {
		if r.byBase[l.Base] == nil {
			// A base with a single variant and no explicit default.
			r.byBase[l.Base] = l
		}
	}
	return nil
}

func (l *Language) validate() error {
	if l.Code == "" || l.Base == "" || l.Name == "" {
		return fmt.Errorf("language entry %q: code, base and name are required", l.Code)
	}
	if got := trackprobe.Normalize(l.Code); got != l.Base {
		return fmt.Errorf("language %q: base %q does not match the canonical key %q (internal/torrents/trackprobe)", l.Code, l.Base, got)
	}
	if len(l.FunctionWords) < 10 {
		return fmt.Errorf("language %q: needs at least 10 function words to detect untranslated lines", l.Code)
	}
	return nil
}

func (r *Registry) add(l *Language) {
	key := strings.ToLower(l.Code)
	if old := r.byCode[key]; old != nil {
		for i, x := range r.list {
			if x == old {
				r.list = append(r.list[:i], r.list[i+1:]...)
				break
			}
		}
		if r.byBase[old.Base] == old {
			delete(r.byBase, old.Base)
		}
		for k, v := range r.byCode {
			if v == old {
				delete(r.byCode, k)
			}
		}
	}
	r.list = append(r.list, l)
	r.byCode[key] = l
	for _, a := range l.Aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if _, taken := r.byCode[a]; !taken || a == key {
			r.byCode[a] = l
		}
	}
	if l.DefaultForBase {
		r.byBase[l.Base] = l
	}
}

// Resolve turns any code, IETF tag or name the server understands ("es-419",
// "spa", "Español", "fr-FR", "French") into a registry entry. An exact code or
// alias wins; otherwise the base language's default variant is used.
func (r *Registry) Resolve(value string) (*Language, error) {
	v := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(value, "_", "-")))
	if v == "" {
		return nil, fmt.Errorf("empty language")
	}
	if l := r.byCode[v]; l != nil {
		return l, nil
	}
	base := trackprobe.Normalize(v)
	if l := r.byBase[base]; l != nil {
		return l, nil
	}
	return nil, fmt.Errorf("language %q is not in the registry (known: %s); add an entry to languages.json or pass -languages", value, strings.Join(r.Codes(), ", "))
}

// Codes lists the registered codes, sorted.
func (r *Registry) Codes() []string {
	out := make([]string, 0, len(r.list))
	for _, l := range r.list {
		out = append(out, l.Code)
	}
	sort.Strings(out)
	return out
}

// Pair is a source -> target translation direction.
type Pair struct {
	Source *Language
	Target *Language
}

// String is the pair as used in reports ("en→es-419").
func (p Pair) String() string { return p.Source.Code + "→" + p.Target.Code }

// NewPair resolves both languages of a pair.
func (r *Registry) NewPair(source, target string) (Pair, error) {
	s, err := r.Resolve(source)
	if err != nil {
		return Pair{}, fmt.Errorf("source: %w", err)
	}
	t, err := r.Resolve(target)
	if err != nil {
		return Pair{}, fmt.Errorf("target: %w", err)
	}
	if s.Base == t.Base {
		return Pair{}, fmt.Errorf("source %s and target %s are the same language", s.Code, t.Code)
	}
	return Pair{Source: s, Target: t}, nil
}
