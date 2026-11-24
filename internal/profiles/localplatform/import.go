package localplatform

import (
	"context"
	"errors"
	"fmt"
	"seanime/internal/api/anilist"
	"seanime/internal/customsource"
	"strconv"

	"gorm.io/gorm"
)

// ImportMode is the conflict policy of an import (plan §3.5).
type ImportMode string

const (
	// ImportReplace deletes the profile's anime list and custom lists first.
	ImportReplace ImportMode = "replace"
	// ImportMerge keeps, for each media, the most recently updated entry.
	ImportMerge ImportMode = "merge"
)

// ProfileImportEntry is one list entry to import (AniList list, JSON export...).
type ProfileImportEntry struct {
	MediaID               int                `json:"mediaId"`
	Status                string             `json:"status"`
	Progress              int                `json:"progress"`
	ScoreRaw              *int               `json:"scoreRaw,omitempty"`
	Repeat                int                `json:"repeat"`
	Private               bool               `json:"private"`
	HiddenFromStatusLists bool               `json:"hiddenFromStatusLists,omitempty"`
	Notes                 *string            `json:"notes,omitempty"`
	StartedAt             ProfileFuzzyDate   `json:"startedAt"`
	CompletedAt           ProfileFuzzyDate   `json:"completedAt"`
	CreatedAt             int64              `json:"createdAt,omitempty"`
	UpdatedAt             int64              `json:"updatedAt,omitempty"`
	CustomLists           []string           `json:"customLists,omitempty"`
	Media                 *anilist.BaseAnime `json:"-"`
}

// ProfileImportResult summarizes an import.
type ProfileImportResult struct {
	Imported int      `json:"imported"`
	Updated  int      `json:"updated"`
	Skipped  int      `json:"skipped"`
	Errors   []string `json:"errors,omitempty"`
	// Favourites is how many favourites were added (phase 3).
	Favourites int `json:"favourites,omitempty"`
}

