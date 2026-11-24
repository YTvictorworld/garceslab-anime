package localplatform

// Phase 3: the full entry editor (everything AniList's list editor has: status,
// score, progress, dates, rewatches, private, notes, custom lists) and "recently
// watched". The upstream list endpoints only carry status/score/progress/dates.

import (
	"context"
	"errors"
	"fmt"
	"seanime/internal/api/anilist"
	"seanime/internal/platforms/platform"
	"unicode/utf8"

	"gorm.io/gorm"
)

// MaxNotesLength caps the notes of an entry (in characters).
const MaxNotesLength = 5000

// ErrInvalidEntry is a malformed entry edit.
var ErrInvalidEntry = errors.New("invalid list entry")

// ProfileEntryEdit is a partial edit of a list entry. Nil fields are unchanged.
// A date with every part null clears it; empty notes clear them.
type ProfileEntryEdit struct {
	Status                *string           `json:"status,omitempty"`
	ScoreRaw              *int              `json:"scoreRaw,omitempty"` // 0-100, 0 clears it
	Progress              *int              `json:"progress,omitempty"`
	Repeat                *int              `json:"repeat,omitempty"`
	Private               *bool             `json:"private,omitempty"`
	HiddenFromStatusLists *bool             `json:"hiddenFromStatusLists,omitempty"`
	Notes                 *string           `json:"notes,omitempty"`
	StartedAt             *ProfileFuzzyDate `json:"startedAt,omitempty"`
	CompletedAt           *ProfileFuzzyDate `json:"completedAt,omitempty"`
	// CustomListIDs replaces the entry's custom list membership when set.
	CustomListIDs *[]int64 `json:"customListIds,omitempty"`
}

// ProfileEntryDetails is one entry with every editable field.
type ProfileEntryDetails struct {
	ID                    int64            `json:"id"`
	MediaID               int              `json:"mediaId"`
	Status                string           `json:"status"`
	Progress              int              `json:"progress"`
	ScoreRaw              *int             `json:"scoreRaw,omitempty"`
	Repeat                int              `json:"repeat"`
	Private               bool             `json:"private"`
	HiddenFromStatusLists bool             `json:"hiddenFromStatusLists"`
	Notes                 *string          `json:"notes,omitempty"`
	StartedAt             ProfileFuzzyDate `json:"startedAt"`
	CompletedAt           ProfileFuzzyDate `json:"completedAt"`
	CustomListIDs         []int64          `json:"customListIds"`
	IsFavourite           bool             `json:"isFavourite"`
	CreatedAt             int64            `json:"createdAt"`
	UpdatedAt             int64            `json:"updatedAt"`
}

