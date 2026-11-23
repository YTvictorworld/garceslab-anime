package localplatform_test

import (
	"seanime/internal/api/anilist"
	"seanime/internal/profiles/localplatform"
	"seanime/internal/profiles/profilestest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rules of the auto-watching plugin (README + plugin.js), now native.
func TestMarkWatchingOnStart(t *testing.T) {
	type seed struct {
		status   anilist.MediaListStatus
		progress int
		score    int
		started  bool
	}
	cases := []struct {
		name            string
		seed            *seed
		episode         int
		completedAction anilist.MediaListStatus
		wantAction      localplatform.WatchingAction
		wantStatus      anilist.MediaListStatus
		wantProgress    int
		wantStartedDay  *int // nil: unchanged / not checked
	}{
		{name: "not in list", episode: 3, wantAction: localplatform.WatchingAdded, wantStatus: anilist.MediaListStatusCurrent, wantProgress: 2, wantStartedDay: new(23)},
		{name: "not in list, first episode", episode: 1, wantAction: localplatform.WatchingAdded, wantStatus: anilist.MediaListStatusCurrent, wantProgress: 0},
		{name: "not in list, special", episode: 0, wantAction: localplatform.WatchingAdded, wantStatus: anilist.MediaListStatusCurrent, wantProgress: 0},
		{name: "planning", seed: &seed{status: anilist.MediaListStatusPlanning}, episode: 5, wantAction: localplatform.WatchingResumed, wantStatus: anilist.MediaListStatusCurrent, wantProgress: 4, wantStartedDay: new(23)},
		{name: "paused, never lowers progress", seed: &seed{status: anilist.MediaListStatusPaused, progress: 8, started: true}, episode: 3, wantAction: localplatform.WatchingResumed, wantStatus: anilist.MediaListStatusCurrent, wantProgress: 8, wantStartedDay: new(1)},
		{name: "dropped, raises progress", seed: &seed{status: anilist.MediaListStatusDropped, progress: 2}, episode: 6, wantAction: localplatform.WatchingResumed, wantStatus: anilist.MediaListStatusCurrent, wantProgress: 5},
		{name: "dropped, special keeps progress", seed: &seed{status: anilist.MediaListStatusDropped, progress: 2}, episode: 0, wantAction: localplatform.WatchingResumed, wantStatus: anilist.MediaListStatusCurrent, wantProgress: 2},
		{name: "completed -> repeating", seed: &seed{status: anilist.MediaListStatusCompleted, progress: 12, score: 90, started: true}, episode: 1, wantAction: localplatform.WatchingRewatched, wantStatus: anilist.MediaListStatusRepeating, wantProgress: 0, wantStartedDay: new(1)},
		{name: "completed -> current (setting)", seed: &seed{status: anilist.MediaListStatusCompleted, progress: 12}, episode: 4, completedAction: anilist.MediaListStatusCurrent, wantAction: localplatform.WatchingRewatched, wantStatus: anilist.MediaListStatusCurrent, wantProgress: 3},
		{name: "completed, special does nothing", seed: &seed{status: anilist.MediaListStatusCompleted, progress: 12}, episode: 0, wantAction: localplatform.WatchingNone, wantStatus: anilist.MediaListStatusCompleted, wantProgress: 12},
		{name: "current does nothing", seed: &seed{status: anilist.MediaListStatusCurrent, progress: 7}, episode: 2, wantAction: localplatform.WatchingNone, wantStatus: anilist.MediaListStatusCurrent, wantProgress: 7},
		{name: "repeating does nothing", seed: &seed{status: anilist.MediaListStatusRepeating, progress: 1}, episode: 9, wantAction: localplatform.WatchingNone, wantStatus: anilist.MediaListStatusRepeating, wantProgress: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			lp, ctx := e.platform(e.profile(t, "Ana"))
			media := profilestest.MediaAlpha
			if c.seed != nil {
				var started *anilist.FuzzyDateInput
				if c.seed.started {
					started = &anilist.FuzzyDateInput{Year: new(2026), Month: new(1), Day: new(1)}
				}
				require.NoError(t, lp.UpdateEntry(ctx, media, status(c.seed.status), new(c.seed.score), new(c.seed.progress), started, nil))
			}
			action, err := lp.MarkWatchingOnStart(ctx, media, c.episode, localplatform.MarkWatchingOptions{CompletedAction: c.completedAction})
			require.NoError(t, err)
			assert.Equal(t, c.wantAction, action)
			en := entryOf(t, lp, ctx, media)
			assert.Equal(t, c.wantStatus, *en.Status)
			assert.Equal(t, c.wantProgress, *en.Progress)
			if c.wantStartedDay != nil {
				require.NotNil(t, en.StartedAt.Day)
				assert.Equal(t, *c.wantStartedDay, *en.StartedAt.Day)
			}
			if c.seed != nil && c.seed.score > 0 {
				assert.Equal(t, float64(c.seed.score), *en.Score, "the score is kept")
			}
		})
	}
}

// After MarkWatchingOnStart, finishing the last episode completes the anime; a
// rewatch completes with repeat+1 (plan: completion rules).
func TestMarkWatchingThenComplete(t *testing.T) {
	e := newEnv(t)
	lp, ctx := e.platform(e.profile(t, "Ana"))
	require.NoError(t, lp.UpdateEntry(ctx, profilestest.MediaEpsilon, status(anilist.MediaListStatusCompleted), nil, new(2), nil, nil))
	action, err := lp.MarkWatchingOnStart(ctx, profilestest.MediaEpsilon, 1, localplatform.MarkWatchingOptions{})
	require.NoError(t, err)
	require.Equal(t, localplatform.WatchingRewatched, action)
	require.NoError(t, lp.UpdateEntryProgress(ctx, profilestest.MediaEpsilon, 1, new(2)))
	assert.Equal(t, anilist.MediaListStatusRepeating, *entryOf(t, lp, ctx, profilestest.MediaEpsilon).Status)
	require.NoError(t, lp.UpdateEntryProgress(ctx, profilestest.MediaEpsilon, 2, new(2)))
	en := entryOf(t, lp, ctx, profilestest.MediaEpsilon)
	assert.Equal(t, anilist.MediaListStatusCompleted, *en.Status)
	assert.Equal(t, 1, *en.Repeat)
}
