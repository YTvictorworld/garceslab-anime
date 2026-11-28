package ass

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// An invented script with everything a fansub file can have that must survive.
const script = "\ufeff[Script Info]\r\n" +
	"; Invented test script\r\n" +
	"Title: Test\r\n" +
	"ScriptType: v4.00+\r\n" +
	"PlayResX: 1920\r\n" +
	"PlayResY: 1080\r\n" +
	"YCbCr Matrix: TV.709\r\n" +
	"\r\n" +
	"[V4+ Styles]\r\n" +
	"Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\r\n" +
	"Style: Default,Arial,64,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,3,0,2,60,60,50,1\r\n" +
	"Style: Sign,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,8,10,10,10,1\r\n" +
	"Style: OP-Romaji,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,8,10,10,10,1\r\n" +
	"\r\n" +
	"[Fonts]\r\n" +
	"fontname: test.ttf\r\n" +
	"M3)!-E=\r\n" +
	"\r\n" +
	"[Events]\r\n" +
	"Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\r\n" +
	"Comment: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,This is a comment, with commas\r\n" +
	"Dialogue: 0,0:00:01.50,0:00:03.20,Default,Riko,0,0,0,,Where were you, Tomo? I waited!\r\n" +
	"Dialogue: 0,0:00:03.30,0:00:05.00,Default,Tomo,0,0,0,,I {\\i1}swear{\\i0} it wasn't\\Nmy fault!\r\n" +
	"Dialogue: 0,0:00:05.00,0:00:07.00,Sign,,0,0,0,,{\\an8\\pos(960,80)}MOONLIGHT BAKERY\r\n" +
	"Dialogue: 1,0:00:05.00,0:00:07.00,Sign,,0,0,0,,{\\an8\\pos(960,80)\\bord0}MOONLIGHT BAKERY\r\n" +
	"Dialogue: 0,0:00:08.00,0:00:10.00,OP-Romaji,,0,0,0,,{\\k20}ha{\\k30}ru {\\k25}no\r\n" +
	"Dialogue: 0,0:00:10.00,0:00:11.00,Default,,0,0,0,,{\\p1}m 0 0 l 100 0 100 100{\\p0}\r\n" +
	"Dialogue: 0,0:00:11.00,0:00:12.00,Default,,0,0,0,Scroll up;10;100;5,Credits text\r\n" +
	"Dialogue: 0,0:00:12.00,0:00:13.00,Default,,0,0,0,,♪ ♪\r\n" +
	"Dialogue: 0,0:00:13.00,0:00:14.50,Default,,0,0,0,,Hard\\hspace and{\\i1} italic tail{\\i0}\r\n"

func TestRoundTripIsByteIdentical(t *testing.T) {
	f, err := Parse([]byte(script))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Bytes(); !bytes.Equal(got, []byte(script)) {
		t.Fatalf("round trip changed the file:\n%q", got)
	}
	// Without BOM, LF endings and no final newline.
	lf := strings.ReplaceAll(strings.TrimPrefix(script, "\ufeff"), "\r\n", "\n")
	lf = strings.TrimSuffix(lf, "\n")
	f2, err := Parse([]byte(lf))
	if err != nil {
		t.Fatal(err)
	}
	if string(f2.Bytes()) != lf {
		t.Fatal("LF round trip changed the file")
	}
}

func TestParseEvents(t *testing.T) {
	f, err := Parse([]byte(script))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Events) != 10 {
		t.Fatalf("%d events", len(f.Events))
	}
	e := f.Events[1]
	if e.Type != "Dialogue" || e.Name != "Riko" || e.Style != "Default" || e.Text != "Where were you, Tomo? I waited!" {
		t.Errorf("event %+v", e)
	}
	if e.Start != 1500*time.Millisecond || e.End != 3200*time.Millisecond {
		t.Errorf("times %v %v", e.Start, e.End)
	}
	if f.Events[0].Text != "This is a comment, with commas" {
		t.Errorf("comment text %q", f.Events[0].Text)
	}
	if f.Events[7].Effect != "Scroll up;10;100;5" {
		t.Errorf("effect %q", f.Events[7].Effect)
	}
}

