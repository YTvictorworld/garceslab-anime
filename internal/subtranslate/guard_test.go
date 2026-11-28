package subtranslate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoEvalDataInRepo fails when subtitle files, eval manifests or evaluation
// outputs are in the repository. Real subtitles and their translations are
// copyrighted works: they stay in the eval directory outside the repository
// (docs/subtitle-translation-eval.md). Tests use synthetic fixtures written inline.
func TestNoEvalDataInRepo(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found from %s", root)
	}
	skipDirs := map[string]bool{".git": true, "node_modules": true, "web": true, "out": true, "dist": true, ".next": true}
	subtitleExt := map[string]bool{".ass": true, ".ssa": true, ".srt": true, ".vtt": true, ".sub": true, ".sup": true}
	var bad []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if skipDirs[name] && path != root {
				return filepath.SkipDir
			}
			if name == "subtranslate-eval-data" || name == "subtranslate-results" || (rel == "subtranslate-eval") {
				bad = append(bad, rel+"/")
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(name))
		switch {
		case subtitleExt[ext]:
			bad = append(bad, rel)
		case strings.HasSuffix(name, ".lines.jsonl"), name == "review-key.json",
			strings.Contains(name, ".translated."):
			bad = append(bad, rel)
		case name == "manifest.json" && looksLikeEvalManifest(path):
			bad = append(bad, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Errorf("eval data or subtitle file in the repository: %s (keep it in the eval directory outside the repo)", b)
	}
}

func looksLikeEvalManifest(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 1<<20 {
		return false
	}
	s := string(data)
	return strings.Contains(s, `"items"`) && strings.Contains(s, `"source"`) && strings.Contains(s, `"lang"`)
}
