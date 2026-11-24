package localplatform_test

import (
	"seanime/internal/api/anilist"
	"seanime/internal/profiles/localplatform"
	"seanime/internal/profiles/profilestest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func favIDs(favs []*localplatform.ProfileFavourite) []int {
	ret := make([]int, 0, len(favs))
	for _, f := range favs {
		ret = append(ret, f.RefID)
	}
	return ret
}

func TestFavourites(t *testing.T) {
	e := newEnv(t)
	ana, bob := e.profile(t, "Ana"), e.profile(t, "Bob")
	lp, ctx := e.platform(ana)
	lpB, ctxB := e.platform(bob)

	require.NoError(t, lp.AddFavourite(ctx, "anime", profilestest.MediaDelta))
	require.NoError(t, lp.AddFavourite(ctx, "ANIME", profilestest.MediaAlpha))
	require.NoError(t, lp.AddFavourite(ctx, "ANIME", profilestest.MediaDelta)) // no duplicate
	require.NoError(t, lp.AddFavourite(ctx, "character", 77))                  // no metadata check for characters
	assert.ErrorIs(t, lp.AddFavourite(ctx, "ANIME", 999999), localplatform.ErrMediaNotFound)
	assert.ErrorIs(t, lp.AddFavourite(ctx, "SONG", 1), localplatform.ErrInvalidFavouriteKind)

	favs, err := lp.Favourites(ctx, "ANIME")
	require.NoError(t, err)
	assert.Equal(t, []int{profilestest.MediaDelta, profilestest.MediaAlpha}, favIDs(favs), "in the order they were added")
	require.NotNil(t, favs[0].Media)
	assert.Equal(t, profilestest.MediaDelta, favs[0].Media.ID)
	all, err := lp.Favourites(ctx, "")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	ok, err := lp.IsFavourite(ctx, "ANIME", profilestest.MediaAlpha)
	require.NoError(t, err)
	assert.True(t, ok)

	// Isolation: Bob has none and his writes don't touch Ana's.
	favsB, err := lpB.Favourites(ctxB, "")
	require.NoError(t, err)
	assert.Empty(t, favsB)
	require.NoError(t, lpB.RemoveFavourite(ctxB, "ANIME", profilestest.MediaAlpha))
	ok, _ = lp.IsFavourite(ctx, "ANIME", profilestest.MediaAlpha)
	assert.True(t, ok)

	require.NoError(t, lp.RemoveFavourite(ctx, "ANIME", profilestest.MediaAlpha))
	require.NoError(t, lp.RemoveFavourite(ctx, "ANIME", profilestest.MediaAlpha)) // idempotent
	favs, _ = lp.Favourites(ctx, "ANIME")
	assert.Equal(t, []int{profilestest.MediaDelta}, favIDs(favs))

	// Import: merge adds, replace swaps the imported kinds only.
	n, err := lp.ImportFavourites(ctx, []localplatform.ProfileFavourite{
		{Kind: "ANIME", RefID: profilestest.MediaGamma}, {Kind: "ANIME", RefID: profilestest.MediaDelta}, {Kind: "BAD", RefID: 1}, {Kind: "ANIME", RefID: -1},
	}, localplatform.ImportMerge)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, err = lp.ImportFavourites(ctx, []localplatform.ProfileFavourite{{Kind: "ANIME", RefID: profilestest.MediaEpsilon}}, localplatform.ImportReplace)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	favs, _ = lp.Favourites(ctx, "ANIME")
	assert.Equal(t, []int{profilestest.MediaEpsilon}, favIDs(favs))
	chars, _ := lp.Favourites(ctx, "CHARACTER")
	assert.Equal(t, []int{77}, favIDs(chars), "replace only touches the imported kinds")

	// Purging the profile removes its favourites (FK cascade), not Bob's.
	require.NoError(t, lpB.AddFavourite(ctxB, "ANIME", profilestest.MediaAlpha))
	require.NoError(t, e.svc.Delete(ctx, ana.ID))
	_, err = e.svc.PurgeDeleted(ctx, 0)
	require.NoError(t, err)
	var left, bobs int
	require.NoError(t, e.db.Raw("SELECT COUNT(*) FROM favourites WHERE profile_id = ?", ana.ID).Scan(&left).Error)
	require.NoError(t, e.db.Raw("SELECT COUNT(*) FROM favourites WHERE profile_id = ?", bob.ID).Scan(&bobs).Error)
	assert.Zero(t, left)
	assert.Equal(t, 1, bobs)
}

