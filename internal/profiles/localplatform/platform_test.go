package localplatform_test

import (
	"context"
	"encoding/json"
	"seanime/internal/api/anilist"
	"seanime/internal/extension"
	"seanime/internal/platforms/shared_platform"
	"seanime/internal/profiles"
	"seanime/internal/profiles/avatars"
	"seanime/internal/profiles/localplatform"
	"seanime/internal/profiles/metadata"
	"seanime/internal/profiles/profilestest"
	"seanime/internal/profiles/store"
	"seanime/internal/util"
	"sort"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type env struct {
	svc  *profiles.Service
	db   *gorm.DB
	meta *metadata.Service
	fake *profilestest.FakeAnilist
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open("")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	svc, err := profiles.New(st, profiles.Options{})
	require.NoError(t, err)
	fake := profilestest.NewFakeAnilist(t)
	logger := zerolog.Nop()
	helper := shared_platform.NewPlatformHelper(util.NewRef(extension.NewUnifiedBank()), nil, &logger)
	t.Cleanup(helper.Close)
	e := &env{svc: svc, db: st.DB(), fake: fake, now: time.Date(2026, 11, 23, 20, 0, 0, 0, time.Local)}
	e.meta, err = metadata.New(metadata.Options{Client: fake, DB: st.DB(), Helper: helper, Now: func() time.Time { return e.now }})
	require.NoError(t, err)
	return e
}

func (e *env) profile(t *testing.T, name string) *profiles.Profile {
	t.Helper()
	p, err := e.svc.Create(context.Background(), profiles.CreateInput{Name: name, AvatarID: avatars.Catalog()[0].ID})
	require.NoError(t, err)
	return p
}

func (e *env) platform(p *profiles.Profile) (*localplatform.Platform, context.Context) {
	lp := localplatform.New(localplatform.Options{ProfileID: p.ID, DB: e.db, Meta: e.meta, Now: func() time.Time { return e.now }})
	return lp, profiles.WithProfile(context.Background(), p)
}

func status(s anilist.MediaListStatus) *anilist.MediaListStatus { return &s }

func entryOf(t *testing.T, lp *localplatform.Platform, ctx context.Context, mediaID int) *anilist.AnimeListEntry {
	t.Helper()
	col, err := lp.GetAnimeCollection(ctx, false)
	require.NoError(t, err)
	e, ok := col.GetListEntryFromAnimeId(mediaID)
	require.True(t, ok, "media %d not in the collection", mediaID)
	return e
}

func activityKinds(t *testing.T, db *gorm.DB, pid int64, mediaID int) []string {
	t.Helper()
	var kinds []string
	require.NoError(t, db.Raw("SELECT kind FROM activity_log WHERE profile_id = ? AND media_id = ? ORDER BY id", pid, mediaID).Scan(&kinds).Error)
	return kinds
}

func TestUpdateEntryCreatesAndUpdates(t *testing.T) {
	e := newEnv(t)
	p := e.profile(t, "Ana")
	lp, ctx := e.platform(p)

	// New entry with every field.
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaDelta, status(anilist.MediaListStatusCurrent), new(85), new(3),
		&anilist.FuzzyDateInput{Year: new(2026), Month: new(11), Day: new(1)}, nil))

	en := entryOf(t, lp, ctx, profilestest.MediaDelta)
	assert.Equal(t, anilist.MediaListStatusCurrent, *en.Status)
	assert.Equal(t, 3, *en.Progress)
	assert.Equal(t, 85.0, *en.Score) // POINT_100, like score(format: POINT_100)
	assert.Equal(t, 2026, *en.StartedAt.Year)
	assert.Equal(t, 11, *en.StartedAt.Month)
	assert.Nil(t, en.CompletedAt.Year)
	assert.Equal(t, "Fixture Delta", *en.Media.Title.English)

	// Partial update: nil fields stay, the progress is clamped to the episode count.
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaDelta, nil, nil, new(99), nil, nil))
	en = entryOf(t, lp, ctx, profilestest.MediaDelta)
	assert.Equal(t, 24, *en.Progress)
	assert.Equal(t, 85.0, *en.Score)
	assert.Equal(t, anilist.MediaListStatusCurrent, *en.Status)

	// Score 0 clears the score; an empty date clears the date.
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaDelta, nil, new(0), nil, &anilist.FuzzyDateInput{}, nil))
	en = entryOf(t, lp, ctx, profilestest.MediaDelta)
	assert.Equal(t, 0.0, *en.Score)
	assert.Nil(t, en.StartedAt.Year)
	assert.NotNil(t, en.StartedAt, "AniList always returns the date object")

	assert.Equal(t, []string{"added", "progress", "score", "started", "progress", "score", "started"}, activityKinds(t, e.db, p.ID, profilestest.MediaDelta))
}

