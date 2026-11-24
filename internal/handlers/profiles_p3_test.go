package handlers

import (
	"context"
	"fmt"
	"net/http"
	"seanime/internal/api/anilist"
	"seanime/internal/profiles/importer"
	"seanime/internal/profiles/profilestest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// p3FakeToken is made up: no real AniList token is ever used in tests.
const p3FakeToken = "fake-handler-token-0123456789abcdefghijklmnop"

type p3FakeAccount struct{}

func (p3FakeAccount) ViewerName(context.Context) (string, error) {
	return profilestest.FixtureUserName, nil
}
func (p3FakeAccount) ViewerAnimeCollection(context.Context, string) (*anilist.AnimeCollection, error) {
	return profilestest.FixtureAnimeCollection(), nil
}
func (p3FakeAccount) ViewerFavourites(context.Context) (*importer.AnilistFavourites, error) {
	return &importer.AnilistFavourites{Anime: []int{1004}}, nil
}

func dataList(t *testing.T, res map[string]any) []any {
	t.Helper()
	l, ok := res["data"].([]any)
	require.True(t, ok, "data is not a list: %v", res)
	return l
}

func TestPhase3FavouritesEndpoints(t *testing.T) {
	env, _ := newM2Env(t)
	_, tokA := env.session("Ana", "tv")
	_, tokB := env.session("Bruno", "web")

	code, res := env.call("POST", "/api/v1/profiles/me/favourites/anime/1001", "tv", "", nil)
	assert.Equal(t, http.StatusUnauthorized, code, res)

	code, res = env.call("POST", "/api/v1/profiles/me/favourites/anime/1001", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	code, res = env.call("POST", "/api/v1/profiles/me/favourites/STUDIO/7", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	code, res = env.call("POST", "/api/v1/profiles/me/favourites/song/1", "tv", tokA, nil)
	assert.Equal(t, http.StatusBadRequest, code, res)
	code, res = env.call("POST", "/api/v1/profiles/me/favourites/anime/424242", "tv", tokA, nil)
	assert.Equal(t, http.StatusNotFound, code, res)

	code, res = env.call("GET", "/api/v1/profiles/me/favourites?kind=ANIME", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	favs := dataList(t, res)
	require.Len(t, favs, 1)
	fav := favs[0].(map[string]any)
	assert.Equal(t, float64(1001), fav["refId"])
	assert.Equal(t, float64(1001), fav["media"].(map[string]any)["id"])

	code, res = env.call("GET", "/api/v1/profiles/me/favourites", "web", tokB, nil)
	require.Equal(t, 200, code, res)
	assert.Empty(t, dataList(t, res), "favourites are per profile")

	code, res = env.call("DELETE", "/api/v1/profiles/me/favourites/anime/1001", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	code, res = env.call("GET", "/api/v1/profiles/me/favourites?kind=anime", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	assert.Empty(t, dataList(t, res))
}

func TestPhase3EntryEditorAndListsEndpoints(t *testing.T) {
	env, _ := newM2Env(t)
	_, tokA := env.session("Ana", "tv")
	_, tokB := env.session("Bruno", "web")

	code, res := env.call("POST", "/api/v1/profiles/me/lists", "tv", tokA, map[string]any{"name": "Cozy"})
	require.Equal(t, 200, code, res)
	listID := dataList(t, res)[0].(map[string]any)["id"].(float64)
	code, res = env.call("POST", "/api/v1/profiles/me/lists", "tv", tokA, map[string]any{"name": "Later"})
	require.Equal(t, 200, code, res)

	// "Add to my list" = an edit without status: PLANNING.
	code, res = env.call("PATCH", "/api/v1/profiles/me/entries/1006", "tv", tokA, map[string]any{})
	require.Equal(t, 200, code, res)
	assert.Equal(t, "PLANNING", dataMap(t, res)["status"])

	body := map[string]any{
		"status": "CURRENT", "scoreRaw": 80, "progress": 4, "repeat": 1, "private": true, "notes": "rewatch with friends",
		"startedAt": map[string]any{"year": 2026, "month": 11, "day": 2}, "customListIds": []float64{listID},
	}
	code, res = env.call("PATCH", "/api/v1/profiles/me/entries/1004", "tv", tokA, body)
	require.Equal(t, 200, code, res)
	d := dataMap(t, res)
	assert.Equal(t, "CURRENT", d["status"])
	assert.Equal(t, float64(80), d["scoreRaw"])
	assert.Equal(t, true, d["private"])
	assert.Equal(t, "rewatch with friends", d["notes"])
	assert.Equal(t, []any{listID}, d["customListIds"])

	code, res = env.call("GET", "/api/v1/profiles/me/entries/1004", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	assert.Equal(t, float64(1), dataMap(t, res)["repeat"])
	code, res = env.call("GET", "/api/v1/profiles/me/entries/1004", "web", tokB, nil)
	require.Equal(t, 200, code, res)
	assert.Nil(t, res["data"], "not in Bruno's list")

	// The raw collection exposes the custom list.
	code, res = env.call("GET", "/api/v1/anilist/collection/raw", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	found := false
	for _, l := range dataMap(t, res)["MediaListCollection"].(map[string]any)["lists"].([]any) {
		lm := l.(map[string]any)
		if lm["isCustomList"] == true && lm["name"] == "Cozy" {
			found = len(lm["entries"].([]any)) == 1
		}
	}
	assert.True(t, found, "custom list with the entry in the raw collection")

	code, res = env.call("PATCH", "/api/v1/profiles/me/entries/1004", "tv", tokA, map[string]any{"scoreRaw": 101})
	assert.Equal(t, http.StatusBadRequest, code, res)
	code, res = env.call("PATCH", "/api/v1/profiles/me/entries/1004", "web", tokB, map[string]any{"customListIds": []float64{listID}})
	assert.Equal(t, http.StatusNotFound, code, "Bruno can't use Ana's list: %v", res)

	// Rename / move.
	lid := fmt.Sprintf("%d", int64(listID))
	code, res = env.call("PATCH", "/api/v1/profiles/me/lists/"+lid, "tv", tokA, map[string]any{"name": "Comfy", "position": 1})
	require.Equal(t, 200, code, res)
	lists := dataList(t, res)
	assert.Equal(t, "Later", lists[0].(map[string]any)["name"])
	assert.Equal(t, "Comfy", lists[1].(map[string]any)["name"])
	code, res = env.call("PATCH", "/api/v1/profiles/me/lists/"+lid, "tv", tokA, map[string]any{"name": "later"})
	assert.Equal(t, http.StatusConflict, code, res)
	code, res = env.call("PATCH", "/api/v1/profiles/me/lists/"+lid, "web", tokB, map[string]any{"name": "Mine"})
	assert.Equal(t, http.StatusNotFound, code, res)

	// Recently watched follows the profile's continuity.
	code, res = env.call("PATCH", "/api/v1/continuity/item", "tv", tokA, map[string]any{"options": map[string]any{"mediaId": 1004, "episodeNumber": 5, "currentTime": 60, "duration": 1380, "kind": "onlinestream"}})
	require.Equal(t, 200, code, res)
	code, res = env.call("GET", "/api/v1/profiles/me/recently-watched", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	rec := dataList(t, res)
	require.Len(t, rec, 1)
	assert.Equal(t, float64(5), rec[0].(map[string]any)["episodeNumber"])
	assert.Equal(t, "CURRENT", rec[0].(map[string]any)["listStatus"])
	code, res = env.call("GET", "/api/v1/profiles/me/recently-watched", "web", tokB, nil)
	require.Equal(t, 200, code, res)
	assert.Empty(t, dataList(t, res))

	// Stats see the edit.
	code, res = env.call("GET", "/api/v1/anilist/stats", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	assert.Equal(t, float64(1), dataMap(t, res)["animeStats"].(map[string]any)["count"], "PLANNING isn't counted")
}

func TestPhase3TokenImportEndpoint(t *testing.T) {
	env, _ := newM2Env(t)
	ana, tokA := env.session("Ana", "tv")
	path := "/api/v1/profiles/" + itoa(ana.ID) + "/import/anilist"

	// A malformed token is refused before anything is sent to AniList.
	code, res := env.call("POST", path, "tv", tokA, map[string]any{"token": "not a token"})
	assert.Equal(t, http.StatusBadRequest, code, res)

	var seen []string
	profileImportSourcesForTest = func() importer.AnilistSources {
		return importer.AnilistSources{NewAccount: func(token string) (importer.AccountFetcher, error) {
			seen = append(seen, token)
			return p3FakeAccount{}, nil
		}}
	}
	t.Cleanup(func() { profileImportSourcesForTest = nil })

	code, res = env.call("POST", path, "tv", tokA, map[string]any{"token": p3FakeToken, "mode": "replace"})
	require.Equal(t, 200, code, res)
	assert.Equal(t, float64(6), dataMap(t, res)["imported"])
	assert.Equal(t, float64(1), dataMap(t, res)["favourites"])
	assert.Equal(t, []string{p3FakeToken}, seen)

	code, res = env.call("GET", "/api/v1/profiles/me/favourites?kind=ANIME", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	require.Len(t, dataList(t, res), 1)

	// The audit names the source, never the token.
	code, res = env.call("GET", "/api/v1/profiles/audit", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	audit := dataList(t, res)
	require.NotEmpty(t, audit)
	assert.Equal(t, "import", audit[0].(map[string]any)["action"])
	assert.Equal(t, "Private AniList list (token)", audit[0].(map[string]any)["detail"])

	// The token isn't anywhere in profiles.db.
	var tables []string
	require.NoError(t, env.svc.DB().Raw("SELECT name FROM sqlite_master WHERE type = 'table'").Scan(&tables).Error)
	for _, table := range tables {
		var rows []map[string]any
		require.NoError(t, env.svc.DB().Raw("SELECT * FROM "+table).Scan(&rows).Error)
		for _, r := range rows {
			assert.False(t, strings.Contains(fmt.Sprint(r), p3FakeToken), "token stored in %s", table)
		}
	}

	// Export carries the favourites and the private/notes fields.
	code, res = env.call("GET", "/api/v1/profiles/"+itoa(ana.ID)+"/export", "tv", tokA, nil)
	require.Equal(t, 200, code, res)
	assert.Len(t, dataMap(t, res)["favourites"].([]any), 1)
}
