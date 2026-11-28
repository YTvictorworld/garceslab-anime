package eval

import (
	"fmt"
	"os"
	"path/filepath"
)

// The sample line sets are invented for this repository (no real subtitles): they
// show the line-set format and give a quick smoke test. The real eval set is written
// by the owner and kept outside the repository.
const sampleEN = `{"id":"en-001","scene":"s1","cat":"dialogue","who":"Riko","sec":2.0,"text":"Where were you? I waited for an hour!","ref":"¿Dónde estabas? ¡Te esperé una hora!"}
{"id":"en-002","scene":"s1","cat":"honorifics","who":"Tomo","sec":2.2,"text":"Sorry, Riko-senpai. The train was late.","ref":"Perdón, Riko-senpai. El tren llegó tarde."}
{"id":"en-003","scene":"s1","cat":"slang","who":"Riko","sec":1.6,"text":"Damn it, not again...","ref":"Maldita sea, otra vez no..."}
{"id":"en-004","scene":"s1","cat":"tags","who":"Tomo","sec":2.4,"text":"I {\\i1}swear{\\i0} it wasn't my fault!","ref":"¡Te {\\i1}juro{\\i0} que no fue mi culpa!"}
{"id":"en-005","scene":"s1","cat":"short","who":"Riko","sec":0.8,"text":"Huh?","ref":"¿Eh?"}
{"id":"en-006","scene":"s1","cat":"breaks","who":"Tomo","sec":3.5,"text":"If we hurry, we can still catch\\Nthe last bus to the harbor.","ref":"Si nos apuramos, todavía podemos\\Nalcanzar el último autobús al puerto."}
{"id":"en-007","scene":"s2","cat":"glossary","who":"Narrator","sec":3.0,"text":"The Lantern Guild has guarded the old bridge for 300 years.","ref":"El Lantern Guild ha protegido el viejo puente durante 300 años."}
{"id":"en-008","scene":"s2","cat":"signs","style":"Sign","sec":3.0,"text":"{\\an8\\pos(640,80)}MOONLIGHT BAKERY","ref":"{\\an8\\pos(640,80)}PANADERÍA LUZ DE LUNA"}
{"id":"en-009","scene":"s2","cat":"songs","style":"OP-Romaji","sec":3.0,"text":"{\\k20}ha{\\k30}ru {\\k25}no {\\k40}so{\\k35}ra","ref":"{\\k20}ha{\\k30}ru {\\k25}no {\\k40}so{\\k35}ra"}
{"id":"en-010","scene":"s2","cat":"jokes","who":"Tomo","sec":2.5,"text":"Nice! Let's go, before the boss changes his mind.","ref":"¡Genial! Vámonos antes de que el jefe cambie de opinión."}
`

const sampleFR = `{"id":"fr-001","scene":"s1","cat":"dialogue","who":"Riko","sec":2.0,"text":"T'étais où ? Je t'ai attendu une heure !","ref":"¿Dónde estabas? ¡Te esperé una hora!"}
{"id":"fr-002","scene":"s1","cat":"honorifics","who":"Tomo","sec":2.2,"text":"Désolé, Riko-senpai. Le train avait du retard.","ref":"Perdón, Riko-senpai. El tren llegó tarde."}
{"id":"fr-003","scene":"s1","cat":"slang","who":"Riko","sec":1.6,"text":"Putain, pas encore...","ref":"Carajo, otra vez no..."}
{"id":"fr-004","scene":"s1","cat":"tags","who":"Tomo","sec":2.4,"text":"Je te {\\i1}jure{\\i0} que c'est pas ma faute !","ref":"¡Te {\\i1}juro{\\i0} que no fue mi culpa!"}
{"id":"fr-005","scene":"s1","cat":"short","who":"Riko","sec":0.8,"text":"Hein ?","ref":"¿Eh?"}
{"id":"fr-006","scene":"s1","cat":"breaks","who":"Tomo","sec":3.5,"text":"Si on se dépêche, on peut encore\\Nprendre le dernier bus pour le port.","ref":"Si nos apuramos, todavía podemos\\Nalcanzar el último autobús al puerto."}
`

const sampleManifest = `{
  "target": "es-419",
  "items": [
    {"id": "sample-lines-en", "source": {"file": "lines/sample.en.jsonl", "lang": "en"}, "target": "es-419", "tags": ["lines", "sample"]},
    {"id": "sample-lines-fr", "source": {"file": "lines/sample.fr.jsonl", "lang": "fr"}, "target": "es-419", "tags": ["lines", "sample"]}
  ]
}
`

// WriteSample creates dir with a manifest and the two invented sample line sets. It
// never overwrites existing files.
func WriteSample(dir string) ([]string, error) {
	files := map[string]string{
		"manifest.json":         sampleManifest,
		"lines/sample.en.jsonl": sampleEN,
		"lines/sample.fr.jsonl": sampleFR,
	}
	var written []string
	for _, rel := range []string{"manifest.json", "lines/sample.en.jsonl", "lines/sample.fr.jsonl"} {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(p, []byte(files[rel]), 0o644); err != nil {
			return written, fmt.Errorf("%s: %w", rel, err)
		}
		written = append(written, p)
	}
	return written, nil
}
