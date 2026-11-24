package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Phase 4: player preferences follow the profile, not the device or the client.
func TestPlayerPrefsEndpointsArePerProfile(t *testing.T) {
	env, _ := newM2Env(t)
	_, tokA := env.session("Ana", "tv")
	_, tokB := env.session("Bruno", "web")

	code, res := env.call("GET", "/api/v1/profiles/me/player-prefs", "tv", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code, res)

	code, res = env.call("PATCH", "/api/v1/profiles/me/player-prefs", "tv", tokA, map[string]any{
		"tv": map[string]any{"preferredSubtitleLanguages": "spa, es", "autoNextEpisode": false, "subtitleFontSize": 56},
	})
	require.Equal(t, 200, code, res)
	tv := dataMap(t, res)["tv"].(map[string]any)
	assert.Equal(t, "spa, es", tv["preferredSubtitleLanguages"])

	code, res = env.call("PATCH", "/api/v1/profiles/me/player-prefs", "web", tokB, map[string]any{
		"web": map[string]any{"mediaCore": map[string]any{"volume": 0.3, "autoNext": true}},
	})
	require.Equal(t, 200, code, res)

	// Ana sees only hers; Bruno only his.
	code, res = env.call("GET", "/api/v1/profiles/me/player-prefs", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	a := dataMap(t, res)
	assert.Equal(t, float64(56), a["tv"].(map[string]any)["subtitleFontSize"])
	assert.Nil(t, a["web"])
	code, res = env.call("GET", "/api/v1/profiles/me/player-prefs", "web", tokB, nil)
	require.Equal(t, 200, code, res)
	b := dataMap(t, res)
	assert.Nil(t, b["tv"])
	assert.Equal(t, 0.3, b["web"].(map[string]any)["mediaCore"].(map[string]any)["volume"])

	// Invalid values and device-only keys are refused.
	code, res = env.call("PATCH", "/api/v1/profiles/me/player-prefs", "tv", tokA, map[string]any{"tv": map[string]any{"speed": 50}})
	assert.Equal(t, http.StatusBadRequest, code, res)
	code, res = env.call("PATCH", "/api/v1/profiles/me/player-prefs", "tv", tokA, map[string]any{"tv": map[string]any{"wyzieApiKey": "k"}})
	assert.Equal(t, http.StatusBadRequest, code, res)

	// The profile settings endpoint shows them too, and its other fields are kept.
	code, res = env.call("PATCH", "/api/v1/profiles/me/settings", "tv", tokA, map[string]any{"markWatchingOnStart": false})
	require.Equal(t, 200, code, res)
	code, res = env.call("GET", "/api/v1/profiles/me/player-prefs", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	assert.Equal(t, false, dataMap(t, res)["tv"].(map[string]any)["autoNextEpisode"])
}

// Phase 4: exports and imports are in the profile audit log, with the actor.
func TestProfileAuditRecordsImportAndExport(t *testing.T) {
	env, _ := newM2Env(t)
	ana, tokA := env.session("Ana", "web")
	bruno, tokB := env.session("Bruno", "tv")
	code, res := env.call("POST", "/api/v1/anilist/list-entry", "tv", tokB, map[string]any{"mediaId": 1001, "status": "CURRENT", "progress": 2, "type": "anime"})
	require.Equal(t, 200, code, res)

	code, res = env.call("GET", "/api/v1/profiles/"+itoa(bruno.ID)+"/export", "web", tokA, nil)
	require.Equal(t, 200, code, res)
	exported := res["data"]

	code, res = env.call("POST", "/api/v1/profiles/"+itoa(bruno.ID)+"/import/json", "web", tokA, map[string]any{"mode": "merge", "data": exported})
	require.Equal(t, 200, code, res)
	// A failed import is not audited.
	code, _ = env.call("POST", "/api/v1/profiles/"+itoa(bruno.ID)+"/import/json", "web", tokA, map[string]any{"mode": "merge"})
	require.Equal(t, http.StatusBadRequest, code)

	code, res = env.call("GET", "/api/v1/profiles/audit", "web", tokA, nil)
	require.Equal(t, 200, code, res)
	list, _ := res["data"].([]any)
	var actions []string
	for _, item := range list {
		e := item.(map[string]any)
		actions = append(actions, e["action"].(string))
		if e["action"] == "export" || e["action"] == "import" {
			assert.Equal(t, float64(ana.ID), e["actorProfileId"])
			assert.Equal(t, float64(bruno.ID), e["targetProfileId"])
		}
		if e["action"] == "import" {
			assert.Equal(t, "JSON file", e["detail"])
		}
	}
	assert.Equal(t, []string{"import", "export"}, actions[:2], "newest first")
	n := 0
	for _, a := range actions {
		if a == "import" {
			n++
		}
	}
	assert.Equal(t, 1, n)
}

func TestImportAuditSource(t *testing.T) {
	assert.Equal(t, "Public AniList list", importAuditSource("/api/v1/profiles/:id/import/anilist"))
	assert.Equal(t, "The server's AniList account", importAuditSource("/api/v1/profiles/:id/import/server-account"))
	assert.Equal(t, "JSON file", importAuditSource("/api/v1/profiles/:id/import/json"))
	assert.Equal(t, "", importAuditSource("/api/v1/other"))
}