func TestUpdateCustomList(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	lpB, ctxB := e.platform(e.profile(t, "Bob"))
	a, err := lp.CreateCustomList(ctx, "A")
	require.NoError(t, err)
	b, _ := lp.CreateCustomList(ctx, "B")
	c, _ := lp.CreateCustomList(ctx, "C")

	require.NoError(t, lp.UpdateCustomList(ctx, b, new("Bee"), nil))
	assert.ErrorIs(t, lp.UpdateCustomList(ctx, c, new("a"), nil), localplatform.ErrDuplicateListName)
	assert.ErrorIs(t, lp.UpdateCustomList(ctx, c, new("  "), nil), localplatform.ErrInvalidListName)
	require.NoError(t, lp.UpdateCustomList(ctx, c, new("c"), nil), "changing only the case is fine")
	require.NoError(t, lp.UpdateCustomList(ctx, c, nil, new(0)))
	names := func() []string {
		lists, err := lp.CustomLists(ctx)
		require.NoError(t, err)
		ret := []string{}
		for _, l := range lists {
			ret = append(ret, l.Name)
		}
		return ret
	}
	assert.Equal(t, []string{"c", "A", "Bee"}, names())
	require.NoError(t, lp.UpdateCustomList(ctx, c, nil, new(99))) // clamped to the end
	assert.Equal(t, []string{"A", "Bee", "c"}, names())

	// Another profile can't rename or move it.
	assert.ErrorIs(t, lpB.UpdateCustomList(ctxB, a, new("Mine"), nil), localplatform.ErrCustomListNotFound)
	assert.Equal(t, []string{"A", "Bee", "c"}, names())

	raw, err := lp.GetRawAnimeCollection(ctx, false)
	require.NoError(t, err)
	var custom []string
	for _, l := range raw.MediaListCollection.Lists {
		if *l.IsCustomList {
			custom = append(custom, *l.Name)
		}
	}
	assert.Equal(t, []string{"A", "Bee", "c"}, custom, "the collection follows the new order and names")
}

