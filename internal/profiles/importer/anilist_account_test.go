package importer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"seanime/internal/api/anilist"
	"seanime/internal/profiles/importer"
	"seanime/internal/profiles/localplatform"
	"seanime/internal/profiles/profilestest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeToken is a made-up token: the tests never use a real one.
const fakeToken = "fake-test-token-0123456789abcdefghijklmnopqrstuvwxyz"

// fakeAccount is the AniList account of fakeToken.
type fakeAccount struct {
	token     string
	favs      *importer.AnilistFavourites
	favErr    error
	viewerErr error
}

func (a *fakeAccount) ViewerName(context.Context) (string, error) {
	if a.viewerErr != nil {
		return "", a.viewerErr
	}
	return profilestest.FixtureUserName, nil
}

func (a *fakeAccount) ViewerAnimeCollection(_ context.Context, name string) (*anilist.AnimeCollection, error) {
	if name != profilestest.FixtureUserName {
		return nil, errors.New("unknown user")
	}
	return profilestest.FixtureAnimeCollection(), nil
}

func (a *fakeAccount) ViewerFavourites(context.Context) (*importer.AnilistFavourites, error) {
	return a.favs, a.favErr
}

type fakePublicFavs struct {
	favs map[string]*importer.AnilistFavourites
}

func (f fakePublicFavs) UserFavourites(_ context.Context, name string) (*importer.AnilistFavourites, error) {
	if v, ok := f.favs[name]; ok {
		return v, nil
	}
	return nil, errors.New("private or unknown user")
}

func TestImportAnilistWithToken(t *testing.T) {
	e := newEnv(t)
	_, lp, ctx := e.profile(t, "Ana")
	_, lpB, ctxB := e.profile(t, "Bob")
	acc := &fakeAccount{favs: &importer.AnilistFavourites{Anime: []int{profilestest.MediaDelta, profilestest.MediaAlpha}, Characters: []int{5}}}
	var gotTokens []string
	src := importer.AnilistSources{
		// The public reader must not be used for a token import.
		Public: importer.ClientFetcher{Client: nil},
		NewAccount: func(token string) (importer.AccountFetcher, error) {
			gotTokens = append(gotTokens, token)
			return acc, nil
		},
	}

	res, err := importer.ImportAnilist(ctx, lp, src, importer.AnilistImportOptions{Token: fakeToken, Mode: localplatform.ImportMerge, Favourites: true})
	require.NoError(t, err)
	assert.Equal(t, []string{fakeToken}, gotTokens)
	assert.Equal(t, 6, res.Imported)
	assert.Equal(t, 3, res.Favourites)

	// Private entries come along with the token.
	d, err := lp.EntryDetails(ctx, profilestest.MediaDelta)
	require.NoError(t, err)
	assert.True(t, d.Private)
	assert.True(t, d.IsFavourite)
	favs, err := lp.Favourites(ctx, "ANIME")
	require.NoError(t, err)
	assert.Equal(t, []int{profilestest.MediaDelta, profilestest.MediaAlpha}, favIDs(favs), "AniList's favourite order")
	var source string
	require.NoError(t, e.db.Raw("SELECT old_value FROM activity_log WHERE kind = 'imported' LIMIT 1").Scan(&source).Error)
	assert.Equal(t, "anilist-token:"+profilestest.FixtureUserName, source)

	// The token is never written to profiles.db.
	assertNotStored(t, e, fakeToken)

	// Bob is untouched.
	favsB, _ := lpB.Favourites(ctxB, "")
	assert.Empty(t, favsB)
	colB, _ := lpB.GetRawAnimeCollection(ctxB, true)
	assert.Empty(t, colB.MediaListCollection.Lists)

	// A refused token and a favourites failure.
	_, err = importer.ImportAnilist(ctx, lp, importer.AnilistSources{NewAccount: func(string) (importer.AccountFetcher, error) {
		return &fakeAccount{viewerErr: importer.ErrInvalidToken}, nil
	}}, importer.AnilistImportOptions{Token: fakeToken, Mode: localplatform.ImportMerge})
	assert.ErrorIs(t, err, importer.ErrInvalidToken)
	res, err = importer.ImportAnilist(ctx, lp, importer.AnilistSources{NewAccount: func(string) (importer.AccountFetcher, error) {
		return &fakeAccount{favErr: errors.New("rate limited")}, nil
	}}, importer.AnilistImportOptions{Token: fakeToken, Mode: localplatform.ImportMerge, Favourites: true})
	require.NoError(t, err, "the list is imported even if the favourites fail")
	assert.NotEmpty(t, res.Errors)
}