func TestUpdateEntryRejectsUnknownMediaAndBadStatus(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	assert.Error(t, lp.UpdateEntry(ctx, 999999, status(anilist.MediaListStatusPlanning), nil, nil, nil, nil))
	assert.Error(t, lp.UpdateEntry(ctx, profilestest.MediaAlpha, status("WATCHING"), nil, nil, nil, nil))
	col, err := lp.GetAnimeCollection(ctx, false)
	require.NoError(t, err)
	assert.Empty(t, col.MediaListCollection.Lists)
}

func TestProgressTransitions(t *testing.T) {
	e := newEnv(t)
	p := e.profile(t, "Ana")
	lp, ctx := e.platform(p)

	// Not in the list: progress adds it as CURRENT, started today.
	require.NoError(t, lp.UpdateEntryProgress(ctx, profilestest.MediaAlpha, 1, new(12)))
	en := entryOf(t, lp, ctx, profilestest.MediaAlpha)
	assert.Equal(t, anilist.MediaListStatusCurrent, *en.Status)
	assert.Equal(t, 2026, *en.StartedAt.Year)

	// Last episode: COMPLETED, completed today.
	require.NoError(t, lp.UpdateEntryProgress(ctx, profilestest.MediaAlpha, 12, new(12)))
	en = entryOf(t, lp, ctx, profilestest.MediaAlpha)
	assert.Equal(t, anilist.MediaListStatusCompleted, *en.Status)
	assert.Equal(t, 12, *en.Progress)
	assert.Equal(t, 23, *en.CompletedAt.Day)
	assert.Equal(t, 0, *en.Repeat)

	// Rewatch: REPEATING stays REPEATING before the end, then COMPLETED with repeat+1
	// and the original completion date.
	e.now = e.now.AddDate(0, 1, 0)
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaAlpha, status(anilist.MediaListStatusRepeating), nil, new(0), nil, nil))
	require.NoError(t, lp.UpdateEntryProgress(ctx, profilestest.MediaAlpha, 5, nil)) // total from the snapshot
	en = entryOf(t, lp, ctx, profilestest.MediaAlpha)
	assert.Equal(t, anilist.MediaListStatusRepeating, *en.Status)
	require.NoError(t, lp.UpdateEntryProgress(ctx, profilestest.MediaAlpha, 12, nil))
	en = entryOf(t, lp, ctx, profilestest.MediaAlpha)
	assert.Equal(t, anilist.MediaListStatusCompleted, *en.Status)
	assert.Equal(t, 1, *en.Repeat)
	assert.Equal(t, 11, *en.CompletedAt.Month, "a rewatch keeps the first completion date")

	// Unknown episode count (airing): never auto-completes.
	require.NoError(t, lp.UpdateEntryProgress(ctx, profilestest.MediaBeta, 7, nil))
	en = entryOf(t, lp, ctx, profilestest.MediaBeta)
	assert.Equal(t, anilist.MediaListStatusCurrent, *en.Status)
	assert.Equal(t, 7, *en.Progress)
}

func TestRepeatDeleteAndAddMedia(t *testing.T) {
	e := newEnv(t)
	p := e.profile(t, "Ana")
	lp, ctx := e.platform(p)

	assert.ErrorIs(t, lp.UpdateEntryRepeat(ctx, profilestest.MediaGamma, 2), localplatform.ErrMediaNotFound)
	require.NoError(t, lp.AddMediaToCollection(ctx, []int{profilestest.MediaGamma, profilestest.MediaZeta, 424242}))
	en := entryOf(t, lp, ctx, profilestest.MediaGamma)
	assert.Equal(t, anilist.MediaListStatusPlanning, *en.Status)
	require.NoError(t, lp.UpdateEntryRepeat(ctx, profilestest.MediaGamma, 2))
	assert.Equal(t, 2, *entryOf(t, lp, ctx, profilestest.MediaGamma).Repeat)

	// AddMediaToCollection never touches an existing entry.
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaZeta, status(anilist.MediaListStatusDropped), nil, nil, nil, nil))
	require.NoError(t, lp.AddMediaToCollection(ctx, []int{profilestest.MediaZeta}))
	assert.Equal(t, anilist.MediaListStatusDropped, *entryOf(t, lp, ctx, profilestest.MediaZeta).Status)

	// Delete by entry id (as the handler does) and by media id.
	gamma := entryOf(t, lp, ctx, profilestest.MediaGamma)
	require.NoError(t, lp.DeleteEntry(ctx, profilestest.MediaGamma, gamma.ID))
	require.NoError(t, lp.DeleteEntry(ctx, profilestest.MediaZeta, 0))
	col, err := lp.GetAnimeCollection(ctx, true)
	require.NoError(t, err)
	assert.Empty(t, col.MediaListCollection.Lists)
	assert.ErrorIs(t, lp.DeleteEntry(ctx, profilestest.MediaZeta, 0), localplatform.ErrMediaNotFound)
	assert.Contains(t, activityKinds(t, e.db, p.ID, profilestest.MediaGamma), "deleted")
}

