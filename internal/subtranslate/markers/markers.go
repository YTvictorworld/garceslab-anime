// Package markers protects subtitle formatting from the translation model
// (Garceslab Anime: subtitle translation).
//
// Every formatting tag inside a line (an ASS override block such as {\i1}, a soft
// break \n, a hard space \h, an SRT <i>) is replaced by a numbered placeholder ⟦1⟧,
// ⟦2⟧… that the model must copy exactly once. A line break (ASS \N, a new line in
// SRT) becomes ⟦N⟧, which the model may move. Tags at the very start of a line (the
// usual {\an8\pos(…)}) never reach the model: they are kept as a prefix and put back
// in front of the translation.
package markers

import (
	"regexp"
	"strconv"
	"strings"
)

// Break is the placeholder of a line break.
const Break = "⟦N⟧"

var placeholderRe = regexp.MustCompile(`⟦(\d+|N)⟧`)

// Masked is a line ready for the model.
type Masked struct {
	// Prefix is the leading tags, restored verbatim and never sent to the model.
	Prefix string
	// Text is the line with placeholders, as sent to the model.
	Text string
	// Tags[i] is the original text of placeholder ⟦i+1⟧.
	Tags []string
	// Breaks is how many ⟦N⟧ the source has.
	Breaks int
	// BreakAs is what ⟦N⟧ is restored to (`\N` in ASS, "\n" in SRT).
	BreakAs string
}

// Builder assembles a Masked line from text, tags and breaks.
type Builder struct {
	m       Masked
	sb      strings.Builder
	started bool // a non-tag token was added
}

// NewBuilder starts a line whose breaks restore to breakAs.
func NewBuilder(breakAs string) *Builder { return &Builder{m: Masked{BreakAs: breakAs}} }

// Text adds plain text.
func (b *Builder) Text(s string) {
	if s == "" {
		return
	}
	b.started = true
	b.sb.WriteString(s)
}

// Tag adds a formatting tag. Tags before any text or break go to the prefix.
func (b *Builder) Tag(raw string) {
	if !b.started {
		b.m.Prefix += raw
		return
	}
	b.m.Tags = append(b.m.Tags, raw)
	b.sb.WriteString("⟦" + strconv.Itoa(len(b.m.Tags)) + "⟧")
}

// LineBreak adds a line break.
func (b *Builder) LineBreak() {
	b.started = true
	b.m.Breaks++
	b.sb.WriteString(Break)
}

// Done returns the masked line.
func (b *Builder) Done() Masked {
	b.m.Text = b.sb.String()
	return b.m
}

// Restore puts the tags back into a translated text. Unknown placeholders are dropped,
// so call Check first. Spaces around a moved break are tidied.
func (m Masked) Restore(out string) string {
	s := placeholderRe.ReplaceAllStringFunc(out, func(p string) string {
		inner := p[len("⟦") : len(p)-len("⟧")]
		if inner == "N" {
			return "\x00"
		}
		n, err := strconv.Atoi(inner)
		if err != nil || n < 1 || n > len(m.Tags) {
			return ""
		}
		return m.Tags[n-1]
	})
	if strings.Contains(s, "\x00") {
		parts := strings.Split(s, "\x00")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		s = strings.Join(parts, m.BreakAs)
	}
	return m.Prefix + strings.TrimSpace(s)
}

// Problem describes how a translation broke the placeholders.
type Problem struct {
	Missing    []int // numbered placeholders not in the output
	Duplicated []int // numbered placeholders more than once
	Invented   []string
	OutOfOrder bool // the numbered placeholders are not in increasing order
	// TooManyBreaks: more ⟦N⟧ than max(1, source breaks).
	TooManyBreaks bool
}

// OK is true when the output keeps every tag exactly once, in order.
func (p Problem) OK() bool {
	return len(p.Missing) == 0 && len(p.Duplicated) == 0 && len(p.Invented) == 0 && !p.OutOfOrder && !p.TooManyBreaks
}

// Check compares the placeholders of a translation with the source line.
func (m Masked) Check(out string) Problem {
	var p Problem
	seen := make(map[int]int)
	last := 0
	breaks := 0
	for _, match := range placeholderRe.FindAllStringSubmatch(out, -1) {
		if match[1] == "N" {
			breaks++
			continue
		}
		n, _ := strconv.Atoi(match[1])
		if n < 1 || n > len(m.Tags) {
			p.Invented = append(p.Invented, match[0])
			continue
		}
		seen[n]++
		if seen[n] == 2 {
			p.Duplicated = append(p.Duplicated, n)
		}
		if n < last {
			p.OutOfOrder = true
		}
		last = n
	}
	for n := 1; n <= len(m.Tags); n++ {
		if seen[n] == 0 {
			p.Missing = append(p.Missing, n)
		}
	}
	// Broken-looking placeholders the regexp does not match (⟦ 1 ⟧, ⟦1, 1⟧).
	if rest := placeholderRe.ReplaceAllString(out, ""); strings.ContainsAny(rest, "⟦⟧") {
		p.Invented = append(p.Invented, "⟦…⟧")
	}
	maxBreaks := m.Breaks
	if maxBreaks < 1 {
		maxBreaks = 1
	}
	p.TooManyBreaks = breaks > maxBreaks
	return p
}

// HasTags reports whether the line has numbered placeholders (inner tags).
func (m Masked) HasTags() bool { return len(m.Tags) > 0 }

// Plain removes every placeholder (a break becomes a space), for scoring and language
// detection.
func Plain(s string) string {
	s = placeholderRe.ReplaceAllStringFunc(s, func(p string) string {
		if p == Break {
			return " "
		}
		return ""
	})
	return strings.Join(strings.Fields(s), " ")
}