func TestEditEntry(t *testing.T) {
	e := newEnv(t)
	ana, bob := e.profile(t, "Ana"), e.profile(t, "Bob")
	lp, ctx := e.platform(ana)
	lpB, ctxB := e.platform(bob)
	l1, err := lp.CreateCustomList(ctx, "One")
	require.NoError(t, err)
	l2, _ := lp.CreateCustomList(ctx, "Two")
	bobList, _ := lpB.CreateCustomList(ctxB, "Bob's")

	started := localplatform.ProfileFuzzyDate{Year: new(2026), Month: new(11), Day: new(1)}
	require.NoError(t, lp.EditEntry(ctx, profilestest.MediaAlpha, localplatform.ProfileEntryEdit{
		Status: new("CURRENT"), ScoreRaw: new(75), Progress: new(3), Repeat: new(1), Private: new(true),
		Notes: new("great opening"), StartedAt: &started, CustomListIDs: &[]int64{l1, l2},
	}))
	d, err := lp.EntryDetails(ctx, profilestest.MediaAlpha)
	require.NoError(t, err)
	assert.Equal(t, "CURRENT", d.Status)
	assert.Equal(t, 75, *d.ScoreRaw)
	assert.Equal(t, 3, d.Progress)
	assert.Equal(t, 1, d.Repeat)
	assert.True(t, d.Private)
	assert.Equal(t, "great opening", *d.Notes)
	assert.Equal(t, 2026, *d.StartedAt.Year)
	assert.Equal(t, []int64{l1, l2}, d.CustomListIDs)
	assert.False(t, d.IsFavourite)

	en := entryOf(t, lp, ctx, profilestest.MediaAlpha)
	assert.True(t, *en.Private)
	assert.Equal(t, "great opening", *en.Notes)

	// Partial edit: clear the notes, the start date and the score, keep the rest.
	require.NoError(t, lp.EditEntry(ctx, profilestest.MediaAlpha, localplatform.ProfileEntryEdit{
		Notes: new(""), StartedAt: &localplatform.ProfileFuzzyDate{}, ScoreRaw: new(0), CustomListIDs: &[]int64{l2},
	}))
	d, _ = lp.EntryDetails(ctx, profilestest.MediaAlpha)
	assert.Nil(t, d.Notes)
	assert.True(t, d.StartedAt.IsZero())
	assert.Nil(t, d.ScoreRaw)
	assert.True(t, d.Private)
	assert.Equal(t, 3, d.Progress)
	assert.Equal(t, []int64{l2}, d.CustomListIDs)

	// Validation and isolation.
	assert.ErrorIs(t, lp.EditEntry(ctx, profilestest.MediaAlpha, localplatform.ProfileEntryEdit{ScoreRaw: new(101)}), localplatform.ErrInvalidEntry)
	assert.ErrorIs(t, lp.EditEntry(ctx, profilestest.MediaAlpha, localplatform.ProfileEntryEdit{Status: new("WATCHING")}), localplatform.ErrInvalidEntry)
	assert.ErrorIs(t, lp.EditEntry(ctx, profilestest.MediaAlpha, localplatform.ProfileEntryEdit{CustomListIDs: &[]int64{bobList}}),
		localplatform.ErrCustomListNotFound, "another profile's custom list")
	_, err = lpB.EntryDetails(ctxB, profilestest.MediaAlpha)
	assert.ErrorIs(t, err, localplatform.ErrMediaNotFound)

	// A new entry without a status is PLANNING ("Add to my list").
	require.NoError(t, lp.EditEntry(ctx, profilestest.MediaZeta, localplatform.ProfileEntryEdit{}))
	d, _ = lp.EntryDetails(ctx, profilestest.MediaZeta)
	assert.Equal(t, "PLANNING", d.Status)

	// Hidden from the status lists: only in its custom lists.
	require.NoError(t, lp.EditEntry(ctx, profilestest.MediaAlpha, localplatform.ProfileEntryEdit{HiddenFromStatusLists: new(true)}))
	col, err := lp.GetAnimeCollection(ctx, false)
	require.NoError(t, err)
	_, found := col.GetListEntryFromAnimeId(profilestest.MediaAlpha)
	assert.False(t, found)

	// Deleting the entry drops its custom list membership.
	require.NoError(t, lp.DeleteEntry(ctx, profilestest.MediaAlpha, 0))
	lists, _ := lp.CustomLists(ctx)
	for _, l := range lists {
		assert.Empty(t, l.MediaIDs)
	}
}

