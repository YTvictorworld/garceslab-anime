// Package srt reads and writes SubRip subtitles (Garceslab Anime: subtitle
// translation). Cue numbers, timings (with any position settings after them), the
// BOM and the line endings are kept; only the text of each cue changes.
package srt

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"seanime/internal/subtranslate/markers"
)

// Cue is one subtitle.
type Cue struct {
	Index  int    // position in the file (0-based)
	Number string // the cue number as written
	Timing string // the timing line as written
	Start  time.Duration
	End    time.Duration
	Text   string // lines joined with "\n"
}

// File is a parsed SRT file.
type File struct {
	bom  bool
	eol  string
	Cues []*Cue
}

var timingRe = regexp.MustCompile(`^\s*(\d+):(\d{1,2}):(\d{1,2})[,.](\d{1,3})\s*-->\s*(\d+):(\d{1,2}):(\d{1,2})[,.](\d{1,3})`)

// Parse reads an SRT file.
func Parse(data []byte) (*File, error) {
	f := &File{eol: "\n"}
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		f.bom = true
		data = data[3:]
	}
	s := string(data)
	if strings.Contains(s, "\r\n") {
		f.eol = "\r\n"
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		number, timing := "", lines[i]
		if !timingRe.MatchString(timing) {
			if i+1 >= len(lines) || !timingRe.MatchString(lines[i+1]) {
				return nil, fmt.Errorf("line %d: expected a cue number and a timing line", i+1)
			}
			number, timing = strings.TrimSpace(lines[i]), lines[i+1]
			i++
		}
		i++
		var text []string
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			text = append(text, lines[i])
			i++
		}
		m := timingRe.FindStringSubmatch(timing)
		f.Cues = append(f.Cues, &Cue{
			Index:  len(f.Cues),
			Number: number,
			Timing: strings.TrimSpace(timing),
			Start:  clock(m[1:5]),
			End:    clock(m[5:9]),
			Text:   strings.Join(text, "\n"),
		})
	}
	if len(f.Cues) == 0 {
		return nil, fmt.Errorf("no SRT cues found")
	}
	return f, nil
}

func clock(p []string) time.Duration {
	h, _ := strconv.Atoi(p[0])
	m, _ := strconv.Atoi(p[1])
	s, _ := strconv.Atoi(p[2])
	ms, _ := strconv.Atoi((p[3] + "00")[:3])
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second + time.Duration(ms)*time.Millisecond
}

// SetText replaces a cue's text ("\n" separates lines).
func (f *File) SetText(index int, text string) { f.Cues[index].Text = text }

// Bytes writes the file.
func (f *File) Bytes() []byte {
	var b bytes.Buffer
	if f.bom {
		b.WriteString("\xef\xbb\xbf")
	}
	for i, c := range f.Cues {
		if i > 0 {
			b.WriteString(f.eol)
		}
		number := c.Number
		if number == "" {
			number = strconv.Itoa(i + 1)
		}
		b.WriteString(number + f.eol + c.Timing + f.eol)
		for _, l := range strings.Split(c.Text, "\n") {
			b.WriteString(l + f.eol)
		}
	}
	return b.Bytes()
}

var tagRe = regexp.MustCompile(`(?i)</?(i|b|u|s|font)(\s[^>]*)?>|\{\\[^}]*\}`)

// Mask turns a cue's text into a line for the model: HTML-like tags (<i>, <font …>)
// and ASS-style {\an8} become placeholders, line breaks become ⟦N⟧.
func Mask(text string) markers.Masked {
	b := markers.NewBuilder("\n")
	for li, line := range strings.Split(text, "\n") {
		if li > 0 {
			b.LineBreak()
		}
		last := 0
		for _, loc := range tagRe.FindAllStringIndex(line, -1) {
			b.Text(line[last:loc[0]])
			b.Tag(line[loc[0]:loc[1]])
			last = loc[1]
		}
		b.Text(line[last:])
	}
	return b.Done()
}
