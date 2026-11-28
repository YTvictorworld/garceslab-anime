package eval

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"seanime/internal/subtranslate/ass"
)

// ReviewOptions configure a blind review sheet.
type ReviewOptions struct {
	OutDir string
	// Runs are run directory names under OutDir/runs ("<model>__<preset>"); empty = all.
	Runs []string
	// Lines is how many lines to sample (the plan asks for ~80 per finalist).
	Lines int
	Seed  int64
	// Reference: "visible" (shown as the reference column), "blind" (one more hidden
	// candidate, as in "model A, model B or official") or "none".
	Reference string
	// Pair limits the sheet to one language pair ("" = all).
	Pair string
}

// ReviewKey maps every (line, letter) of a sheet to its run. It is written apart
// from the sheet so the reviewer doesn't see it.
type ReviewKey struct {
	Seed  int64                        `json:"seed"`
	Runs  []string                     `json:"runs"`
	Lines map[string]map[string]string `json:"lines"` // line number -> letter -> run (or "reference")
	Pairs map[string]string            `json:"pairs"` // line number -> pair
}

// Flags the reviewer can tick (plan §3.4).
var reviewFlags = []string{"register", "name_honorific", "insult", "too_long", "meaning"}

// Review writes OutDir/review/sheet.csv, sheet.md and OutDir/review-key.json.
func Review(o ReviewOptions) (string, error) {
	runs := o.Runs
	if len(runs) == 0 {
		dirs, _ := filepath.Glob(filepath.Join(o.OutDir, "runs", "*"))
		for _, d := range dirs {
			if fi, err := os.Stat(d); err == nil && fi.IsDir() {
				runs = append(runs, filepath.Base(d))
			}
		}
	}
	sort.Strings(runs)
	if len(runs) == 0 {
		return "", fmt.Errorf("no runs under %s", filepath.Join(o.OutDir, "runs"))
	}
	type lineKey struct {
		item string
		n    int
	}
	outputs := map[lineKey]map[string]LineRecord{}
	for _, run := range runs {
		files, _ := filepath.Glob(filepath.Join(o.OutDir, "runs", run, "*.lines.jsonl"))
		if len(files) == 0 {
			return "", fmt.Errorf("run %q has no line records", run)
		}
		for _, f := range files {
			recs, err := readRecords(f)
			if err != nil {
				return "", err
			}
			for _, r := range recs {
				if o.Pair != "" && r.Pair != o.Pair {
					continue
				}
				k := lineKey{r.Item, r.N}
				if outputs[k] == nil {
					outputs[k] = map[string]LineRecord{}
				}
				outputs[k][run] = r
			}
		}
	}
	var keys []lineKey
	for k, byRun := range outputs {
		if len(byRun) != len(runs) {
			continue // only lines every run translated
		}
		if o.Reference != "none" && firstRecord(byRun).Reference == "" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return "", fmt.Errorf("no line is common to all runs%s", map[bool]string{true: " and has a reference", false: ""}[o.Reference != "none"])
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].item != keys[j].item {
			return keys[i].item < keys[j].item
		}
		return keys[i].n < keys[j].n
	})
	rng := rand.New(rand.NewSource(o.Seed))
	rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	if o.Lines > 0 && len(keys) > o.Lines {
		keys = keys[:o.Lines]
	}

	dir := filepath.Join(o.OutDir, "review")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	key := ReviewKey{Seed: o.Seed, Runs: runs, Lines: map[string]map[string]string{}, Pairs: map[string]string{}}
	csvFile, err := os.Create(filepath.Join(dir, "sheet.csv"))
	if err != nil {
		return "", err
	}
	defer csvFile.Close()
	w := csv.NewWriter(csvFile)
	header := []string{"line", "candidate", "source", "reference", "translation", "score_1_5"}
	header = append(append(header, reviewFlags...), "notes")
	_ = w.Write(header)
	var md strings.Builder
	md.WriteString("# Blind review\n\nScore each candidate 1–5 in sheet.csv (5 natural and correct, 4 small style issue, 3 understandable with errors, 2 meaning partly wrong, 1 wrong or unusable) and mark the flags that apply with an x: ")
	md.WriteString("register (tú/usted, vosotros in es-419), name_honorific (name or honorific mishandled), insult (censored or exaggerated), too_long (too long to read), meaning (meaning changed).\n")
	md.WriteString("The candidates are in a random order on every line; the key is in review-key.json, outside this folder. Don't open it until you're done.\n\n")
	letters := "ABCDEFGHIJ"
	for i, k := range keys {
		num := strconv.Itoa(i + 1)
		byRun := outputs[k]
		first := firstRecord(byRun)
		cands := append([]string{}, runs...)
		if o.Reference == "blind" {
			cands = append(cands, "reference")
		}
		rng.Shuffle(len(cands), func(a, b int) { cands[a], cands[b] = cands[b], cands[a] })
		key.Lines[num] = map[string]string{}
		key.Pairs[num] = first.Pair
		refShown := ""
		if o.Reference == "visible" {
			refShown = plainASS(first.Reference)
		}
		fmt.Fprintf(&md, "## %s\n\n- **Source:** %s\n", num, plainASS(first.Source))
		if refShown != "" {
			fmt.Fprintf(&md, "- **Reference:** %s\n", refShown)
		}
		for ci, c := range cands {
			letter := string(letters[ci])
			key.Lines[num][letter] = c
			text := ""
			if c == "reference" {
				text = plainASS(first.Reference)
			} else {
				text = plainASS(byRun[c].Output)
			}
			fmt.Fprintf(&md, "- **%s:** %s\n", letter, text)
			row := []string{num, letter, plainASS(first.Source), refShown, text, ""}
			for range reviewFlags {
				row = append(row, "")
			}
			_ = w.Write(append(row, ""))
		}
		md.WriteString("\n")
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "sheet.md"), []byte(md.String()), 0o644); err != nil {
		return "", err
	}
	keyPath := filepath.Join(o.OutDir, "review-key.json")
	if err := writeJSON(keyPath, key); err != nil {
		return "", err
	}
	return dir, nil
}

