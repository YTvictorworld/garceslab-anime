package handlers

import (
	"strings"
	"testing"

	"seanime/internal/constants"

	"github.com/goccy/go-json"
)

// The status response carries Seanime's version (for compatibility checks) and
// Garceslab Anime's own version side by side.
func TestStatusCarriesBothVersions(t *testing.T) {
	data, err := json.Marshal(&Status{Version: constants.Version, GarceslabVersion: constants.GarceslabVersion})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`"version":"` + constants.Version + `"`,
		`"garceslabVersion":"` + constants.GarceslabVersion + `"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("status JSON %s does not contain %s", got, want)
		}
	}
}
