// Package ass reads and writes ASS/SSA subtitle scripts without losing anything
// (Garceslab Anime: subtitle translation).
//
// The file is kept as its original lines. Only the Text field of the events that are
// changed with SetText is rewritten; every other byte ([Script Info], styles, fonts,
// graphics, comments, the other fields of each event, line endings and the BOM) comes
// out exactly as it went in.
package ass

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Event is a Dialogue or Comment line of the [Events] section.
type Event struct {
	// Index is the position of the event among all events (0-based).
	Index int
	// Type is "Dialogue" or "Comment".
	Type   string
	Layer  int
	Start  time.Duration
	End    time.Duration
	Style  string
	Name   string
	Effect string
	// Text is the raw Text field (override tags included).
	Text string

	line   int    // index in File.lines
	prefix string // raw line up to the Text field
	dirty  bool
}

type rawLine struct {
	content string // without the terminator
	eol     string // "\n", "\r\n" or "" (last line without newline)
}

// File is a parsed script.
type File struct {
	bom    bool
	lines  []rawLine
	Events []*Event
}

var defaultFormat = []string{"layer", "start", "end", "style", "name", "marginl", "marginr", "marginv", "effect", "text"}

// Parse reads an ASS or SSA script.
func Parse(data []byte) (*File, error) {
	f := &File{}
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		f.bom = true
		data = data[3:]
	}
	s := string(data)
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			f.lines = append(f.lines, rawLine{content: s})
			break
		}
		content, eol := s[:i], "\n"
		if strings.HasSuffix(content, "\r") {
			content, eol = content[:len(content)-1], "\r\n"
		}
		f.lines = append(f.lines, rawLine{content: content, eol: eol})
		s = s[i+1:]
	}

	section := ""
	format := defaultFormat
	sawScriptInfo := false
	for li, l := range f.lines {
		t := strings.TrimSpace(l.content)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = strings.ToLower(t)
			if section == "[script info]" {
				sawScriptInfo = true
			}
			continue
		}
		if section != "[events]" {
			continue
		}
		key, _, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "format":
			format = parseFormat(t[len(key)+1:])
		case "dialogue", "comment":
			ev, err := parseEvent(l.content, format)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", li+1, err)
			}
			ev.line = li
			ev.Index = len(f.Events)
			f.Events = append(f.Events, ev)
		}
	}
	if !sawScriptInfo {
		return nil, fmt.Errorf("not an ASS/SSA script: no [Script Info] section")
	}
	return f, nil
}

func parseFormat(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.ToLower(strings.TrimSpace(p))
	}
	return out
}

func parseEvent(raw string, format []string) (*Event, error) {
	colon := strings.IndexByte(raw, ':')
	ev := &Event{Type: strings.TrimSpace(raw[:colon])}
	if strings.EqualFold(ev.Type, "comment") {
		ev.Type = "Comment"
	} else {
		ev.Type = "Dialogue"
	}
	// Offset of the first field (spaces after the colon belong to the prefix).
	pos := colon + 1
	for pos < len(raw) && raw[pos] == ' ' {
		pos++
	}
	textIdx := -1
	for i, name := range format {
		if name == "text" {
			textIdx = i
		}
	}
	if textIdx != len(format)-1 {
		return nil, fmt.Errorf("the Text field must be the last one in Format")
	}
	fields := make([]string, 0, len(format))
	rest := raw[pos:]
	for i := 0; i < len(format)-1; i++ {
		j := strings.IndexByte(rest, ',')
		if j < 0 {
			return nil, fmt.Errorf("event has fewer fields than Format")
		}
		fields = append(fields, rest[:j])
		rest = rest[j+1:]
		pos += j + 1
	}
	ev.prefix = raw[:pos]
	ev.Text = rest
	for i, name := range format[:len(format)-1] {
		v := strings.TrimSpace(fields[i])
		switch name {
		case "layer":
			ev.Layer, _ = strconv.Atoi(v)
		case "start":
			ev.Start = parseTime(v)
		case "end":
			ev.End = parseTime(v)
		case "style":
			ev.Style = v
		case "name", "actor":
			ev.Name = v
		case "effect":
			ev.Effect = v
		}
	}
	return ev, nil
}

// parseTime reads H:MM:SS.cc (centiseconds; more or fewer fraction digits are accepted).
func parseTime(s string) time.Duration {
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0
	}
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	sec, frac, _ := strings.Cut(parts[2], ".")
	sv, _ := strconv.Atoi(sec)
	d := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sv)*time.Second
	if frac != "" {
		f, _ := strconv.ParseFloat("0."+frac, 64)
		d += time.Duration(f * float64(time.Second))
	}
	return d
}

// SetText replaces the Text field of an event. Line breaks are not allowed inside a
// field; use \N.
func (f *File) SetText(index int, text string) {
	ev := f.Events[index]
	text = strings.NewReplacer("\r\n", `\N`, "\n", `\N`, "\r", "").Replace(text)
	if ev.Text == text {
		return
	}
	ev.Text = text
	ev.dirty = true
}

// Bytes writes the script. Unchanged files are byte-for-byte identical to the input.
func (f *File) Bytes() []byte {
	var b bytes.Buffer
	if f.bom {
		b.WriteString("\xef\xbb\xbf")
	}
	dirty := make(map[int]*Event)
	for _, ev := range f.Events {
		if ev.dirty {
			dirty[ev.line] = ev
		}
	}
	for i, l := range f.lines {
		if ev := dirty[i]; ev != nil {
			b.WriteString(ev.prefix)
			b.WriteString(ev.Text)
		} else {
			b.WriteString(l.content)
		}
		b.WriteString(l.eol)
	}
	return b.Bytes()
}