func TestCustomListsAndHiddenEntries(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaAlpha, status(anilist.MediaListStatusCompleted), nil, nil, nil, nil))
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaDelta, status(anilist.MediaListStatusPaused), nil, nil, nil, nil))

	id, err := lp.CreateCustomList(ctx, "Favourites to rewatch")
	require.NoError(t, err)
	again, err := lp.CreateCustomList(ctx, "favourites TO rewatch") // NOCASE: same list
	require.NoError(t, err)
	assert.Equal(t, id, again)
	require.NoError(t, lp.SetInCustomList(ctx, id, profilestest.MediaAlpha, true))
	assert.ErrorIs(t, lp.SetInCustomList(ctx, id, profilestest.MediaGamma, true), localplatform.ErrMediaNotFound)

	raw, err := lp.GetRawAnimeCollection(ctx, false)
	require.NoError(t, err)
	var custom *anilist.AnimeCollection_MediaListCollection_Lists
	for _, l := range raw.MediaListCollection.Lists {
		if *l.IsCustomList {
			custom = l
		}
	}
	require.NotNil(t, custom)
	assert.Nil(t, custom.Status)
	assert.Equal(t, "Favourites to rewatch", *custom.Name)
	require.Len(t, custom.Entries, 1)
	assert.Equal(t, profilestest.MediaAlpha, custom.Entries[0].Media.ID)

	// The non-raw collection has no custom lists (like the AniList platform).
	col, err := lp.GetAnimeCollection(ctx, false)
	require.NoError(t, err)
	for _, l := range col.MediaListCollection.Lists {
		assert.False(t, *l.IsCustomList)
	}

	lists, err := lp.CustomLists(ctx)
	require.NoError(t, err)
	require.Len(t, lists, 1)
	assert.Equal(t, []int{profilestest.MediaAlpha}, lists[0].MediaIDs)

	require.NoError(t, lp.SetInCustomList(ctx, id, profilestest.MediaAlpha, false))
	require.NoError(t, lp.DeleteCustomList(ctx, id))
	assert.ErrorIs(t, lp.DeleteCustomList(ctx, id), localplatform.ErrCustomListNotFound)
}

func TestScoreConversion(t *testing.T) {
	cases := []struct {
		raw    int
		format string
		want   float64
	}{
		{85, localplatform.ScorePoint100, 85},
		{85, localplatform.ScorePoint10Dec, 8.5},
		{85, localplatform.ScorePoint10, 9},
		{84, localplatform.ScorePoint10, 8},
		{85, localplatform.ScorePoint5, 4},
		{90, localplatform.ScorePoint5, 5},
		{30, localplatform.ScorePoint3, 1},
		{50, localplatform.ScorePoint3, 2},
		{61, localplatform.ScorePoint3, 3},
		{0, localplatform.ScorePoint10Dec, 0},
		{150, localplatform.ScorePoint100, 100},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, localplatform.ScoreForFormat(c.raw, c.format), "%d %s", c.raw, c.format)
	}
	assert.Equal(t, 85, localplatform.ScoreRawFromFormat(8.5, localplatform.ScorePoint10Dec))
	assert.Equal(t, 80, localplatform.ScoreRawFromFormat(4, localplatform.ScorePoint5))
	assert.Equal(t, 0, localplatform.ScoreRawFromFormat(0, localplatform.ScorePoint3))
	assert.Equal(t, 100, localplatform.ScoreRawFromFormat(12, localplatform.ScorePoint10))
}

func TestFuzzyDatesArePartialAndValidated(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	// Year only; a day without a month is dropped; month 13 is invalid.
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaAlpha, status(anilist.MediaListStatusCompleted), nil, nil,
		&anilist.FuzzyDateInput{Year: new(2019), Day: new(5)},
		&anilist.FuzzyDateInput{Year: new(2019), Month: new(13), Day: new(2)}))
	en := entryOf(t, lp, ctx, profilestest.MediaAlpha)
	assert.Equal(t, 2019, *en.StartedAt.Year)
	assert.Nil(t, en.StartedAt.Month)
	assert.Nil(t, en.StartedAt.Day)
	assert.Equal(t, 2019, *en.CompletedAt.Year)
	assert.Nil(t, en.CompletedAt.Month)
}

