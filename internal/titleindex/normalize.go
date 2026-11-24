// Package titleindex is Garceslab Anime's local anime title index for prefix
// typeahead ("ble" -> Bleach). AniList's anonymous search matches whole words
// (with typo tolerance), so a few typed letters rarely find anything; this index
// matches prefixes and substrings of every title and synonym instead.
//
// The data comes from manami-project's anime-offline-database
// (https://github.com/manami-project/anime-offline-database), licensed under the
// Open Database License (ODbL) v1.0, with its contents under the Database Contents
// License (DbCL) v1.0. It is downloaded at runtime into the server's data
// directory and never shipped with the program. Only entries with an AniList
// source are kept, and they are identified by their AniList id.
package titleindex

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize folds a title or a query into the form the index compares:
// compatibility-decomposed (full-width letters become ASCII), diacritics removed,
// lowercase, apostrophes dropped ("JoJo's" -> "jojos") and every other run of
// punctuation, symbols or spaces collapsed into one space, trimmed.
//
// Titles and queries go through the same function, so lossy steps (such as
// dropping Japanese voicing marks with the other combining marks) still compare
// consistently.
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range norm.NFKD.String(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
			// Combining marks (diacritics) are dropped.
		case r == '\'' || r == '’' || r == '`' || r == '´':
			// Apostrophes join the word: "JoJo's" is typed "jojos".
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if pendingSpace && b.Len() > 0 {
				b.WriteByte(' ')
			}
			pendingSpace = false
			b.WriteRune(unicode.ToLower(r))
		default:
			pendingSpace = true
		}
	}
	return b.String()
}
