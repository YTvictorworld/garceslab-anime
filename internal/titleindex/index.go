package titleindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Suggestion is one anime suggested for a partial title.
type Suggestion struct {
	// MediaID is the AniList anime id.
	MediaID int `json:"mediaId"`
	// Title is the database's main title (usually the romaji title).
	Title string `json:"title"`
	// Matched is the synonym that matched the query, when it wasn't the main title.
	Matched string `json:"matched,omitempty"`
	// Year is the year the anime started (0 when unknown).
	Year int `json:"year,omitempty"`
	// Format is TV, MOVIE, OVA, ONA or SPECIAL (a subset of AniList's MediaFormat; empty when unknown).
	Format   string `json:"format,omitempty"`
	Episodes int    `json:"episodes,omitempty"`
	// Picture and Thumbnail are cover image URLs from the database, when it has them.
	Picture   string `json:"picture,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

// Match tiers, best first.
const (
	tierExact      = iota // the title or a synonym equals the query
	tierPrefix            // the title or a synonym starts with the query
	tierWordPrefix        // a later word of the title or a synonym starts with the query
	tierSubstring         // the title or a synonym contains the query anywhere
	tierNone
)

// MinSubstringQuery is the shortest normalized query that may match in the middle
// of a word; shorter ones only match at the start of a name or a word.
const MinSubstringQuery = 3

type entry struct {
	suggestion Suggestion
	// names[0] is the normalized main title, the rest are normalized synonyms (deduplicated).
	names []string
	// display names, aligned with names (to report which synonym matched).
	display []string
	// popularity is a rough "how well known" signal: the number of sources, tags and
	// synonyms in the database (well-known anime are listed, tagged and translated by
	// more communities). The database has no member counts.
	popularity int
	typeRank   int
	score      float32
}

// Index is an immutable, in-memory title index. Safe for concurrent use.
type Index struct {
	entries    []entry
	lastUpdate string
}

// Len returns the number of anime in the index.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return len(ix.entries)
}

// LastUpdate is the database's own "lastUpdate" date (YYYY-MM-DD), if it had one.
func (ix *Index) LastUpdate() string {
	if ix == nil {
		return ""
	}
	return ix.lastUpdate
}

// typeRank orders formats for tie-breaking: TV series first.
func typeRank(t string) int {
	switch t {
	case "TV":
		return 0
	case "MOVIE":
		return 1
	case "ONA", "OVA":
		return 2
	case "SPECIAL":
		return 3
	default:
		return 4
	}
}

const anilistSourcePrefix = "https://anilist.co/anime/"

// AnilistID returns the AniList id in a list of source URLs (0 if there is none).
func AnilistID(sources []string) int {
	for _, s := range sources {
		rest, ok := strings.CutPrefix(s, anilistSourcePrefix)
		if !ok {
			continue
		}
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			rest = rest[:i]
		}
		if id, err := strconv.Atoi(rest); err == nil && id > 0 {
			return id
		}
	}
	return 0
}

// rawEntry is the part of an anime-offline-database entry the index uses.
type rawEntry struct {
	Sources     []string `json:"sources"`
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Episodes    int      `json:"episodes"`
	AnimeSeason struct {
		Year int `json:"year"`
	} `json:"animeSeason"`
	Picture   string   `json:"picture"`
	Thumbnail string   `json:"thumbnail"`
	Synonyms  []string `json:"synonyms"`
	Tags      []string `json:"tags"`
	Score     *struct {
		ArithmeticMean float64 `json:"arithmeticMean"`
	} `json:"score"`
}

// Placeholder images the database uses when a source has no cover.
func isPlaceholderImage(url string) bool {
	return url == "" || strings.Contains(url, "/manami-project/anime-offline-database/master/pics/")
}

func newEntry(r *rawEntry, id int) (entry, bool) {
	title := strings.TrimSpace(r.Title)
	nt := Normalize(title)
	if nt == "" {
		return entry{}, false
	}
	e := entry{
		suggestion: Suggestion{
			MediaID:  id,
			Title:    title,
			Year:     r.AnimeSeason.Year,
			Episodes: r.Episodes,
		},
		names:      []string{nt},
		display:    []string{title},
		popularity: len(r.Sources) + len(r.Tags) + len(r.Synonyms),
		typeRank:   typeRank(r.Type),
	}
	if r.Type != "UNKNOWN" {
		e.suggestion.Format = r.Type
	}
	if !isPlaceholderImage(r.Picture) {
		e.suggestion.Picture = r.Picture
	}
	if !isPlaceholderImage(r.Thumbnail) {
		e.suggestion.Thumbnail = r.Thumbnail
	}
	if r.Score != nil {
		e.score = float32(r.Score.ArithmeticMean)
	}
	seen := map[string]bool{nt: true}
	for _, syn := range r.Synonyms {
		syn = strings.TrimSpace(syn)
		ns := Normalize(syn)
		if ns == "" || seen[ns] {
			continue
		}
		seen[ns] = true
		e.names = append(e.names, ns)
		e.display = append(e.display, syn)
	}
	return e, true
}

// Parse reads an anime-offline-database JSON document (the "minified" release
// asset, already decompressed) and builds the index. Entries are decoded one at
// a time, so the whole document is never held in memory. Entries without an
// AniList source are skipped; a second entry with the same AniList id is ignored.
func Parse(r io.Reader) (*Index, error) {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return nil, err
	}
	ix := &Index{}
	foundData := false
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("titleindex: %w", err)
		}
		key, _ := keyTok.(string)
		switch key {
		case "lastUpdate":
			var s string
			if err := dec.Decode(&s); err != nil {
				return nil, fmt.Errorf("titleindex: lastUpdate: %w", err)
			}
			ix.lastUpdate = s
		case "data":
			foundData = true
			if err := ix.parseData(dec); err != nil {
				return nil, err
			}
		default:
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, fmt.Errorf("titleindex: %s: %w", key, err)
			}
		}
	}
	if !foundData {
		return nil, errors.New("titleindex: no \"data\" array in the document")
	}
	return ix, nil
}

func (ix *Index) parseData(dec *json.Decoder) error {
	if err := expectDelim(dec, '['); err != nil {
		return err
	}
	seen := make(map[int]bool, 1<<15)
	for dec.More() {
		var r rawEntry
		if err := dec.Decode(&r); err != nil {
			return fmt.Errorf("titleindex: entry %d: %w", len(ix.entries), err)
		}
		id := AnilistID(r.Sources)
		if id == 0 || seen[id] {
			continue
		}
		e, ok := newEntry(&r, id)
		if !ok {
			continue
		}
		seen[id] = true
		ix.entries = append(ix.entries, e)
	}
	if _, err := dec.Token(); err != nil { // closing ']'
		return fmt.Errorf("titleindex: %w", err)
	}
	ix.entries = ix.entries[:len(ix.entries):len(ix.entries)]
	return nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("titleindex: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("titleindex: expected %q, got %v", want, tok)
	}
	return nil
}

// matchTier returns how well a normalized name matches a normalized query.
func matchTier(name, q string) int {
	if !strings.HasPrefix(name, q) {
		if strings.Contains(name, " "+q) {
			return tierWordPrefix
		}
		if len(q) >= MinSubstringQuery && strings.Contains(name, q) {
			return tierSubstring
		}
		return tierNone
	}
	if len(name) == len(q) {
		return tierExact
	}
	return tierPrefix
}

type hit struct {
	e    *entry
	tier int
	name int // index of the best matching name
	// groupPopularity is the highest popularity among the hits that matched the
	// same normalized name (e.g. every season listing "Attack on Titan" as a synonym).
	groupPopularity int
}

func (h hit) matchedName() string { return h.e.names[h.name] }

// yearOrLast puts unknown years after known ones.
func yearOrLast(y int) int {
	if y <= 0 {
		return 1 << 30
	}
	return y
}

// less is the ranking, compared key by key:
//
//  1. match tier: exact > prefix > word prefix > substring;
//  2. TV before movies before OVA/ONA before specials;
//  3. popularity (see entry), taken per matched name so that the anime sharing a
//     name stay together (groupPopularity);
//  4. the shorter matched name (closer to what was typed);
//  5. within one matched name, the earliest year first (usually the first season);
//  6. then popularity, the higher average score and the lower AniList id.
func less(a, b hit) bool {
	if a.tier != b.tier {
		return a.tier < b.tier
	}
	if a.e.typeRank != b.e.typeRank {
		return a.e.typeRank < b.e.typeRank
	}
	if a.groupPopularity != b.groupPopularity {
		return a.groupPopularity > b.groupPopularity
	}
	na, nb := a.matchedName(), b.matchedName()
	if len(na) != len(nb) {
		return len(na) < len(nb)
	}
	if na != nb {
		return na < nb
	}
	if ya, yb := yearOrLast(a.e.suggestion.Year), yearOrLast(b.e.suggestion.Year); ya != yb {
		return ya < yb
	}
	if a.e.popularity != b.e.popularity {
		return a.e.popularity > b.e.popularity
	}
	if a.e.score != b.e.score {
		return a.e.score > b.e.score
	}
	return a.e.suggestion.MediaID < b.e.suggestion.MediaID
}

// Search returns up to limit anime whose title or synonyms match the query,
// best first (see less). An empty query returns no results.
func (ix *Index) Search(query string, limit int) []Suggestion {
	if ix == nil || limit <= 0 {
		return []Suggestion{}
	}
	q := Normalize(query)
	if q == "" {
		return []Suggestion{}
	}
	hits := make([]hit, 0, 64)
	for i := range ix.entries {
		e := &ix.entries[i]
		best := hit{e: e, tier: tierNone}
		for n, name := range e.names {
			t := matchTier(name, q)
			// The best tier wins; on a tie the main title (checked first), then the shorter synonym.
			if t < best.tier || (t == best.tier && t != tierNone && best.name != 0 && len(name) < len(e.names[best.name])) {
				best.tier, best.name = t, n
			}
			if best.tier == tierExact {
				break
			}
		}
		if best.tier != tierNone {
			hits = append(hits, best)
		}
	}
	groups := make(map[string]int, len(hits))
	for _, h := range hits {
		if p := h.e.popularity; p > groups[h.matchedName()] {
			groups[h.matchedName()] = p
		}
	}
	for i := range hits {
		hits[i].groupPopularity = groups[hits[i].matchedName()]
	}
	sort.Slice(hits, func(i, j int) bool { return less(hits[i], hits[j]) })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Suggestion, len(hits))
	for i, h := range hits {
		out[i] = h.e.suggestion
		if h.name != 0 {
			out[i].Matched = h.e.display[h.name]
		}
	}
	return out
}