func TestStats(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaAlpha, status(anilist.MediaListStatusCompleted), new(80), new(12), nil, nil))
	require.NoError(t, lp.UpdateEntryRepeat(ctx, profilestest.MediaAlpha, 1))
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaDelta, status(anilist.MediaListStatusCurrent), new(60), new(4), nil, nil))
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaZeta, status(anilist.MediaListStatusPlanning), nil, nil, nil, nil))

	vs, err := lp.GetViewerStats(ctx)
	require.NoError(t, err)
	st := vs.Viewer.Statistics.Anime
	assert.Equal(t, 2, st.Count) // planned media aren't counted, like AniList
	assert.Equal(t, 12+12+4, st.EpisodesWatched)
	assert.Equal(t, (12+12)*24+4*23, st.MinutesWatched)
	assert.InDelta(t, 70.0, st.MeanScore, 0.001)
	statuses := map[string]int{}
	for _, s := range st.Statuses {
		statuses[string(*s.Status)] = s.Count
	}
	assert.Equal(t, map[string]int{"COMPLETED": 1, "CURRENT": 1, "PLANNING": 1}, statuses)
	genres := map[string]int{}
	for _, g := range st.Genres {
		genres[*g.Genre] = g.Count
	}
	assert.Equal(t, 2, genres["Action"])

	ret, err := anilist.GetStats(ctx, vs)
	require.NoError(t, err)
	assert.Equal(t, 2, ret.AnimeStats.Count)
}

func TestMangaIsEmpty(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	m, err := lp.GetMangaCollection(ctx, false)
	require.NoError(t, err)
	assert.Empty(t, m.MediaListCollection.Lists)
	m, err = lp.GetRawMangaCollection(ctx, true)
	require.NoError(t, err)
	assert.Empty(t, m.MediaListCollection.Lists)
}

func TestCollectionSurvivesAniListDown(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaAlpha, status(anilist.MediaListStatusCurrent), nil, new(2), nil, nil))
	e.fake.Fail = true
	col, err := lp.GetAnimeCollection(ctx, true)
	require.NoError(t, err)
	_, ok := col.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	assert.True(t, ok, "the list is built from media_snapshots")
}

// TestShapeMatchesAniList imports the synthetic AniList-shaped collection and
// compares the collection the local platform builds with it: same lists, same
// entry fields, and every JSON key AniList returns is present.
func TestShapeMatchesAniList(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	fixture := profilestest.FixtureAnimeCollection()
	res, err := lp.Import(ctx, localplatform.FromAnilistCollection(fixture), localplatform.ImportReplace, "test")
	require.NoError(t, err)
	assert.Equal(t, 6, res.Imported)

	raw, err := lp.GetRawAnimeCollection(ctx, true)
	require.NoError(t, err)

	type listKey struct {
		status string
		name   string
		custom bool
	}
	key := func(l *anilist.AnimeCollection_MediaListCollection_Lists) listKey {
		st := ""
		if l.Status != nil {
			st = string(*l.Status)
		}
		return listKey{st, *l.Name, *l.IsCustomList}
	}
	got := map[listKey][]*anilist.AnimeCollection_MediaListCollection_Lists_Entries{}
	for _, l := range raw.MediaListCollection.Lists {
		got[key(l)] = l.Entries
	}
	for _, want := range fixture.MediaListCollection.Lists {
		entries, ok := got[key(want)]
		require.True(t, ok, "list %+v missing", key(want))
		require.Len(t, entries, len(want.Entries), "list %+v", key(want))
		byMedia := map[int]*anilist.AnimeCollection_MediaListCollection_Lists_Entries{}
		for _, en := range entries {
			byMedia[en.Media.ID] = en
		}
		for _, w := range want.Entries {
			g := byMedia[w.Media.ID]
			require.NotNil(t, g, "media %d", w.Media.ID)
			assert.Equal(t, *w.Status, *g.Status)
			assert.Equal(t, *w.Progress, *g.Progress)
			assert.Equal(t, *w.Score, *g.Score)
			assert.Equal(t, *w.Repeat, *g.Repeat)
			assert.Equal(t, *w.Private, *g.Private)
			assert.Equal(t, w.Notes, g.Notes)
			assert.Equal(t, *w.StartedAt, *g.StartedAt)
			assert.Equal(t, *w.CompletedAt, *g.CompletedAt)
			assert.Equal(t, *w.CreatedAt, *g.CreatedAt)
			assert.Equal(t, *w.UpdatedAt, *g.UpdatedAt)
			assert.Equal(t, w.Media, g.Media)
		}
	}

	// JSON keys: AniList's raw response vs ours (null values keep their keys there).
	var fixtureJSON map[string]any
	require.NoError(t, json.Unmarshal(profilestest.FixtureAnimeCollectionJSON(), &fixtureJSON))
	ours, err := json.Marshal(raw)
	require.NoError(t, err)
	var oursJSON map[string]any
	require.NoError(t, json.Unmarshal(ours, &oursJSON))
	wantKeys := entryKeys(fixtureJSON)
	gotKeys := entryKeys(oursJSON)
	for _, k := range []string{"id", "createdAt", "updatedAt", "score", "progress", "status", "repeat", "private", "startedAt", "completedAt", "media"} {
		assert.Contains(t, wantKeys, k)
		assert.Contains(t, gotKeys, k, "entry key %q missing", k)
	}
	assert.Subset(t, listKeys(fixtureJSON), []string{"status", "name", "isCustomList", "entries"})
	assert.Subset(t, listKeys(oursJSON), []string{"name", "isCustomList", "entries"})
}

