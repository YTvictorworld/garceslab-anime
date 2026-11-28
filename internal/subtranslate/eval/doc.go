package eval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"seanime/internal/subtranslate/ass"
	"seanime/internal/subtranslate/markers"
	"seanime/internal/subtranslate/srt"
)

// unit is one subtitle event of a document, whatever the format.
type unit struct {
	idx     int
	start   time.Duration
	end     time.Duration
	speaker string
	kind    string // ass.Kind values; SRT cues are "dialogue"
	simple  bool   // a simple sign (ASS only)
	raw     string // the event text as in the file
	masked  markers.Masked
	// Line-set fields.
	scene, cat, ref, lineID string
}

func (u unit) plain() string { return markers.Plain(u.masked.Text) }

// doc is a subtitle document the evaluation can translate.
type doc interface {
	units() []unit
	mask(text string) markers.Masked
	setText(idx int, text string)
	bytes() []byte
	ext() string
}

// loadDoc reads an .ass/.ssa, .srt or line-set .jsonl file.
func loadDoc(path string) (doc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseDoc(data, path)
}

// parseDoc parses a document; name gives the format by its extension.
func parseDoc(data []byte, path string) (doc, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ass", ".ssa":
		f, err := ass.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		return &assDoc{f: f}, nil
	case ".srt":
		f, err := srt.Parse(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		return &srtDoc{f: f}, nil
	case ".jsonl":
		return parseLineSet(data)
	}
	return nil, fmt.Errorf("%s: unsupported subtitle format (want .ass, .ssa, .srt or .jsonl)", filepath.Base(path))
}

type assDoc struct{ f *ass.File }

func (d *assDoc) units() []unit {
	out := make([]unit, len(d.f.Events))
	for i, e := range d.f.Events {
		k := e.Classify()
		out[i] = unit{idx: i, start: e.Start, end: e.End, speaker: e.Name, kind: string(k),
			simple: k == ass.KindSign && e.SimpleSign(), raw: e.Text, masked: ass.Mask(e.Text)}
	}
	return out
}
func (d *assDoc) mask(t string) markers.Masked { return ass.Mask(t) }
func (d *assDoc) setText(i int, t string)      { d.f.SetText(i, t) }
func (d *assDoc) bytes() []byte                { return d.f.Bytes() }
func (d *assDoc) ext() string                  { return ".ass" }

type srtDoc struct{ f *srt.File }

func (d *srtDoc) units() []unit {
	out := make([]unit, len(d.f.Cues))
	for i, c := range d.f.Cues {
		m := srt.Mask(c.Text)
		kind := string(ass.KindDialogue)
		if !hasLetter(markers.Plain(m.Text)) {
			kind = string(ass.KindNoLetter)
		}
		out[i] = unit{idx: i, start: c.Start, end: c.End, kind: kind, raw: c.Text, masked: m}
	}
	return out
}
func (d *srtDoc) mask(t string) markers.Masked { return srt.Mask(t) }
func (d *srtDoc) setText(i int, t string)      { d.f.SetText(i, t) }
func (d *srtDoc) bytes() []byte                { return d.f.Bytes() }
func (d *srtDoc) ext() string                  { return ".srt" }

// LineSetEntry is one line of a line-set file (.jsonl): hand-written lines in a
// realistic style, grouped in scenes, with their reference translation. Text and Ref
// use ASS markup ({\i1}, \N).
type LineSetEntry struct {
	ID    string  `json:"id"`
	Scene string  `json:"scene,omitempty"`
	Cat   string  `json:"cat,omitempty"`
	Who   string  `json:"who,omitempty"`
	Sec   float64 `json:"sec,omitempty"`
	Style string  `json:"style,omitempty"`
	Text  string  `json:"text"`
	Ref   string  `json:"ref,omitempty"`
	Out   string  `json:"out,omitempty"` // written in the translated copy
}

type lineSetDoc struct{ lines []LineSetEntry }

func parseLineSet(data []byte) (*lineSetDoc, error) {
	d := &lineSetDoc{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		var e LineSetEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if e.ID == "" {
			e.ID = fmt.Sprintf("L%d", n)
		}
		d.lines = append(d.lines, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(d.lines) == 0 {
		return nil, fmt.Errorf("empty line set")
	}
	return d, nil
}

func (d *lineSetDoc) units() []unit {
	out := make([]unit, len(d.lines))
	var t time.Duration
	for i, l := range d.lines {
		sec := l.Sec
		if sec <= 0 {
			sec = 2
		}
		dur := time.Duration(sec * float64(time.Second))
		// Classify like an ASS event so karaoke and drawings are skipped the same way.
		ev := &ass.Event{Type: "Dialogue", Style: l.Style, Name: l.Who, Text: l.Text, Start: t, End: t + dur}
		k := ev.Classify()
		out[i] = unit{idx: i, start: t, end: t + dur, speaker: l.Who, kind: string(k),
			simple: k == ass.KindSign && ev.SimpleSign(), raw: l.Text, masked: ass.Mask(l.Text),
			scene: l.Scene, cat: l.Cat, ref: l.Ref, lineID: l.ID}
		t += dur + 500*time.Millisecond
	}
	return out
}
func (d *lineSetDoc) mask(t string) markers.Masked { return ass.Mask(t) }
func (d *lineSetDoc) setText(i int, t string)      { d.lines[i].Out = t }
func (d *lineSetDoc) bytes() []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, l := range d.lines {
		_ = enc.Encode(l)
	}
	return b.Bytes()
}
func (d *lineSetDoc) ext() string { return ".jsonl" }
