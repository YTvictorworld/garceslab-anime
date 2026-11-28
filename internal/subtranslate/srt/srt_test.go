package srt

import (
	"strings"
	"testing"
	"time"
)

const sample = "1\r\n00:00:01,500 --> 00:00:03,200\r\nWhere were you?\r\n\r\n" +
	"2\r\n00:00:03,300 --> 00:00:05,000 X1:10 X2:20\r\nI <i>swear</i> it wasn't\r\nmy fault!\r\n\r\n" +
	"3\r\n00:00:05.000 --> 00:00:07.000\r\n{\\an8}<font color=\"#ffff00\">BAKERY</font>\r\n"

func TestRoundTrip(t *testing.T) {
	f, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Cues) != 3 {
		t.Fatalf("%d cues", len(f.Cues))
	}
	c := f.Cues[1]
	if c.Start != 3300*time.Millisecond || c.End != 5*time.Second || c.Text != "I <i>swear</i> it wasn't\nmy fault!" {
		t.Errorf("cue %+v", c)
	}
	if got := string(f.Bytes()); got != sample {
		t.Fatalf("round trip:\n%q", got)
	}
	f.SetText(0, "¿Dónde estabas?")
	if !strings.Contains(string(f.Bytes()), "00:00:01,500 --> 00:00:03,200\r\n¿Dónde estabas?\r\n") {
		t.Error("SetText not written")
	}
}

func TestParseWithoutNumbersAndBOM(t *testing.T) {
	in := "\ufeff00:00:01,000 --> 00:00:02,000\nHello\n\n00:00:03,000 --> 00:00:04,000\nBye\n"
	f, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Cues) != 2 || f.Cues[1].Text != "Bye" {
		t.Fatalf("%+v", f.Cues)
	}
	if !strings.HasPrefix(string(f.Bytes()), "\ufeff1\n00:00:01,000") {
		t.Errorf("numbers are added on write: %q", f.Bytes())
	}
	if _, err := Parse([]byte("not a subtitle\nat all\n")); err == nil {
		t.Error("garbage must fail")
	}
}

func TestMask(t *testing.T) {
	m := Mask("I <i>swear</i> it wasn't\nmy fault!")
	if m.Text != "I ⟦1⟧swear⟦2⟧ it wasn't⟦N⟧my fault!" || m.Breaks != 1 {
		t.Errorf("mask %q", m.Text)
	}
	if got := m.Restore("Te ⟦1⟧juro⟦2⟧ que no fue⟦N⟧mi culpa!"); got != "Te <i>juro</i> que no fue\nmi culpa!" {
		t.Errorf("restore %q", got)
	}
	m = Mask(`{\an8}<font color="#ffff00">BAKERY</font>`)
	if m.Prefix != `{\an8}<font color="#ffff00">` || m.Text != "BAKERY⟦1⟧" {
		t.Errorf("prefix %q text %q", m.Prefix, m.Text)
	}
}
