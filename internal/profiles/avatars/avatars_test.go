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
	assert.GreaterOrEqual(t, len(list), 24)
	for _, a := range list {
		assert.Equal(t, "CC0 1.0", a.License, a.ID)
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
	}, fsys)
	require.Error(t, err)
	for _, want := range []string{"no-license: missing license", "no-source: missing source", "missing.svg not found", "duplicate id"} {
		assert.Contains(t, err.Error(), want)
	}
	assert.Error(t, validate(nil, fsys))
}