// assertNotStored scans every column of every table for s.
func assertNotStored(t *testing.T, e *env, s string) {
	t.Helper()
	var tables []string
	require.NoError(t, e.db.Raw("SELECT name FROM sqlite_master WHERE type = 'table'").Scan(&tables).Error)
	for _, table := range tables {
		var rows []map[string]any
		require.NoError(t, e.db.Raw("SELECT * FROM "+table).Scan(&rows).Error)
		for _, r := range rows {
			assert.NotContains(t, fmt.Sprint(r), s, "token stored in %s", table)
		}
	}
}

func TestImportAnilistPublicFavourites(t *testing.T) {
	e := newEnv(t)
	_, lp, ctx := e.profile(t, "Ana")
	src := importer.AnilistSources{
		Public:           importer.ClientFetcher{Client: e.fake},
		PublicFavourites: fakePublicFavs{favs: map[string]*importer.AnilistFavourites{profilestest.FixtureUserName: {Anime: []int{profilestest.MediaGamma}}}},
		NewAccount: func(string) (importer.AccountFetcher, error) {
			t.Fatal("no token was given")
			return nil, nil
		},
	}
	_, err := importer.ImportAnilist(ctx, lp, src, importer.AnilistImportOptions{Username: "x", Mode: localplatform.ImportMerge})
	assert.ErrorIs(t, err, importer.ErrInvalidUsername)

	res, err := importer.ImportAnilist(ctx, lp, src, importer.AnilistImportOptions{Username: profilestest.FixtureUserName, Mode: localplatform.ImportMerge, Favourites: true})
	require.NoError(t, err)
	assert.Equal(t, 6, res.Imported)
	assert.Equal(t, 1, res.Favourites)

	// Favourites off: only the list.
	_, lp2, ctx2 := e.profile(t, "Bob")
	res, err = importer.ImportAnilist(ctx2, lp2, src, importer.AnilistImportOptions{Username: profilestest.FixtureUserName, Mode: localplatform.ImportMerge})
	require.NoError(t, err)
	assert.Zero(t, res.Favourites)
	favs, _ := lp2.Favourites(ctx2, "")
	assert.Empty(t, favs)
}

func TestNormalizeToken(t *testing.T) {
	got, err := importer.NormalizeToken("  Bearer " + fakeToken + "\n")
	require.NoError(t, err)
	assert.Equal(t, fakeToken, got)
	for _, bad := range []string{"", "short", "has space " + fakeToken, strings.Repeat("a", importer.MaxTokenLength+1)} {
		_, err := importer.NormalizeToken(bad)
		assert.ErrorIs(t, err, importer.ErrInvalidToken, bad)
	}
}

// favouritesPage is an AniList favourites response page, as anilist.CustomQuery
// returns it (decoded JSON).
func favouritesPage(t *testing.T, root string, anime []int, hasNext bool) interface{} {
	nodes := make([]map[string]int, 0)
	for _, id := range anime {
		nodes = append(nodes, map[string]int{"id": id})
	}
	empty := map[string]any{"pageInfo": map[string]bool{"hasNextPage": false}, "nodes": []any{}}
	raw, err := json.Marshal(map[string]any{root: map[string]any{"favourites": map[string]any{
		"anime":      map[string]any{"pageInfo": map[string]bool{"hasNextPage": hasNext}, "nodes": nodes},
		"manga":      empty,
		"characters": map[string]any{"pageInfo": map[string]bool{"hasNextPage": false}, "nodes": []map[string]int{{"id": 9}}},
		"staff":      empty,
		"studios":    empty,
	}}})
	require.NoError(t, err)
	var data interface{}
	require.NoError(t, json.Unmarshal(raw, &data))
	return data
}