func TestSetTextOnlyChangesText(t *testing.T) {
	f, _ := Parse([]byte(script))
	f.SetText(2, "Te {\\i1}juro{\\i0} que no fue\\Nmi culpa!")
	f.SetText(1, "¿Dónde estabas, Tomo?\n¡Te esperé!")
	got := string(f.Bytes())
	want := strings.Replace(script,
		"Dialogue: 0,0:00:03.30,0:00:05.00,Default,Tomo,0,0,0,,I {\\i1}swear{\\i0} it wasn't\\Nmy fault!",
		"Dialogue: 0,0:00:03.30,0:00:05.00,Default,Tomo,0,0,0,,Te {\\i1}juro{\\i0} que no fue\\Nmi culpa!", 1)
	want = strings.Replace(want,
		"Dialogue: 0,0:00:01.50,0:00:03.20,Default,Riko,0,0,0,,Where were you, Tomo? I waited!",
		"Dialogue: 0,0:00:01.50,0:00:03.20,Default,Riko,0,0,0,,¿Dónde estabas, Tomo?\\N¡Te esperé!", 1)
	if got != want {
		t.Fatalf("unexpected output:\n%s", got)
	}
}

func TestSSAFormat(t *testing.T) {
	ssa := "[Script Info]\nScriptType: v4.00\n\n[Events]\nFormat: Marked, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: Marked=0,0:00:01.00,0:00:02.00,Default,,0000,0000,0000,,Hello, there\n"
	f, err := Parse([]byte(ssa))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Events) != 1 || f.Events[0].Text != "Hello, there" || f.Events[0].Start != time.Second {
		t.Fatalf("%+v", f.Events)
	}
	if string(f.Bytes()) != ssa {
		t.Fatal("SSA round trip changed the file")
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n")); err == nil {
		t.Error("an SRT file is not an ASS script")
	}
	bad := "[Script Info]\n[Events]\nFormat: Layer, Start, Text, End\nDialogue: 0,0:00:01.00,hi,0:00:02.00\n"
	if _, err := Parse([]byte(bad)); err == nil {
		t.Error("Text must be the last field")
	}
}

func TestMask(t *testing.T) {
	cases := []struct {
		in, prefix, text string
		tags, breaks     int
	}{
		{`Where were you?`, "", "Where were you?", 0, 0},
		{`I {\i1}swear{\i0} it wasn't\Nmy fault!`, "", "I ⟦1⟧swear⟦2⟧ it wasn't⟦N⟧my fault!", 2, 1},
		{`{\an8\pos(960,80)}MOONLIGHT BAKERY`, `{\an8\pos(960,80)}`, "MOONLIGHT BAKERY", 0, 0},
		{`{\an8}{\i1}Top line{\i0}`, `{\an8}{\i1}`, "Top line⟦1⟧", 1, 0},
		{`Hard\hspace and{\i1} italic`, "", "Hard⟦1⟧space and⟦2⟧ italic", 2, 0},
		{`soft\nbreak`, "", "soft⟦1⟧break", 1, 0},
		{`unterminated {\i1 block`, "", `unterminated {\i1 block`, 0, 0},
		{`{TL note}Hello`, `{TL note}`, "Hello", 0, 0},
	}
	for _, c := range cases {
		m := Mask(c.in)
		if m.Prefix != c.prefix || m.Text != c.text || len(m.Tags) != c.tags || m.Breaks != c.breaks {
			t.Errorf("Mask(%q) = prefix %q text %q tags %d breaks %d", c.in, m.Prefix, m.Text, len(m.Tags), m.Breaks)
		}
		if got := m.Restore(m.Text); got != c.in {
			t.Errorf("Restore(Mask(%q)) = %q", c.in, got)
		}
	}
}

func TestClassify(t *testing.T) {
	f, _ := Parse([]byte(script))
	want := []Kind{KindComment, KindDialogue, KindDialogue, KindSign, KindSign, KindKaraoke, KindDrawing, KindEffect, KindNoLetter, KindDialogue}
	for i, e := range f.Events {
		if got := e.Classify(); got != want[i] {
			t.Errorf("event %d (%q): %s, want %s", i, e.Text, got, want[i])
		}
	}
	if !f.Events[3].SimpleSign() {
		t.Error("a short sign is simple")
	}
	song := &Event{Type: "Dialogue", Style: "ED_Translation", Text: "Under the spring sky"}
	if song.Classify() != KindSong {
		t.Errorf("ED style: %s", song.Classify())
	}
	anim := &Event{Type: "Dialogue", Style: "Sign", Text: `{\pos(1,1)\t(0,500,\frz30)}SPINNING`}
	if anim.Classify() != KindSign || anim.SimpleSign() {
		t.Error("an animated sign is not simple")
	}
}
