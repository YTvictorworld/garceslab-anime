package ass

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"seanime/internal/subtranslate/markers"
)

// Mask turns an event's Text into a line for the model: override blocks {…}, the soft
// break \n and the hard space \h become numbered placeholders, \N becomes ⟦N⟧ and the
// leading tags are kept aside (see package markers).
func Mask(text string) markers.Masked {
	b := markers.NewBuilder(`\N`)
	var plain strings.Builder
	flush := func() {
		b.Text(plain.String())
		plain.Reset()
	}
	for i := 0; i < len(text); {
		switch {
		case text[i] == '{':
			j := strings.IndexByte(text[i:], '}')
			if j < 0 { // unterminated block: libass shows it as text
				plain.WriteString(text[i:])
				i = len(text)
				continue
			}
			flush()
			b.Tag(text[i : i+j+1])
			i += j + 1
		case text[i] == '\\' && i+1 < len(text) && text[i+1] == 'N':
			flush()
			b.LineBreak()
			i += 2
		case text[i] == '\\' && i+1 < len(text) && (text[i+1] == 'n' || text[i+1] == 'h'):
			flush()
			b.Tag(text[i : i+2])
			i += 2
		default:
			_, size := utf8.DecodeRuneInString(text[i:])
			plain.WriteString(text[i : i+size])
			i += size
		}
	}
	flush()
	return b.Done()
}

// PlainText is the visible text of an event: no tags, breaks as spaces.
func PlainText(text string) string { return markers.Plain(Mask(text).Text) }

// Kind is what an event is, for deciding whether to translate it.
type Kind string

const (
	KindDialogue Kind = "dialogue"
	KindSign     Kind = "sign"     // typesetting (on-screen text)
	KindSong     Kind = "song"     // OP/ED/insert song lines, karaoke
	KindDrawing  Kind = "drawing"  // \p1 vector drawing
	KindKaraoke  Kind = "karaoke"  // \k timing tags
	KindEffect   Kind = "effect"   // Effect field set (scroll, banner…)
	KindComment  Kind = "comment"  // Comment: line
	KindNoLetter Kind = "noletter" // nothing to translate (♪, …, numbers)
)

var (
	drawingRe = regexp.MustCompile(`\\p[1-9]`)
	karaokeRe = regexp.MustCompile(`\\(k|kf|ko|K)\d`)
	signTagRe = regexp.MustCompile(`\\(pos|move|org|clip|iclip)\(|\\(frz|frx|fry|fr|fax|fay)-?[\d.]`)
	animRe    = regexp.MustCompile(`\\t\(`)
)

var songWords = map[string]bool{"op": true, "ed": true, "song": true, "songs": true, "kara": true, "karaoke": true,
	"lyric": true, "lyrics": true, "romaji": true, "kanji": true, "insert": true, "theme": true, "opening": true, "ending": true}

var signWords = map[string]bool{"sign": true, "signs": true, "ts": true, "type": true, "typeset": true, "typesetting": true,
	"title": true, "note": true, "notes": true, "screen": true, "onscreen": true}

func hasWord(s string, set map[string]bool) bool {
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) }) {
		if set[w] {
			return true
		}
	}
	return false
}

// Classify says what an event is. Only dialogue and simple signs are translated by
// default; songs, karaoke, drawings and effects are copied as they are.
func (e *Event) Classify() Kind {
	switch {
	case e.Type == "Comment":
		return KindComment
	case drawingRe.MatchString(e.Text):
		return KindDrawing
	case karaokeRe.MatchString(e.Text):
		return KindKaraoke
	case strings.TrimSpace(e.Effect) != "":
		return KindEffect
	case hasWord(e.Style, songWords) || hasWord(e.Name, songWords):
		return KindSong
	}
	if !hasLetter(PlainText(e.Text)) {
		return KindNoLetter
	}
	if hasWord(e.Style, signWords) || signTagRe.MatchString(e.Text) {
		return KindSign
	}
	return KindDialogue
}

// SimpleSign is a sign that can be translated safely: one short text without
// per-glyph animation.
func (e *Event) SimpleSign() bool {
	m := Mask(e.Text)
	return utf8.RuneCountInString(markers.Plain(m.Text)) <= 60 && !animRe.MatchString(e.Text) && len(m.Tags) <= 2
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
