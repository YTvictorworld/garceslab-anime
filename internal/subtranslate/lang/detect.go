package lang

import (
	"strings"
	"unicode"
)

// Detector tells whether a line is written in the source or the target language of a
// pair. It only counts the function words and marks that the two languages do NOT
// share ("de", "que" and "la" are both French and Spanish, so they say nothing), which
// is enough to catch a model that returned the line untranslated. It is not a general
// language identifier.
type Detector struct {
	src, tgt           map[string]bool
	srcMarks, tgtMarks string
}

// NewDetector builds the detector of a pair.
func NewDetector(p Pair) *Detector {
	srcSet := wordSet(p.Source.FunctionWords)
	tgtSet := wordSet(p.Target.FunctionWords)
	for w := range srcSet {
		if tgtSet[w] {
			delete(srcSet, w)
			delete(tgtSet, w)
		}
	}
	return &Detector{
		src:      srcSet,
		tgt:      tgtSet,
		srcMarks: exclusiveRunes(p.Source.Marks, p.Target.Marks),
		tgtMarks: exclusiveRunes(p.Target.Marks, p.Source.Marks),
	}
}

func wordSet(words []string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[strings.ToLower(w)] = true
	}
	return m
}

func exclusiveRunes(a, b string) string {
	var sb strings.Builder
	for _, r := range a {
		if !strings.ContainsRune(b, r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// Verdict is the outcome of Detect.
type Verdict int

const (
	// Unknown: too short or no evidence either way (names, interjections).
	Unknown Verdict = iota
	Source
	Target
)

func (v Verdict) String() string {
	switch v {
	case Source:
		return "source"
	case Target:
		return "target"
	}
	return "unknown"
}

// Scores counts the evidence for each language in a plain-text line.
func (d *Detector) Scores(text string) (src, tgt, words int) {
	lower := strings.ToLower(text)
	for _, r := range lower {
		if strings.ContainsRune(d.srcMarks, r) {
			src++
		}
		if strings.ContainsRune(d.tgtMarks, r) {
			tgt++
		}
	}
	for _, w := range Words(lower) {
		words++
		if d.src[w] {
			src++
		}
		if d.tgt[w] {
			tgt++
		}
		// Elided French forms ("j'ai", "l'homme") count by their prefix too.
		if i := strings.IndexRune(w, '\''); i > 0 && i < len(w)-1 {
			if d.src[w[:i+1]] {
				src++
			}
			if d.tgt[w[:i+1]] {
				tgt++
			}
		}
	}
	return src, tgt, words
}

// Detect classifies a plain-text line (tags and placeholders removed).
func (d *Detector) Detect(text string) Verdict {
	src, tgt, words := d.Scores(text)
	switch {
	case src > tgt && (src >= 2 || words >= 3):
		return Source
	case tgt > src:
		return Target
	}
	return Unknown
}

// Words splits a line into lowercase words, keeping inner apostrophes and hyphens
// ("don't", "qu'est-ce").
func Words(s string) []string {
	s = strings.ReplaceAll(s, "’", "'")
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '\'' && r != '-'
	})
	out := fields[:0]
	for _, f := range fields {
		// Keep a trailing apostrophe of an elision ("l'", "d'") but not dashes.
		f = strings.Trim(f, "-")
		f = strings.TrimLeft(f, "'")
		if f != "" && f != "'" {
			out = append(out, f)
		}
	}
	return out
}