func TestViewerStatsNumbers(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	lpB, ctxB := e.platform(e.profile(t, "Bob"))
	edit := func(id int, st string, score, progress, repeat int) {
		t.Helper()
		require.NoError(t, lp.EditEntry(ctx, id, localplatform.ProfileEntryEdit{Status: new(st), ScoreRaw: new(score), Progress: new(progress), Repeat: new(repeat)}))
	}
	edit(profilestest.MediaAlpha, "COMPLETED", 90, 12, 2) // 12 + 2*12 eps x 24 min
	edit(profilestest.MediaGamma, "COMPLETED", 70, 1, 0)  // movie, 110 min
	edit(profilestest.MediaEpsilon, "DROPPED", 0, 1, 0)   // 30 min, no score
	edit(profilestest.MediaDelta, "REPEATING", 50, 5, 1)  // 5 + 1*24 eps x 23 min
	edit(profilestest.MediaZeta, "PLANNING", 0, 0, 0)     // not counted
	require.NoError(t, lpB.EditEntry(ctxB, profilestest.MediaBeta, localplatform.ProfileEntryEdit{Status: new("CURRENT"), Progress: new(3), ScoreRaw: new(10)}))

	vs, err := lp.GetViewerStats(ctx)
	require.NoError(t, err)
	st := vs.Viewer.Statistics.Anime
	assert.Equal(t, 4, st.Count)
	assert.Equal(t, 36+1+1+29, st.EpisodesWatched)
	assert.Equal(t, 36*24+110+30+29*23, st.MinutesWatched)
	assert.InDelta(t, 70.0, st.MeanScore, 0.001, "unscored entries don't count in the mean")

	statuses := map[string][2]float64{}
	for _, s := range st.Statuses {
		statuses[string(*s.Status)] = [2]float64{float64(s.Count), s.MeanScore}
	}
	assert.Equal(t, map[string][2]float64{"COMPLETED": {2, 80}, "DROPPED": {1, 0}, "REPEATING": {1, 50}, "PLANNING": {1, 0}}, statuses)

	formats := map[anilist.MediaFormat]int{}
	for _, f := range st.Formats {
		formats[*f.Format] = f.Count
	}
	assert.Equal(t, map[anilist.MediaFormat]int{anilist.MediaFormatTv: 2, anilist.MediaFormatMovie: 1, anilist.MediaFormatOva: 1}, formats)
	assert.Equal(t, anilist.MediaFormatTv, *st.Formats[0].Format, "most common format first")

	genres := map[string]int{}
	for _, g := range st.Genres {
		genres[*g.Genre] = g.Count
	}
	assert.Equal(t, map[string]int{"Action": 2, "Comedy": 2, "Fantasy": 1, "Sci-Fi": 1}, genres)

	years := []int{}
	for _, y := range st.ReleaseYears {
		years = append(years, *y.ReleaseYear)
	}
	assert.Equal(t, []int{2015, 2019, 2020, 2021}, years)
	scores := []int{}
	for _, s := range st.Scores {
		scores = append(scores, *s.Score)
	}
	assert.Equal(t, []int{50, 70, 90}, scores)
	assert.NotNil(t, st.Studios)

	// Bob's stats only see Bob's list.
	vsB, err := lpB.GetViewerStats(ctxB)
	require.NoError(t, err)
	assert.Equal(t, 1, vsB.Viewer.Statistics.Anime.Count)
	assert.Equal(t, 3*24, vsB.Viewer.Statistics.Anime.MinutesWatched)

	// The web consumes it through anilist.GetStats.
	ret, err := anilist.GetStats(ctx, vs)
	require.NoError(t, err)
	assert.Equal(t, 4, ret.AnimeStats.Count)
}

func TestRecentlyWatched(t *testing.T) {
	e := newEnv(t)
	ana, bob := e.profile(t, "Ana"), e.profile(t, "Bob")
	lp, ctx := e.platform(ana)
	lpB, ctxB := e.platform(bob)
	require.NoError(t, lp.UpdateEntryProgress(ctx, profilestest.MediaAlpha, 2, nil))
	ins := func(pid int64, media, ep int, updated int64) {
		require.NoError(t, e.db.Exec(`INSERT INTO watch_history (profile_id, media_id, episode_number, kind, position_sec, duration, time_added, time_updated)
VALUES (?, ?, ?, 'onlinestream', 120, 1440, ?, ?)`, pid, media, ep, updated, updated).Error)
	}
	ins(ana.ID, profilestest.MediaAlpha, 3, 100)
	ins(ana.ID, profilestest.MediaDelta, 7, 200)
	ins(bob.ID, profilestest.MediaGamma, 1, 300)

	rec, err := lp.RecentlyWatched(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rec, 2)
	assert.Equal(t, profilestest.MediaDelta, rec[0].MediaID, "newest first")
	assert.Nil(t, rec[0].ListStatus, "not in the list")
	assert.Equal(t, 3, rec[1].EpisodeNumber)
	assert.Equal(t, "CURRENT", *rec[1].ListStatus)
	assert.Equal(t, 2, *rec[1].ListProgress)
	require.NotNil(t, rec[1].Media)
	assert.Equal(t, profilestest.MediaAlpha, rec[1].Media.ID)

	recB, err := lpB.RecentlyWatched(ctxB, 10)
	require.NoError(t, err)
	require.Len(t, recB, 1)
	assert.Equal(t, profilestest.MediaGamma, recB[0].MediaID)
}
