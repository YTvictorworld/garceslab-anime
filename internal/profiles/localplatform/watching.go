package localplatform

import (
	"context"
	"errors"
	"seanime/internal/api/anilist"
)

// WatchingAction is what MarkWatchingOnStart did.
type WatchingAction string

const (
	WatchingNone      WatchingAction = "none"
	WatchingAdded     WatchingAction = "added"     // not in the list -> CURRENT
	WatchingResumed   WatchingAction = "resumed"   // PLANNING/PAUSED/DROPPED -> CURRENT
	WatchingRewatched WatchingAction = "rewatched" // COMPLETED -> REPEATING (or CURRENT)
)

// MarkWatchingOptions configures MarkWatchingOnStart (profile settings).
type MarkWatchingOptions struct {
	// CompletedAction is the status for an anime that was COMPLETED: REPEATING
	// (default) or CURRENT.
	CompletedAction anilist.MediaListStatus
}

// MarkWatchingOnStart is the native version of the auto-watching plugin (plan §3.8),
// run when a playback of `episode` (AniList progress number; <= 0 for specials and
// unknown episodes) starts:
//
//	not in the list            -> CURRENT, progress N-1, started today
//	PLANNING / PAUSED / DROPPED -> CURRENT, progress max(progress, N-1) (never lowered)
//	COMPLETED                  -> REPEATING (or CURRENT), progress N-1, dates kept
//	CURRENT / REPEATING        -> nothing
//
// Specials only add the anime to the list (progress untouched) and do nothing on a
// COMPLETED anime. Score and dates are kept.
func (p *Platform) MarkWatchingOnStart(ctx context.Context, mediaID int, episode int, opts MarkWatchingOptions) (WatchingAction, error) {
	if mediaID <= 0 {
		return WatchingNone, nil
	}
	target := 0
	if episode > 0 {
		target = episode - 1
	}

	entry, err := p.findEntry(ctx, p.db, mediaID)
	if err != nil && !errors.Is(err, ErrMediaNotFound) {
		return WatchingNone, err
	}

	current := anilist.MediaListStatusCurrent
	if entry == nil {
		today := Today(p.now())
		patch := entryPatch{Status: &current, Progress: new(target), StartedAt: &today}
		if err := p.upsertWithHooks(ctx, mediaID, patch); err != nil {
			return WatchingNone, err
		}
		return WatchingAdded, nil
	}

	switch anilist.MediaListStatus(entry.Status) {
	case anilist.MediaListStatusCurrent, anilist.MediaListStatusRepeating:
		return WatchingNone, nil
	case anilist.MediaListStatusCompleted:
		if episode <= 0 {
			return WatchingNone, nil
		}
		status := anilist.MediaListStatusRepeating
		if opts.CompletedAction == anilist.MediaListStatusCurrent {
			status = anilist.MediaListStatusCurrent
		}
		if err := p.upsertWithHooks(ctx, mediaID, entryPatch{Status: &status, Progress: new(target)}); err != nil {
			return WatchingNone, err
		}
		return WatchingRewatched, nil
	default: // PLANNING, PAUSED, DROPPED
		patch := entryPatch{Status: &current, Progress: new(max(entry.Progress, target))}
		if entry.StartedAt().IsZero() {
			today := Today(p.now())
			patch.StartedAt = &today
		}
		if err := p.upsertWithHooks(ctx, mediaID, patch); err != nil {
			return WatchingNone, err
		}
		return WatchingResumed, nil
	}
}

// upsertWithHooks writes through UpdateEntry so the entry hooks run, like the
// plugin's $anilist.updateEntry did.
func (p *Platform) upsertWithHooks(ctx context.Context, mediaID int, patch entryPatch) error {
	var started *anilist.FuzzyDateInput
	if patch.StartedAt != nil {
		started = &anilist.FuzzyDateInput{Year: patch.StartedAt.Year, Month: patch.StartedAt.Month, Day: patch.StartedAt.Day}
	}
	return p.UpdateEntry(ctx, mediaID, patch.Status, nil, patch.Progress, started, nil)
}
