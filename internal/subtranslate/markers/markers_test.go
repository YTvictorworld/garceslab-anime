package markers

import "testing"

func build() Masked {
	b := NewBuilder(`\N`)
	b.Tag(`{\an8\pos(10,20)}`)
	b.Text("I ")
	b.Tag(`{\i1}`)
	b.Text("swear")
	b.Tag(`{\i0}`)
	b.Text(" it")
	b.LineBreak()
	b.Text("wasn't me!")
	return b.Done()
}

func TestBuilder(t *testing.T) {
	m := build()
	if m.Prefix != `{\an8\pos(10,20)}` {
		t.Errorf("prefix %q", m.Prefix)
	}
	if m.Text != "I ⟦1⟧swear⟦2⟧ it⟦N⟧wasn't me!" {
		t.Errorf("text %q", m.Text)
	}
	if m.Breaks != 1 || len(m.Tags) != 2 {
		t.Errorf("breaks %d tags %d", m.Breaks, len(m.Tags))
	}
}

func TestRestore(t *testing.T) {
	m := build()
	got := m.Restore("Te ⟦1⟧juro⟦2⟧ que ⟦N⟧ no fui yo!")
	want := `{\an8\pos(10,20)}Te {\i1}juro{\i0} que\Nno fui yo!`
	if got != want {
		t.Errorf("Restore = %q, want %q", got, want)
	}
	if m.Restore(m.Text) != `{\an8\pos(10,20)}I {\i1}swear{\i0} it\Nwasn't me!` {
		t.Errorf("identity restore = %q", m.Restore(m.Text))
	}
}

func TestCheck(t *testing.T) {
	m := build()
	cases := map[string]bool{
		"Te ⟦1⟧juro⟦2⟧ que no fui yo!":     true,
		"Te ⟦1⟧juro⟦2⟧ que⟦N⟧no fui yo!":   true,
		"Te juro que no fui yo!":           false, // missing
		"Te ⟦1⟧juro⟦2⟧⟦2⟧ que no fui yo!":  false, // duplicated
		"Te ⟦2⟧juro⟦1⟧ que no fui yo!":     false, // order
		"Te ⟦1⟧juro⟦2⟧ que ⟦3⟧no fui yo!":  false, // invented
		"Te ⟦1⟧juro⟦2⟧⟦N⟧que⟦N⟧no fui yo!": false, // too many breaks
		"Te ⟦ 1 ⟧juro⟦2⟧ que no fui yo!":   false, // mangled
	}
	for out, ok := range cases {
		if got := m.Check(out).OK(); got != ok {
			t.Errorf("Check(%q).OK() = %v, want %v (%+v)", out, got, ok, m.Check(out))
		}
	}
}

func TestPlain(t *testing.T) {
	if got := Plain("Te ⟦1⟧juro⟦2⟧ que⟦N⟧no  fui yo!"); got != "Te juro que no fui yo!" {
		t.Errorf("Plain = %q", got)
	}
}
