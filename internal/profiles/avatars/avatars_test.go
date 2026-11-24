package avatars

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedCatalogIsValid(t *testing.T) {
	require.NoError(t, Validate())
	list := Catalog()
	assert.GreaterOrEqual(t, len(list), 48)
	for _, a := range list {
		assert.True(t, AllowedLicenses[a.License], a.ID)
		if NeedsAttribution(a.License) {
			assert.NotEmpty(t, a.Attribution, a.ID)
			assert.NotEmpty(t, a.Creator, a.ID)
		} else {
			assert.Empty(t, a.Attribution, a.ID)
		}
		assert.True(t, strings.HasPrefix(a.Source, "https://"), a.ID)
		assert.Equal(t, URLPrefix+a.File, a.URL)
		data, ct, err := ReadFile(a.File)
		require.NoError(t, err, a.ID)
		assert.Equal(t, "image/svg+xml", ct)
		assert.Contains(t, string(data), "<svg")
	}
	assert.True(t, Exists("gl-lorelei-01"))
	assert.False(t, Exists("missing"))
	assert.Equal(t, list[0], Resolve("missing"))
}

func TestReadFileRejectsTraversal(t *testing.T) {
	for _, f := range []string{"", "../catalog.json", "files/gl-lorelei-01.svg", "/etc/passwd"} {
		_, _, err := ReadFile(f)
		assert.Error(t, err, f)
	}
}

func TestValidateDetectsProblems(t *testing.T) {
	fsys := fstest.MapFS{"files/ok.svg": {Data: []byte("<svg/>")}}
	assert.NoError(t, validate([]*Avatar{{ID: "a", File: "ok.svg", License: "CC0 1.0", Source: "https://x"}}, fsys))

	err := validate([]*Avatar{
		{ID: "no-license", File: "ok.svg", Source: "https://x"},
		{ID: "no-source", File: "ok.svg", License: "CC0 1.0"},
		{ID: "no-file", File: "missing.svg", License: "CC0 1.0", Source: "https://x"},
		{ID: "no-file", File: "ok.svg", License: "CC0 1.0", Source: "https://x"},
		{ID: "bad-license", File: "ok.svg", License: "All rights reserved", Source: "https://x"},
		{ID: "no-credit", File: "ok.svg", License: "CC BY 4.0", Source: "https://x"},
	}, fsys)
	require.Error(t, err)
	for _, want := range []string{"no-license: missing license", "no-source: missing source", "missing.svg not found", "duplicate id",
		`bad-license: license "All rights reserved" is not allowed`, "no-credit: CC BY 4.0 needs an attribution and a creator"} {
		assert.Contains(t, err.Error(), want)
	}
	assert.Error(t, validate(nil, fsys))

	assert.NoError(t, validate([]*Avatar{{ID: "by", File: "ok.svg", License: "CC BY 4.0", Source: "https://x",
		Creator: "Someone", Attribution: "Art by Someone, CC BY 4.0"}}, fsys))
}

// The catalog keeps the phase 1 ids (profiles store them) and adds the new styles.
func TestCatalogKeepsExistingIDs(t *testing.T) {
	for _, id := range []string{"gl-lorelei-01", "gl-lorelei-08", "gl-notionists-06", "gl-critters-06", "gl-thumbs-04"} {
		assert.True(t, Exists(id), id)
	}
	for _, id := range []string{"gl-open-peeps-01", "gl-adventurer-06", "gl-pixel-art-04", "gl-voxel-bot-01", "gl-clay-04", "gl-sprouts-01", "gl-planets-04"} {
		assert.True(t, Exists(id), id)
	}
}
