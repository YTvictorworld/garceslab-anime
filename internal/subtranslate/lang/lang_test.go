package lang

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedRegistry(t *testing.T) {
	r := Default()
	for _, code := range []string{"es-419", "es-ES", "en", "fr"} {
		l, err := r.Resolve(code)
		if err != nil {
			t.Fatalf("%s: %v", code, err)
		}
		if l.Code != code {
			t.Errorf("Resolve(%q) = %s", code, l.Code)
		}
	}
	if DefaultTarget != "es-419" {
		t.Errorf("default target %s", DefaultTarget)
	}
}

func TestResolve(t *testing.T) {
	r := Default()
	cases := map[string]string{
		"es-419":  "es-419",
		"ES-419":  "es-419",
		"es_419":  "es-419",
		"spa":     "es-419", // base only -> the default variant
		"Spanish": "es-419",
		"Español": "es-419",
		"es-MX":   "es-419",
		"latino":  "es-419",
		"es-ES":   "es-ES",
		"es-es":   "es-ES",
		"eng":     "en",
		"en-GB":   "en",
		"fre":     "fr",
		"French":  "fr",
		"fr-CA":   "fr",
		"pt":      "pt-BR",
		"por":     "pt-BR",
		"ger":     "de",
	}
	for in, want := range cases {
		l, err := r.Resolve(in)
		if err != nil {
			t.Errorf("Resolve(%q): %v", in, err)
			continue
		}
		if l.Code != want {
			t.Errorf("Resolve(%q) = %s, want %s", in, l.Code, want)
		}
	}
	if _, err := r.Resolve("klingon"); err == nil {
		t.Error("an unknown language must be an error")
	}
	if _, err := r.Resolve(""); err == nil {
		t.Error("an empty language must be an error")
	}
}

func TestNewPair(t *testing.T) {
	r := Default()
	p, err := r.NewPair("fre", "es-419")
	if err != nil {
		t.Fatal(err)
	}
	if p.String() != "fr→es-419" {
		t.Errorf("pair %s", p)
	}
	if _, err := r.NewPair("es-ES", "es-419"); err == nil {
		t.Error("two variants of one language are not a translation pair")
	}
}

// Adding a language is adding a registry entry: no code change.
func TestMergeFileAddsLanguage(t *testing.T) {
	r := Default()
	extra := `{"languages":[{"code":"ca","base":"ca","name":"Catalan","default_for_base":true,
	  "target_rules":["Natural spoken Catalan."],
	  "function_words":["el","la","els","les","que","de","i","és","amb","per","una","però","això","molt","també","perquè"]}]}`
	path := filepath.Join(t.TempDir(), "extra.json")
	if err := os.WriteFile(path, []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.MergeFile(path); err != nil {
		t.Fatal(err)
	}
	p, err := r.NewPair("en", "ca")
	if err != nil {
		t.Fatal(err)
	}
	if p.Target.Name != "Catalan" {
		t.Errorf("target %s", p.Target.Name)
	}
	// Replacing an existing entry by code.
	repl := `{"languages":[{"code":"fr","base":"fr","name":"French (custom)","default_for_base":true,
	  "function_words":["le","les","des","et","est","je","il","elle","qui","pas","ne"]}]}`
	if err := os.WriteFile(path, []byte(repl), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.MergeFile(path); err != nil {
		t.Fatal(err)
	}
	if l, _ := r.Resolve("fre"); l.Name != "French (custom)" {
		t.Errorf("fr not replaced: %s", l.Name)
	}
}

func TestParseRejectsBadEntries(t *testing.T) {
	for name, doc := range map[string]string{
		"base mismatch": `{"languages":[{"code":"fr","base":"es","name":"x","function_words":["a","b","c","d","e","f","g","h","i","j"]}]}`,
		"no name":       `{"languages":[{"code":"fr","base":"fr","function_words":["a","b","c","d","e","f","g","h","i","j"]}]}`,
		"few words":     `{"languages":[{"code":"fr","base":"fr","name":"French","function_words":["le"]}]}`,
		"not json":      `{`,
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestDetector(t *testing.T) {
	r := Default()
	enEs, _ := r.NewPair("en", "es-419")
	frEs, _ := r.NewPair("fr", "es-419")
	d := NewDetector(enEs)
	cases := []struct {
		text string
		want Verdict
	}{
		{"I told you to wait for me!", Source},
		{"Where were you? I waited for an hour!", Source},
		{"¡Te dije que me esperaras!", Target},
		{"¿Dónde estabas? Te esperé una hora.", Target},
		{"Huh?", Unknown},
		{"Riko-senpai!", Unknown},
	}
	for _, c := range cases {
		if got := d.Detect(c.text); got != c.want {
			t.Errorf("en→es Detect(%q) = %s, want %s", c.text, got, c.want)
		}
	}
	f := NewDetector(frEs)
	for text, want := range map[string]Verdict{
		"T'étais où ? Je t'ai attendu une heure !": Source,
		"Je te jure que c'est pas ma faute !":      Source,
		"¡Te juro que no fue mi culpa!":            Target,
		"Hein ?":                                   Unknown,
	} {
		if got := f.Detect(text); got != want {
			t.Errorf("fr→es Detect(%q) = %s, want %s", text, got, want)
		}
	}
}

func TestWords(t *testing.T) {
	got := Words("Don't go — qu'est-ce que c'est ? « Oui » l'homme")
	want := []string{"don't", "go", "qu'est-ce", "que", "c'est", "oui", "l'homme"}
	if len(got) != len(want) {
		t.Fatalf("Words = %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Words[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
