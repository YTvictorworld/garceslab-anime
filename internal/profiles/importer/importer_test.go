package importer_test

import (
	"context"
	"encoding/json"
	"seanime/internal/api/anilist"
	"seanime/internal/continuity"
	"seanime/internal/profiles"
	"seanime/internal/profiles/avatars"
	"seanime/internal/profiles/history"
	"seanime/internal/profiles/importer"
	"seanime/internal/profiles/localplatform"
	"seanime/internal/profiles/metadata"
	"seanime/internal/profiles/profilestest"
	"seanime/internal/profiles/settings"
	"seanime/internal/profiles/store"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type env struct {
	svc  *profiles.Service
	db   *gorm.DB
	meta *metadata.Service
	fake *profilestest.FakeAnilist
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open("")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	svc, err := profiles.New(st, profiles.Options{})
	require.NoError(t, err)
	fake := profilestest.NewFakeAnilist(t)
	meta, err := metadata.New(metadata.Options{Client: fake, DB: st.DB()})
	require.NoError(t, err)
	return &env{svc: svc, db: st.DB(), meta: meta, fake: fake}
}

func (e *env) profile(t *testing.T, name string) (*profiles.Profile, *localplatform.Platform, context.Context) {
	t.Helper()
	p, err := e.svc.Create(context.Background(), profiles.CreateInput{Name: name, AvatarID: avatars.Catalog()[0].ID})
	require.NoError(t, err)
	return p, localplatform.New(localplatform.Options{ProfileID: p.ID, DB: e.db, Meta: e.meta}), profiles.WithProfile(context.Background(), p)
}

type authenticated struct{ *profilestest.FakeAnilist }

func (authenticated) IsAuthenticated() bool { return true }

func TestImportPublicAnilist(t *testing.T) {
	e := newEnv(t)
	_, lp, ctx := e.profile(t, "Ana")
	fetcher := importer.ClientFetcher{Client: e.fake}

	_, err := importer.ImportPublicAnilist(ctx, lp, fetcher, "x", localplatform.ImportMerge)
	assert.ErrorIs(t, err, importer.ErrInvalidUsername)
	_, err = importer.ImportPublicAnilist(ctx, lp, fetcher, "SomePrivateUser", localplatform.ImportMerge)
	assert.ErrorIs(t, err, importer.ErrListUnavailable)
	_, err = importer.ImportPublicAnilist(ctx, lp, importer.ClientFetcher{Client: authenticated{e.fake}}, profilestest.FixtureUserName, localplatform.ImportMerge)
	assert.Error(t, err, "a client with a token is refused")

	res, err := importer.ImportPublicAnilist(ctx, lp, fetcher, profilestest.FixtureUserName, localplatform.ImportMerge)
	require.NoError(t, err)
	assert.Equal(t, 6, res.Imported)
	assert.Zero(t, e.fake.Calls("SearchBaseAnimeByIds"), "the collection carries the media: no extra AniList calls")

	col, err := lp.GetRawAnimeCollection(ctx, true)
	require.NoError(t, err)
	en, ok := col.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	require.True(t, ok)
	assert.Equal(t, 85.0, *en.Score)
	assert.Equal(t, "synthetic note", *en.Notes)
	lists, err := lp.CustomLists(ctx)
	require.NoError(t, err)
	require.Len(t, lists, 1)
	assert.Equal(t, "Fixture Custom", lists[0].Name)

	var imported int
	require.NoError(t, e.db.Raw("SELECT COUNT(*) FROM activity_log WHERE kind = 'imported'").Scan(&imported).Error)
	assert.Equal(t, 6, imported)

	// Importing again (merge) changes nothing.
	res, err = importer.ImportPublicAnilist(ctx, lp, fetcher, profilestest.FixtureUserName, localplatform.ImportMerge)
	require.NoError(t, err)
	assert.Zero(t, res.Imported+res.Updated)
}

func TestExportImportRoundTrip(t *testing.T) {
	e := newEnv(t)
	a, lpA, ctxA := e.profile(t, "Ana")
	b, lpB, ctxB := e.profile(t, "Bruno")
	_, err := importer.ImportCollection(ctxA, lpA, profilestest.FixtureAnimeCollection(), localplatform.ImportReplace, "test")
	require.NoError(t, err)
	hs := history.New(e.db)
	require.NoError(t, hs.Set(ctxA, &continuity.WatchHistoryItem{MediaId: profilestest.MediaBeta, EpisodeNumber: 6, CurrentTime: 300, Duration: 1440, Kind: continuity.MediastreamKind}))
	sstore := settings.NewStore(e.db)
	require.NoError(t, sstore.Save(ctxA, a.ID, &settings.ProfileSettings{MarkWatchingOnStart: new(false)}))
	extra := profilestest.FixtureMedia()[0]
	extra.ID = 7777 // only in B's list
	e.fake.SetMedia(extra)
	require.NoError(t, lpB.UpdateEntry(ctxB, extra.ID, new(anilist.MediaListStatusDropped), nil, nil, nil, nil))

	exp, err := importer.BuildExport(ctxA, e.db, lpA, a, time.Now())
	require.NoError(t, err)
	assert.Equal(t, importer.Format, exp.Format)
	assert.Len(t, exp.Entries, 6)
	assert.Len(t, exp.CustomLists, 1)
	assert.Len(t, exp.WatchHistory, 1)
	assert.Len(t, exp.EpisodeProgress, 1)
	assert.False(t, *exp.Settings.MarkWatchingOnStart)
	for _, en := range exp.Entries {
		assert.NotEqual(t, 7777, en.MediaID, "B's data never leaks into A's export")
	}
	_, err = importer.BuildExport(ctxA, e.db, lpA, b, time.Now())
	assert.Error(t, err, "platform/profile mismatch")

	data, err := json.Marshal(exp)
	require.NoError(t, err)
	res, err := importer.ImportJSON(ctxB, e.db, lpB, data, localplatform.ImportReplace)
	require.NoError(t, err)
	assert.Equal(t, 6, res.Imported)

	colB, err := lpB.GetRawAnimeCollection(ctxB, true)
	require.NoError(t, err)
	_, ok := colB.GetListEntryFromAnimeId(7777)
	assert.False(t, ok, "replace removed B's previous list")
	_, ok = colB.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	assert.True(t, ok)
	item, found, err := hs.Get(ctxB, profilestest.MediaBeta)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 6, item.EpisodeNumber)
	listsB, err := lpB.CustomLists(ctxB)
	require.NoError(t, err)
	require.Len(t, listsB, 1)

	// A's data is untouched.
	colA, err := lpA.GetRawAnimeCollection(ctxA, true)
	require.NoError(t, err)
	assert.Len(t, colA.MediaListCollection.Lists, len(colB.MediaListCollection.Lists))
}

func TestParseJSON(t *testing.T) {
	// A simulated platform collection (AniList shape) is accepted.
	entries, exp, err := importer.ParseJSON(profilestest.FixtureAnimeCollectionJSON())
	require.NoError(t, err)
	assert.Nil(t, exp)
	assert.Len(t, entries, 6)

	_, _, err = importer.ParseJSON([]byte(`{"format":"something-else","version":1}`))
	assert.ErrorIs(t, err, importer.ErrInvalidFile)
	_, _, err = importer.ParseJSON([]byte(`{"format":"garceslab-anime-profile","version":99}`))
	assert.ErrorIs(t, err, importer.ErrInvalidFile)
	_, _, err = importer.ParseJSON([]byte(`not json`))
	assert.ErrorIs(t, err, importer.ErrInvalidFile)
}