// Import writes entries into the profile's list in one transaction, with an
// activity_log 'imported' row per written entry. source is recorded in the log.
func (p *Platform) Import(ctx context.Context, entries []ProfileImportEntry, mode ImportMode, source string) (*ProfileImportResult, error) {
	if mode != ImportReplace && mode != ImportMerge {
		return nil, fmt.Errorf("invalid import mode %q", mode)
	}
	res := &ProfileImportResult{}

	// Snapshots for the media we got with the entries (AniList collections carry them).
	withMedia := make([]*anilist.BaseAnime, 0)
	for _, e := range entries {
		if e.Media != nil && e.Media.ID == e.MediaID {
			withMedia = append(withMedia, e.Media)
		}
	}
	if err := p.meta.SaveSnapshots(ctx, withMedia); err != nil {
		return nil, err
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	now := p.now().Unix()
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if mode == ImportReplace {
			if err := tx.Where("profile_id = ? AND media_type = ?", p.profileID, mediaTypeAnime).Delete(&CustomListRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("profile_id = ? AND media_type = ?", p.profileID, mediaTypeAnime).Delete(&EntryRow{}).Error; err != nil {
				return err
			}
		}
		seen := map[int]bool{}
		for _, in := range entries {
			if in.MediaID <= 0 || customsource.IsExtensionId(in.MediaID) || !validStatus(anilist.MediaListStatus(in.Status)) {
				res.Skipped++
				res.Errors = append(res.Errors, fmt.Sprintf("media %d: invalid entry", in.MediaID))
				continue
			}
			if seen[in.MediaID] {
				// AniList lists the same entry in its status list and in custom lists.
				if err := p.importCustomLists(ctx, tx, in); err != nil {
					return err
				}
				continue
			}
			seen[in.MediaID] = true

			updatedAt := in.UpdatedAt
			if updatedAt <= 0 {
				updatedAt = now
			}
			createdAt := in.CreatedAt
			if createdAt <= 0 {
				createdAt = updatedAt
			}
			row := EntryRow{ProfileID: p.profileID, MediaType: mediaTypeAnime, MediaID: in.MediaID, Status: in.Status,
				Progress: max(0, in.Progress), ScoreRaw: normalizeScoreRaw(in.ScoreRaw), Repeat: max(0, in.Repeat),
				Private: in.Private, HiddenFromStatusLists: in.HiddenFromStatusLists, Notes: in.Notes,
				CreatedAt: createdAt, UpdatedAt: updatedAt}
			row.setStartedAt(in.StartedAt)
			row.setCompletedAt(in.CompletedAt)

			existing, err := p.findEntry(ctx, tx, in.MediaID)
			switch {
			case errors.Is(err, ErrMediaNotFound):
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				res.Imported++
			case err != nil:
				return err
			default:
				if existing.UpdatedAt >= updatedAt {
					res.Skipped++
					if err := p.importCustomLists(ctx, tx, in); err != nil {
						return err
					}
					continue
				}
				row.ID = existing.ID
				row.CreatedAt = existing.CreatedAt
				if err := tx.Model(&EntryRow{}).Where("id = ? AND profile_id = ?", existing.ID, p.profileID).Select("*").Updates(&row).Error; err != nil {
					return err
				}
				res.Updated++
			}
			if err := tx.Create(&activity{ProfileID: p.profileID, MediaType: mediaTypeAnime, MediaID: in.MediaID, Kind: "imported",
				OldValue: strPtr(source), NewValue: strPtr(in.Status + " " + strconv.Itoa(row.Progress)), CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := p.importCustomLists(ctx, tx, in); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	p.Invalidate()
	return res, nil
}

func (p *Platform) importCustomLists(ctx context.Context, tx *gorm.DB, in ProfileImportEntry) error {
	for _, name := range in.CustomLists {
		name, err := normalizeListName(name)
		if err != nil {
			continue
		}
		id, err := p.createCustomListTx(tx, name)
		if err != nil {
			return err
		}
		if err := p.setInCustomListTx(ctx, tx, id, in.MediaID, true); err != nil && !errors.Is(err, ErrMediaNotFound) {
			return err
		}
	}
	return nil
}

// FromAnilistCollection maps an AniList AnimeCollection (public list, the server
// account's cached list...) to import entries, with custom list membership.
func FromAnilistCollection(c *anilist.AnimeCollection) []ProfileImportEntry {
	byMedia := map[int]*ProfileImportEntry{}
	order := make([]int, 0)
	for _, list := range c.GetMediaListCollection().GetLists() {
		custom := list.GetIsCustomList() != nil && *list.GetIsCustomList()
		for _, e := range list.GetEntries() {
			if e == nil || e.GetMedia() == nil {
				continue
			}
			id := e.GetMedia().GetID()
			cur, ok := byMedia[id]
			if !ok {
				cur = &ProfileImportEntry{MediaID: id, Media: e.GetMedia(), HiddenFromStatusLists: true}
				byMedia[id] = cur
				order = append(order, id)
			}
			if custom {
				if list.GetName() != nil {
					cur.CustomLists = append(cur.CustomLists, *list.GetName())
				}
			} else {
				cur.HiddenFromStatusLists = false
			}
			if cur.Status == "" || !custom {
				if e.GetStatus() != nil {
					cur.Status = string(*e.GetStatus())
				}
				if e.GetProgress() != nil {
					cur.Progress = *e.GetProgress()
				}
				if e.GetScore() != nil && *e.GetScore() > 0 {
					cur.ScoreRaw = new(int(*e.GetScore() + 0.5)) // POINT_100 in the query
				}
				if e.GetRepeat() != nil {
					cur.Repeat = *e.GetRepeat()
				}
				if e.GetPrivate() != nil {
					cur.Private = *e.GetPrivate()
				}
				cur.Notes = e.GetNotes()
				if d := e.GetStartedAt(); d != nil {
					cur.StartedAt = ProfileFuzzyDate{Year: d.Year, Month: d.Month, Day: d.Day}
				}
				if d := e.GetCompletedAt(); d != nil {
					cur.CompletedAt = ProfileFuzzyDate{Year: d.Year, Month: d.Month, Day: d.Day}
				}
				if e.GetCreatedAt() != nil {
					cur.CreatedAt = int64(*e.GetCreatedAt())
				}
				if e.GetUpdatedAt() != nil {
					cur.UpdatedAt = int64(*e.GetUpdatedAt())
				}
			}
		}
	}
	ret := make([]ProfileImportEntry, 0, len(order))
	for _, id := range order {
		e := byMedia[id]
		if e.Status == "" {
			continue
		}
		ret = append(ret, *e)
	}
	return ret
}
