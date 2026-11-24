// Package trackprobe reads the track list (audio and subtitle languages) of a
// remote MKV file by fetching only its header with HTTP Range requests
// (Garceslab Anime: subtitle-aware debrid auto-select).
package trackprobe

import (
	"strings"
	"unicode"
)

// languageTable maps a canonical language key (ISO 639-1) to the codes and
// names that mean it: ISO 639-2/B and /T codes, IETF tags and names in English
// and in the language itself. Region subtags (es-419, pt-BR) are stripped
// before the lookup.
var languageTable = map[string][]string{
	"es": {"es", "spa", "esp", "spanish", "español", "espanol", "castellano", "castilian", "latino", "latin american", "latinoamericano", "latinoamérica", "latinoamerica", "latam"},
	"en": {"en", "eng", "english", "inglés", "ingles"},
	"pt": {"pt", "por", "portuguese", "português", "portugues", "brazilian", "brasileiro"},
	"fr": {"fr", "fre", "fra", "french", "français", "francais"},
	"de": {"de", "ger", "deu", "german", "deutsch"},
	"it": {"it", "ita", "italian", "italiano"},
	"ja": {"ja", "jpn", "jp", "japanese", "日本語"},
	"ru": {"ru", "rus", "russian", "русский"},
	"ar": {"ar", "ara", "arabic", "العربية"},
	"zh": {"zh", "chi", "zho", "chinese", "中文", "mandarin", "cantonese"},
	"ko": {"ko", "kor", "korean", "한국어"},
	"pl": {"pl", "pol", "polish", "polski"},
	"tr": {"tr", "tur", "turkish", "türkçe"},
	"id": {"id", "ind", "indonesian", "bahasa indonesia"},
	"th": {"th", "tha", "thai"},
	"vi": {"vi", "vie", "vietnamese", "tiếng việt"},
	"hi": {"hi", "hin", "hindi"},
	"nl": {"nl", "dut", "nld", "dutch", "nederlands"},
	"ms": {"ms", "may", "msa", "malay"},
	"uk": {"uk", "ukr", "ukrainian"},
}

// aliasToKey is languageTable inverted.
var aliasToKey = func() map[string]string {
	m := make(map[string]string)
	for key, aliases := range languageTable {
		for _, a := range aliases {
			m[a] = key
		}
	}
	return m
}()

// nameWords are the aliases that are looked for as words inside a track name
// ("Spanish (Latin America)", "Español [Castellano]"). Two- and three-letter
// codes are left out: they would match too much ("it", "de", "lat").
var nameWords = func() map[string]string {
	m := make(map[string]string)
	for alias, key := range aliasToKey {
		if len([]rune(alias)) > 3 {
			m[alias] = key
		}
	}
	return m
}()

// Normalize turns a language code, IETF tag or name into its canonical key
// ("spa", "es-419", "Español" -> "es"). Unknown values are returned lowercased
// and trimmed; "" and "und" give "".
func Normalize(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" || v == "und" || v == "unk" || v == "zxx" || v == "mul" {
		return ""
	}
	if key, ok := aliasToKey[v]; ok {
		return key
	}
	// IETF tag: language subtag first (es-419, pt_BR, zh-Hant).
	if i := strings.IndexAny(v, "-_"); i > 0 {
		if key, ok := aliasToKey[v[:i]]; ok {
			return key
		}
	}
	return v
}

// LanguagesInName returns the languages named in a track name, in order
// ("Spanish (Latin America)" -> ["es"]). It only knows full names, not codes.
func LanguagesInName(name string) []string {
	lower := strings.ToLower(name)
	if lower == "" {
		return nil
	}
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	var out []string
	add := func(key string) {
		for _, k := range out {
			if k == key {
				return
			}
		}
		out = append(out, key)
	}
	for i, w := range words {
		if key, ok := nameWords[w]; ok {
			add(key)
			continue
		}
		// Two-word names ("latin american", "bahasa indonesia").
		if i+1 < len(words) {
			if key, ok := nameWords[w+" "+words[i+1]]; ok {
				add(key)
			}
		}
	}
	return out
}

// TrackLanguages returns the canonical languages of a track from its
// LanguageIETF, Language and Name. LanguageIETF wins over Language (Matroska
// defaults Language to "eng" when the element is missing); languages named in
// the track name are added after it.
func TrackLanguages(language, languageIETF, name string) []string {
	var out []string
	add := func(key string) {
		if key == "" {
			return
		}
		for _, k := range out {
			if k == key {
				return
			}
		}
		out = append(out, key)
	}
	if ietf := Normalize(languageIETF); ietf != "" {
		add(ietf)
	} else {
		add(Normalize(language))
	}
	for _, k := range LanguagesInName(name) {
		add(k)
	}
	return out
}

// ParseList parses a comma-separated preference ("spa, es, spanish") into
// canonical keys, deduplicated, in order.
func ParseList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		key := Normalize(part)
		if key == "" {
			continue
		}
		dup := false
		for _, k := range out {
			if k == key {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, key)
		}
	}
	return out
}

// Code3 returns the three-letter code of a canonical key ("es" -> "spa"), for
// logs and status texts. Unknown keys are returned as they are.
func Code3(key string) string {
	for _, a := range languageTable[key] {
		if len(a) == 3 && a == strings.ToLower(a) && isASCII(a) {
			return a
		}
	}
	return key
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}
