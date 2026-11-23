package metadata_test

import (
	"context"
	"seanime/internal/api/anilist"
	"seanime/internal/profiles/metadata"
	"seanime/internal/profiles/profilestest"
	"seanime/internal/profiles/store"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type authenticatedFake struct{ *profilestest.FakeAnilist }

func (authenticatedFake) IsAuthenticated() bool { return true }

func newService(t *testing.T, now *time.Time) (*metadata.Service, *profilestest.FakeAnilist) {
	t.Helper()
	st, err := store.Open("")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	fake := profilestest.NewFakeAnilist(t)
	svc, err := metadata.New(metadata.Options{Client: fake, DB: st.DB(), Now: func() time.Time { return *now }, RatePerMinute: 2})
	require.NoError(t, err)
	return svc, fake
}

func TestRefusesAuthenticatedClient(t *testing.T) {
	st, err := store.Open("")
	require.NoError(t, err)
	defer st.Close()
	_, err = metadata.New(metadata.Options{Client: authenticatedFake{profilestest.NewFakeAnilist(t)}, DB: st.DB()})
	assert.ErrorIs(t, err, metadata.ErrAuthenticatedClient)
}

func TestBatchFetchAndSnapshots(t *testing.T) {
	now := time.Now()
	svc, fake := newService(t, &now)
	base := profilestest.FixtureMedia()[0]
	ids := make([]int, 0, 120)
	for i := 0; i < 120; i++ {
		m := *base
		m.ID = 5000 + i
		fake.SetMedia(&m)
		ids = append(ids, m.ID)
	}
	got, err := svc.GetBaseAnimeMany(context.Background(), append(ids, ids[0], 999999)) // duplicates and unknown ids
	require.NoError(t, err)
	assert.Len(t, got, 120)
	assert.Equal(t, 3, fake.Calls("SearchBaseAnimeByIds"), "batches of at most 50")

	// From the snapshots now, even with AniList down.
	fake.Fail = true
	got, err = svc.GetBaseAnimeMany(context.Background(), ids)
	require.NoError(t, err)
	assert.Len(t, got, 120)
	assert.Equal(t, 3, fake.Calls("SearchBaseAnimeByIds"))
}

func TestRefreshOnlyStaleSnapshots(t *testing.T) {
	now := time.Now()
	svc, fake := newService(t, &now)
	ctx := context.Background()
	ids := []int{profilestest.MediaAlpha, profilestest.MediaBeta}
	_, err := svc.GetBaseAnimeMany(ctx, ids)
	require.NoError(t, err)

	stale, err := svc.StaleIDs(ctx, ids)
	require.NoError(t, err)
	assert.Empty(t, stale)

	// After 7 hours only the airing anime is stale; after 15 days everything is.
	now = now.Add(7 * time.Hour)
	stale, err = svc.StaleIDs(ctx, ids)
	require.NoError(t, err)
	assert.Equal(t, []int{profilestest.MediaBeta}, stale)

	updated := profilestest.FixtureMedia()[1]
	updated.Title.English = new("Fixture Beta (updated)")
	fake.SetMedia(updated)
	calls := fake.Calls("SearchBaseAnimeByIds")
	refreshed, err := svc.RefreshStale(ctx, ids)
	require.NoError(t, err)
	assert.Equal(t, []int{profilestest.MediaBeta}, refreshed)
	assert.Equal(t, calls+1, fake.Calls("SearchBaseAnimeByIds"))
	got, err := svc.GetBaseAnimeMany(ctx, []int{profilestest.MediaBeta})
	require.NoError(t, err)
	assert.Equal(t, "Fixture Beta (updated)", *got[profilestest.MediaBeta].Title.English)

	now = now.Add(15 * 24 * time.Hour)
	stale, err = svc.StaleIDs(ctx, ids)
	require.NoError(t, err)
	assert.ElementsMatch(t, ids, stale)
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Now()
	releasing := string(anilist.MediaStatusReleasing)
	finished := string(anilist.MediaStatusFinished)
	assert.False(t, metadata.NeedsRefresh(&releasing, now.Add(-time.Hour).Unix(), now))
	assert.True(t, metadata.NeedsRefresh(&releasing, now.Add(-7*time.Hour).Unix(), now))
	assert.False(t, metadata.NeedsRefresh(&finished, now.Add(-7*24*time.Hour).Unix(), now))
	assert.True(t, metadata.NeedsRefresh(nil, now.Add(-15*24*time.Hour).Unix(), now))
}

func TestTokenBucket(t *testing.T) {
	now := time.Now()
	b := metadata.NewTokenBucket(2, time.Minute, func() time.Time { return now })
	assert.True(t, b.TryTake())
	assert.True(t, b.TryTake())
	assert.False(t, b.TryTake())
	now = now.Add(30 * time.Second) // 1 token per 30 s
	assert.True(t, b.TryTake())
	assert.False(t, b.TryTake())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, b.Wait(ctx), "Wait gives up when the context is done")
}

func TestGetAnimeFallsBackToSnapshot(t *testing.T) {
	now := time.Now()
	svc, fake := newService(t, &now)
	ctx := context.Background()
	_, err := svc.EnsureAnime(ctx, profilestest.MediaGamma)
	require.NoError(t, err)
	fake.Fail = true
	m, err := svc.GetAnime(ctx, profilestest.MediaGamma)
	require.NoError(t, err)
	assert.Equal(t, profilestest.MediaGamma, m.ID)
	_, err = svc.GetAnime(ctx, profilestest.MediaZeta)
	assert.Error(t, err)
}
