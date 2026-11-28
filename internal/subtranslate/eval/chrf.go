package eval

import "strings"

// CHRF computes corpus-level chrF / chrF++ exactly as sacreBLEU 2.x does with its
// defaults (character order 6, beta 2, whitespace removed from character n-grams,
// case-sensitive, effective-order smoothing): character n-grams of orders 1–6 and,
// for chrF++, word n-grams of orders 1–2 with punctuation split off word edges.
// Statistics are summed over all segments before computing the score.
type CHRF struct {
	CharOrder int
	WordOrder int
	Beta      float64
	stats     []float64 // per order: hyp, ref, match
}

// NewCHRFpp returns a chrF++ accumulator (sacreBLEU's CHRF(word_order=2)).
func NewCHRFpp() *CHRF { return &CHRF{CharOrder: 6, WordOrder: 2, Beta: 2} }

// NewCHRF returns a plain chrF accumulator.
func NewCHRF() *CHRF { return &CHRF{CharOrder: 6, Beta: 2} }

const puncts = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

func splitPunct(sent string) []string {
	var out []string
	for _, w := range strings.Fields(sent) {
		r := []rune(w)
		switch {
		case len(r) == 1:
			out = append(out, w)
		case strings.ContainsRune(puncts, r[len(r)-1]):
			out = append(out, string(r[:len(r)-1]), string(r[len(r)-1]))
		case strings.ContainsRune(puncts, r[0]):
			out = append(out, string(r[0]), string(r[1:]))
		default:
			out = append(out, w)
		}
	}
	return out
}

func charNgrams(s string, n int) map[string]int {
	r := []rune(strings.Join(strings.Fields(s), ""))
	m := map[string]int{}
	for i := 0; i+n <= len(r); i++ {
		m[string(r[i:i+n])]++
	}
	return m
}

func wordNgrams(words []string, n int) map[string]int {
	m := map[string]int{}
	for i := 0; i+n <= len(words); i++ {
		m[strings.Join(words[i:i+n], " ")]++
	}
	return m
}

// Add accumulates one hypothesis/reference segment.
func (c *CHRF) Add(hyp, ref string) {
	order := c.CharOrder + c.WordOrder
	if c.stats == nil {
		c.stats = make([]float64, 3*order)
	}
	add := func(i int, h, r map[string]int) {
		var nh, nr, nm int
		for _, v := range h {
			nh += v
		}
		for _, v := range r {
			nr += v
		}
		for k, v := range h {
			if rv := r[k]; rv > 0 {
				nm += min(v, rv)
			}
		}
		c.stats[3*i] += float64(nh)
		c.stats[3*i+1] += float64(nr)
		c.stats[3*i+2] += float64(nm)
	}
	for n := 1; n <= c.CharOrder; n++ {
		add(n-1, charNgrams(hyp, n), charNgrams(ref, n))
	}
	if c.WordOrder > 0 {
		hw, rw := splitPunct(hyp), splitPunct(ref)
		for n := 1; n <= c.WordOrder; n++ {
			add(c.CharOrder+n-1, wordNgrams(hw, n), wordNgrams(rw, n))
		}
	}
}

// Score returns the corpus score in [0, 100] (0 with no segments).
func (c *CHRF) Score() float64 {
	if c.stats == nil {
		return 0
	}
	factor := c.Beta * c.Beta
	var avgP, avgR float64
	eff := 0
	for i := 0; i < len(c.stats)/3; i++ {
		nh, nr, nm := c.stats[3*i], c.stats[3*i+1], c.stats[3*i+2]
		if nh > 0 && nr > 0 {
			avgP += nm / nh
			avgR += nm / nr
			eff++
		}
	}
	if eff == 0 {
		return 0
	}
	avgP /= float64(eff)
	avgR /= float64(eff)
	if avgP+avgR == 0 {
		return 0
	}
	return 100 * (1 + factor) * avgP * avgR / (factor*avgP + avgR)
}