// EntryDetails returns the profile's entry for a media (ErrMediaNotFound if it
// isn't in the list).
func (p *Platform) EntryDetails(ctx context.Context, mediaID int) (*ProfileEntryDetails, error) {
	row, err := p.findEntry(ctx, p.db, mediaID)
	if err != nil {
		return nil, err
	}
	lists, err := p.EntryCustomListIDs(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	fav, err := p.IsFavourite(ctx, FavouriteAnime, mediaID)
	if err != nil {
		return nil, err
	}
	return &ProfileEntryDetails{
		ID: row.ID, MediaID: row.MediaID, Status: row.Status, Progress: row.Progress, ScoreRaw: row.ScoreRaw,
		Repeat: row.Repeat, Private: row.Private, HiddenFromStatusLists: row.HiddenFromStatusLists, Notes: row.Notes,
		StartedAt: row.StartedAt(), CompletedAt: row.CompletedAt(), CustomListIDs: lists, IsFavourite: fav,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

func toInput(d *ProfileFuzzyDate) *anilist.FuzzyDateInput {
	if d == nil {
		return nil
	}
	return &anilist.FuzzyDateInput{Year: d.Year, Month: d.Month, Day: d.Day}
}

// EditEntry applies an edit to the media's entry, creating it (PLANNING unless a
// status is given) when the media isn't in the list. The status, score, progress
// and dates go through the same update-entry hooks as UpdateEntry.
func (p *Platform) EditEntry(ctx context.Context, mediaID int, edit ProfileEntryEdit) error {
	var status *anilist.MediaListStatus
	if edit.Status != nil {
		st := anilist.MediaListStatus(*edit.Status)
		if !validStatus(st) {
			return fmt.Errorf("%w: status %q", ErrInvalidEntry, *edit.Status)
		}
		status = &st
	}
	if edit.ScoreRaw != nil && (*edit.ScoreRaw < 0 || *edit.ScoreRaw > 100) {
		return fmt.Errorf("%w: score must be 0-100", ErrInvalidEntry)
	}
	if edit.Progress != nil && *edit.Progress < 0 {
		return fmt.Errorf("%w: progress must be >= 0", ErrInvalidEntry)
	}
	if edit.Repeat != nil && *edit.Repeat < 0 {
		return fmt.Errorf("%w: rewatches must be >= 0", ErrInvalidEntry)
	}
	if edit.Notes != nil && utf8.RuneCountInString(*edit.Notes) > MaxNotesLength {
		return fmt.Errorf("%w: notes are too long", ErrInvalidEntry)
	}

	err := p.withHelperUpdateEntry(ctx, mediaID, status, edit.ScoreRaw, edit.Progress, toInput(edit.StartedAt), toInput(edit.CompletedAt),
		func(event *platform.PreUpdateEntryEvent) error {
			patch := entryPatch{
				Status:   event.Status,
				ScoreRaw: event.ScoreRaw,
				Progress: event.Progress,
				Repeat:   edit.Repeat,
				Private:  edit.Private,
				Notes:    edit.Notes,
				Hidden:   edit.HiddenFromStatusLists,
			}
			if event.StartedAt != nil {
				d := fromInput(event.StartedAt)
				patch.StartedAt = &d
			}
			if event.CompletedAt != nil {
				d := fromInput(event.CompletedAt)
				patch.CompletedAt = &d
			}
			return p.upsert(ctx, mediaID, patch)
		})
	if err != nil || edit.CustomListIDs == nil {
		return err
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	err = p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return p.setEntryCustomListsTx(ctx, tx, mediaID, *edit.CustomListIDs)
	})
	if err != nil {
		return err
	}
	p.Invalidate()
	return nil
}

// ProfileRecentlyWatched is a media the profile watched recently (its latest
// watch history position) with its list entry, if any.
type ProfileRecentlyWatched struct {
	MediaID       int                `json:"mediaId"`
	EpisodeNumber int                `json:"episodeNumber"`
	PositionSec   float64            `json:"positionSec"`
	Duration      float64            `json:"duration"`
	UpdatedAt     int64              `json:"updatedAt"`
	ListStatus    *string            `json:"listStatus,omitempty"`
	ListProgress  *int               `json:"listProgress,omitempty"`
	Media         *anilist.BaseAnime `gorm:"-" json:"media,omitempty"`
}

// RecentlyWatched returns the profile's most recently watched media, newest first.
func (p *Platform) RecentlyWatched(ctx context.Context, limit int) ([]*ProfileRecentlyWatched, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	ret := make([]*ProfileRecentlyWatched, 0)
	if err := p.db.WithContext(ctx).Raw(`SELECT h.media_id, h.episode_number, h.position_sec, h.duration, h.time_updated AS updated_at,
  e.status AS list_status, e.progress AS list_progress
FROM watch_history h
LEFT JOIN list_entries e ON e.profile_id = h.profile_id AND e.media_id = h.media_id AND e.media_type = ?
WHERE h.profile_id = ? ORDER BY h.time_updated DESC LIMIT ?`, mediaTypeAnime, p.profileID, limit).Scan(&ret).Error; err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(ret))
	for _, r := range ret {
		ids = append(ids, r.MediaID)
	}
	media, err := p.meta.GetBaseAnimeMany(ctx, ids)
	if err != nil {
		p.logger.Debug().Err(err).Msg("profiles: Could not load the recently watched metadata")
	}
	for _, r := range ret {
		r.Media = media[r.MediaID]
	}
	return ret, nil
}
