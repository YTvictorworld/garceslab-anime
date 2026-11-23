//go:build ignore

// check-avatars verifies the bundled profile avatar catalog: every entry has an id,
// a license and a source, the license is in the allowed list, ids are unique and the
// file exists. Every file in files/ must be in the catalog.
//
//	go run .github/scripts/check-avatars.go [internal/profiles/avatars]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var allowedLicenses = map[string]bool{"CC0 1.0": true}

type avatar struct {
	ID      string `json:"id"`
	File    string `json:"file"`
	License string `json:"license"`
	Source  string `json:"source"`
}

func main() {
	dir := "internal/profiles/avatars"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	data, err := os.ReadFile(filepath.Join(dir, "catalog.json"))
	if err != nil {
		fail(err.Error())
	}
	var list []avatar
	if err := json.Unmarshal(data, &list); err != nil {
		fail("catalog.json: " + err.Error())
	}
	if _, err := os.Stat(filepath.Join(dir, "LICENSES.md")); err != nil {
		fail("LICENSES.md is missing")
	}

	var problems []string
	seen := map[string]bool{}
	listed := map[string]bool{}
	for i, a := range list {
		name := a.ID
		if name == "" {
			name = fmt.Sprintf("entry %d", i)
			problems = append(problems, name+": missing id")
		} else if seen[a.ID] {
			problems = append(problems, name+": duplicate id")
		}
		seen[a.ID] = true
		if strings.TrimSpace(a.License) == "" {
			problems = append(problems, name+": missing license")
		} else if !allowedLicenses[a.License] {
			problems = append(problems, fmt.Sprintf("%s: license %q is not allowed", name, a.License))
		}
		if !strings.HasPrefix(a.Source, "https://") {
			problems = append(problems, name+": missing or non-https source")
		}
		if a.File == "" || filepath.Base(a.File) != a.File {
			problems = append(problems, fmt.Sprintf("%s: invalid file %q", name, a.File))
			continue
		}
		listed[a.File] = true
		if _, err := os.Stat(filepath.Join(dir, "files", a.File)); err != nil {
			problems = append(problems, fmt.Sprintf("%s: file %s not found", name, a.File))
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "files"))
	for _, e := range entries {
		if !listed[e.Name()] {
			problems = append(problems, fmt.Sprintf("files/%s is not in catalog.json", e.Name()))
		}
	}
	if len(list) == 0 {
		problems = append(problems, "catalog is empty")
	}
	if len(problems) > 0 {
		fail(strings.Join(problems, "\n"))
	}
	fmt.Printf("avatars: %d entries OK\n", len(list))
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "avatar catalog check failed:\n"+msg)
	os.Exit(1)
}