func firstRecord(m map[string]LineRecord) LineRecord {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return m[ks[0]]
}

func plainASS(s string) string { return ass.PlainText(s) }

func readRecords(path string) ([]LineRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []LineRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r LineRecord
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// ScoreRow is the review result of one run (model and preset), or of the reference.
type ScoreRow struct {
	Run    string         `json:"run"`
	Pair   string         `json:"pair"`
	Rated  int            `json:"rated"`
	Mean   float64        `json:"mean"`
	Dist   map[int]int    `json:"dist"`
	Flags  map[string]int `json:"flags"`
	Severe float64        `json:"severe_pct"` // meaning or register flags
}

// ScoreReview tallies a filled-in sheet.csv with its key, per run and pair.
func ScoreReview(sheetPath, keyPath string) ([]ScoreRow, error) {
	var key ReviewKey
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &key); err != nil {
		return nil, err
	}
	f, err := os.Open(sheetPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("empty sheet")
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	for _, need := range []string{"line", "candidate", "score_1_5"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("sheet has no %q column", need)
		}
	}
	type k struct{ run, pair string }
	acc := map[k]*ScoreRow{}
	var order []k
	sums := map[k]float64{}
	for _, r := range rows[1:] {
		line, cand := r[col["line"]], r[col["candidate"]]
		run := key.Lines[line][cand]
		if run == "" {
			return nil, fmt.Errorf("line %s candidate %s is not in the key (sheet and key don't match)", line, cand)
		}
		scoreText := strings.TrimSpace(r[col["score_1_5"]])
		if scoreText == "" {
			continue
		}
		score, err := strconv.Atoi(scoreText)
		if err != nil || score < 1 || score > 5 {
			return nil, fmt.Errorf("line %s candidate %s: score %q is not 1–5", line, cand, scoreText)
		}
		kk := k{run, key.Pairs[line]}
		row := acc[kk]
		if row == nil {
			row = &ScoreRow{Run: run, Pair: kk.pair, Dist: map[int]int{}, Flags: map[string]int{}}
			acc[kk] = row
			order = append(order, kk)
		}
		row.Rated++
		row.Dist[score]++
		sums[kk] += float64(score)
		severe := false
		for _, fl := range reviewFlags {
			if i, ok := col[fl]; ok && i < len(r) && strings.TrimSpace(r[i]) != "" {
				row.Flags[fl]++
				if fl == "meaning" || fl == "register" {
					severe = true
				}
			}
		}
		if severe {
			row.Severe++
		}
	}
	var out []ScoreRow
	for _, kk := range order {
		row := acc[kk]
		row.Mean = sums[kk] / float64(row.Rated)
		row.Severe = 100 * row.Severe / float64(row.Rated)
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pair != out[j].Pair {
			return out[i].Pair < out[j].Pair
		}
		return out[i].Mean > out[j].Mean
	})
	return out, nil
}

// ScoreMarkdown renders review scores.
func ScoreMarkdown(rows []ScoreRow) string {
	var b strings.Builder
	b.WriteString("| Pair | Run | Rated | Mean (1–5) | 5 / 4 / 3 / 2 / 1 | Severe flags % | Flags |\n|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		flags := map[string]int{}
		for k, v := range r.Flags {
			flags[k] = v
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %.2f | %d / %d / %d / %d / %d | %.1f | %s |\n", r.Pair, r.Run, r.Rated, r.Mean,
			r.Dist[5], r.Dist[4], r.Dist[3], r.Dist[2], r.Dist[1], r.Severe, issueList(flags))
	}
	return b.String()
}