func entryKeys(col map[string]any) []string {
	set := map[string]bool{}
	for _, l := range col["MediaListCollection"].(map[string]any)["lists"].([]any) {
		for _, e := range l.(map[string]any)["entries"].([]any) {
			for k := range e.(map[string]any) {
				set[k] = true
			}
		}
	}
	return sortedSet(set)
}

func listKeys(col map[string]any) []string {
	set := map[string]bool{}
	for _, l := range col["MediaListCollection"].(map[string]any)["lists"].([]any) {
		for k := range l.(map[string]any) {
			set[k] = true
		}
	}
	return sortedSet(set)
}

func sortedSet(set map[string]bool) []string {
	ret := make([]string, 0, len(set))
	for k := range set {
		ret = append(ret, k)
	}
	sort.Strings(ret)
	return ret
}

func TestImportMergeKeepsNewest(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaAlpha, status(anilist.MediaListStatusCurrent), nil, new(4), nil, nil))
	// Older incoming entry: kept as is. Newer: replaced.
	res, err := lp.Import(ctx, []localplatform.ProfileImportEntry{
		{MediaID: profilestest.MediaAlpha, Status: "COMPLETED", Progress: 12, UpdatedAt: 1000},
		{MediaID: profilestest.MediaDelta, Status: "PAUSED", Progress: 3, UpdatedAt: 1000},
		{MediaID: profilestest.MediaGamma, Status: "BOGUS"},
	}, localplatform.ImportMerge, "test")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Imported)
	assert.Equal(t, 2, res.Skipped)
	assert.Equal(t, 4, *entryOf(t, lp, ctx, profilestest.MediaAlpha).Progress)

	res, err = lp.Import(ctx, []localplatform.ProfileImportEntry{
		{MediaID: profilestest.MediaAlpha, Status: "COMPLETED", Progress: 12, UpdatedAt: e.now.Unix() + 60},
	}, localplatform.ImportMerge, "test")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Updated)
	assert.Equal(t, anilist.MediaListStatusCompleted, *entryOf(t, lp, ctx, profilestest.MediaAlpha).Status)
}

func TestPurgeCascadesListData(t *testing.T) {
	e := newEnv(t)
	p := e.profile(t, "Ana")
	lp, ctx := e.platform(p)
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaAlpha, status(anilist.MediaListStatusCurrent), nil, new(1), nil, nil))
	id, err := lp.CreateCustomList(ctx, "L")
	require.NoError(t, err)
	require.NoError(t, lp.SetInCustomList(ctx, id, profilestest.MediaAlpha, true))

	require.NoError(t, e.svc.Delete(ctx, p.ID))
	_, err = e.svc.PurgeDeleted(ctx, 0)
	require.NoError(t, err)
	for _, table := range []string{"list_entries", "custom_lists", "activity_log"} {
		var n int
		require.NoError(t, e.db.Raw("SELECT COUNT(*) FROM "+table+" WHERE profile_id = ?", p.ID).Scan(&n).Error)
		assert.Zero(t, n, table)
	}
	var items, snaps int
	require.NoError(t, e.db.Raw("SELECT COUNT(*) FROM custom_list_items").Scan(&items).Error)
	require.NoError(t, e.db.Raw("SELECT COUNT(*) FROM media_snapshots").Scan(&snaps).Error)
	assert.Zero(t, items)
	assert.NotZero(t, snaps, "snapshots are global")
}