func TestPublicFavouritesPaging(t *testing.T) {
	var calls []map[string]interface{}
	query := func(body map[string]interface{}, _ *zerolog.Logger, token string) (interface{}, error) {
		assert.Empty(t, token, "public favourites are anonymous")
		vars := body["variables"].(map[string]interface{})
		calls = append(calls, vars)
		assert.Contains(t, body["query"], "User(name: $name)")
		if vars["page"] == 1 {
			return favouritesPage(t, "User", []int{1, 2}, true), nil
		}
		return favouritesPage(t, "User", []int{3}, false), nil
	}
	favs, err := importer.PublicFavourites{Query: query}.UserFavourites(context.Background(), "someone")
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, favs.Anime)
	assert.Equal(t, []int{9, 9}, favs.Characters, "one per page in this fake")
	require.Len(t, calls, 2)
	assert.Equal(t, "someone", calls[0]["name"])

	// Unknown user: no User in the response.
	_, err = importer.PublicFavourites{Query: func(map[string]interface{}, *zerolog.Logger, string) (interface{}, error) {
		return map[string]interface{}{"User": nil}, nil
	}}.UserFavourites(context.Background(), "nobody")
	assert.ErrorIs(t, err, importer.ErrListUnavailable)
}

func TestExportImportFavouritesRoundTrip(t *testing.T) {
	e := newEnv(t)
	a, lpA, ctxA := e.profile(t, "Ana")
	_, lpB, ctxB := e.profile(t, "Bruno")
	require.NoError(t, lpA.EditEntry(ctxA, profilestest.MediaAlpha, localplatform.ProfileEntryEdit{Status: new("CURRENT"), Notes: new("n"), Private: new(true)}))
	require.NoError(t, lpA.AddFavourite(ctxA, "ANIME", profilestest.MediaDelta))
	require.NoError(t, lpA.AddFavourite(ctxA, "ANIME", profilestest.MediaAlpha))
	require.NoError(t, lpA.AddFavourite(ctxA, "STUDIO", 42))

	exp, err := importer.BuildExport(ctxA, e.db, lpA, a, time.Now())
	require.NoError(t, err)
	assert.Len(t, exp.Favourites, 3)
	data, err := json.Marshal(exp)
	require.NoError(t, err)
	for _, key := range []string{`"version"`, `"profile"`, `"settings"`, `"entries"`, `"customLists"`, `"favourites"`, `"watchHistory"`, `"episodeProgress"`, `"activity"`} {
		assert.Contains(t, string(data), key)
	}

	res, err := importer.ImportJSON(ctxB, e.db, lpB, data, localplatform.ImportReplace)
	require.NoError(t, err)
	assert.Equal(t, 3, res.Favourites)
	favs, err := lpB.Favourites(ctxB, "ANIME")
	require.NoError(t, err)
	assert.Equal(t, []int{profilestest.MediaDelta, profilestest.MediaAlpha}, favIDs(favs))
	d, err := lpB.EntryDetails(ctxB, profilestest.MediaAlpha)
	require.NoError(t, err)
	assert.True(t, d.Private)
	assert.Equal(t, "n", *d.Notes)

	// Favourites only (no entries) is still something to import.
	_, lpC, ctxC := e.profile(t, "Carla")
	res, err = importer.ImportJSON(ctxC, e.db, lpC, []byte(`{"format":"garceslab-anime-profile","version":1,"favourites":[{"kind":"ANIME","refId":1001,"position":0}]}`), localplatform.ImportMerge)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Favourites)
}

func favIDs(favs []*localplatform.ProfileFavourite) []int {
	ret := make([]int, 0, len(favs))
	for _, f := range favs {
		ret = append(ret, f.RefID)
	}
	return ret
}
