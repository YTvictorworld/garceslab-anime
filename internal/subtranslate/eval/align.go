package eval

import (
	"sort"
	"strings"
	"time"
)

type segment struct {
	start, end time.Duration
	text       string
}

type component struct {
	srcIdx []int
	hyp    string
	ref    string
}

// align pairs the translated source lines with the reference lines by time. Two lines
// are linked when they overlap by at least 30 % of the shorter one; every connected
// group (one source line split into two reference lines, or the other way round) is
// one scoring segment, so no reference text is counted twice. Lines without a partner
// (signs only one track has, different splits around the window edges) are left out.
func align(src, ref []segment) []component {
	n := len(src)
	parent := make([]int, n+len(ref))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	linked := make([]bool, len(parent))
	// Both lists sorted by start for a sweep.
	ri := make([]int, len(ref))
	for i := range ri {
		ri[i] = i
	}
	sort.Slice(ri, func(a, b int) bool { return ref[ri[a]].start < ref[ri[b]].start })
	for i, s := range src {
		for _, j := range ri {
			r := ref[j]
			if r.start >= s.end {
				break
			}
			ov := min(s.end, r.end) - max(s.start, r.start)
			if ov <= 0 {
				continue
			}
			shorter := min(s.end-s.start, r.end-r.start)
			if shorter <= 0 || float64(ov) < 0.3*float64(shorter) {
				continue
			}
			a, b := find(i), find(n+j)
			if a != b {
				parent[a] = b
			}
			linked[i], linked[n+j] = true, true
		}
	}
	groups := map[int]*struct {
		src []int
		ref []int
	}{}
	var order []int
	get := func(root int) *struct {
		src []int
		ref []int
	} {
		g := groups[root]
		if g == nil {
			g = &struct {
				src []int
				ref []int
			}{}
			groups[root] = g
			order = append(order, root)
		}
		return g
	}
	for i := range src {
		if linked[i] {
			g := get(find(i))
			g.src = append(g.src, i)
		}
	}
	for j := range ref {
		if linked[n+j] {
			g := get(find(n + j))
			g.ref = append(g.ref, j)
		}
	}
	var out []component
	for _, root := range order {
		g := groups[root]
		if len(g.src) == 0 || len(g.ref) == 0 {
			continue
		}
		sort.Slice(g.ref, func(a, b int) bool { return ref[g.ref[a]].start < ref[g.ref[b]].start })
		var hyp, rt []string
		for _, i := range g.src {
			hyp = append(hyp, src[i].text)
		}
		for _, j := range g.ref {
			rt = append(rt, ref[j].text)
		}
		out = append(out, component{srcIdx: g.src, hyp: strings.Join(hyp, " "), ref: strings.Join(rt, " ")})
	}
	return out
}
