package translate

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"seanime/internal/subtranslate/lang"
	"seanime/internal/subtranslate/markers"
)

// Issue codes of the line validation (blocking: a line with any of them is retried,
// then falls back to the source).
const (
	IssueMissing      = "missing"      // the reply has no item for this id
	IssueEmpty        = "empty"        // empty translation
	IssueTags         = "tags"         // placeholders lost, duplicated, invented or reordered
	IssueBreaks       = "breaks"       // more ⟦N⟧ than allowed
	IssueLanguage     = "language"     // still in the source language
	IssueUntranslated = "untranslated" // identical to the source
	IssueLength       = "length"       // outside 0.5–2.2× the source length
	IssueNumbers      = "numbers"      // a number of the source is missing
	IssueNote         = "note"         // translator note or explanation
	IssueContext      = "context"      // copied a context line
	IssueRepeat       = "repeat"       // same text for several different lines (loop)
)

var (
	numberRe = regexp.MustCompile(`\d+`)
	noteRe   = regexp.MustCompile(`(?i)(\bnote\s*:|\(tl|\btl\s*:|\btranslator|\bn\.?\s?d\.?\s?t\b|\btraducci[oó]n\s*:)`)
)

// checkLine runs the per-line checks. ctx holds the plain context lines of the batch.
func checkLine(det *lang.Detector, src markers.Masked, out string, ctx map[string]bool) []string {
	var issues []string
	plainOut := markers.Plain(out)
	plainSrc := markers.Plain(src.Text)
	if strings.TrimSpace(plainOut) == "" && strings.TrimSpace(plainSrc) != "" {
		return []string{IssueEmpty}
	}
	p := src.Check(out)
	if len(p.Missing) > 0 || len(p.Duplicated) > 0 || len(p.Invented) > 0 || p.OutOfOrder {
		issues = append(issues, IssueTags)
	}
	if p.TooManyBreaks {
		issues = append(issues, IssueBreaks)
	}
	if det.Detect(plainOut) == lang.Source {
		issues = append(issues, IssueLanguage)
	} else if strings.EqualFold(normalize(plainOut), normalize(plainSrc)) && len(lang.Words(plainSrc)) > 3 {
		// Short identical lines are fine: names, "OK", "Huh?".
		issues = append(issues, IssueUntranslated)
	}
	if ns, no := utf8.RuneCountInString(plainSrc), utf8.RuneCountInString(plainOut); ns >= 12 {
		r := float64(no) / float64(ns)
		if r < 0.5 || r > 2.2 {
			issues = append(issues, IssueLength)
		}
	}
	for _, n := range numberRe.FindAllString(plainSrc, -1) {
		if !strings.Contains(plainOut, n) {
			issues = append(issues, IssueNumbers)
			break
		}
	}
	if noteRe.MatchString(plainOut) && !noteRe.MatchString(plainSrc) {
		issues = append(issues, IssueNote)
	}
	if ctx[normalize(plainOut)] && !strings.EqualFold(normalize(plainOut), normalize(plainSrc)) && len(lang.Words(plainOut)) > 2 {
		issues = append(issues, IssueContext)
	}
	return issues
}

func normalize(s string) string {
	return strings.Join(lang.Words(s), " ")
}

// TagsBroken is true when the output does not keep the tags of the source exactly
// (the "broken tags" metric).
func TagsBroken(src markers.Masked, out string) bool {
	p := src.Check(out)
	return len(p.Missing) > 0 || len(p.Duplicated) > 0 || len(p.Invented) > 0 || p.OutOfOrder || p.TooManyBreaks
}
