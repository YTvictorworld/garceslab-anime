package localplatform

import (
	"context"
	"seanime/internal/api/anilist"
)

// statusOrder and listNames follow AniList's default lists.
var statusOrder = []anilist.MediaListStatus{
	anilist.MediaListStatusCurrent,
	anilist.MediaListStatusRepeating,
	anilist.MediaListStatusCompleted,
	anilist.MediaListStatusPaused,
	anilist.MediaListStatusDropped,
	anilist.MediaListStatusPlanning,
}

var listNames = map[anilist.MediaListStatus]string{
	anilist.MediaListStatusCurrent:   "Watching",
	anilist.MediaListStatusRepeating: "Rewatching",
	anilist.MediaListStatusCompleted: "Completed",
	anilist.MediaListStatusPaused:    "Paused",
	anilist.MediaListStatusDropped:   "Dropped",
	anilist.MediaListStatusPlanning:  "Planning",
}

// CustomListRow is a row of custom_lists.
type CustomListRow struct {
	ID        int64  `gorm:"column:id;primaryKey"`
	ProfileID int64  `gorm:"column:profile_id"`
	MediaType string `gorm:"column:media_type"`
	Name      string `gorm:"column:name"`
	Position  int    `gorm:"column:position"`
	CreatedAt int64  `gorm:"column:created_at"`
}

func (CustomListRow) TableName() string { return "custom_lists" }

type customListItem struct {
	ListID  int64 `gorm:"column:list_id"`
	EntryID int64 `gorm:"column:entry_id"`
}

// toEntry converts a row to AniList's collection entry. The score is POINT_100
// (= score_raw), like `score(format: POINT_100)` in the AniList query.
func toEntry(row *EntryRow, media *anilist.BaseAnime) *anilist.AnimeCollection_MediaListCollection_Lists_Entries {
	status := anilist.MediaListStatus(row.Status)
	score := 0.0
	if row.ScoreRaw != nil {
		score = float64(*row.ScoreRaw)
	}
	var notes *string
	if row.Notes != nil {
		notes = new(*row.Notes)
	}
	return &anilist.AnimeCollection_MediaListCollection_Lists_Entries{
		ID:        int(row.ID),
		CreatedAt: new(int(row.CreatedAt)),
		UpdatedAt: new(int(row.UpdatedAt)),
		Score:     &score,
		Progress:  new(row.Progress),
		Status:    &status,
		Notes:     notes,
		Repeat:    new(row.Repeat),
		Private:   new(row.Private),
		StartedAt: &anilist.AnimeCollection_MediaListCollection_Lists_Entries_StartedAt{
			Year: row.StartedY, Month: row.StartedM, Day: row.StartedD,
		},
		CompletedAt: &anilist.AnimeCollection_MediaListCollection_Lists_Entries_CompletedAt{
			Year: row.CompletedY, Month: row.CompletedM, Day: row.CompletedD,
		},
		Media: media,
	}
}

// build assembles the profile's collection: status lists from list_entries and
// custom lists (IsCustomList) from custom_lists, with media from the snapshots.
func (p *Platform) build(ctx context.Context) (raw, filtered *anilist.AnimeCollection, err error) {
	rows, err := p.Entries(ctx)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.MediaID)
	}
	media, err := p.meta.GetBaseAnimeMany(ctx, ids)
	if err != nil {
		p.logger.Warn().Err(err).Int64("profileId", p.profileID).Msg("profiles: Could not load the list metadata")
		media = map[int]*anilist.BaseAnime{}
	}

	byStatus := map[anilist.MediaListStatus][]*anilist.AnimeCollection_MediaListCollection_Lists_Entries{}
	byID := map[int64]*EntryRow{}
	missing := 0
	for i := range rows {
		row := &rows[i]
		byID[row.ID] = row
		m, ok := media[row.MediaID]
		if !ok {
			missing++
			continue
		}
		if row.HiddenFromStatusLists {
			continue
		}
		st := anilist.MediaListStatus(row.Status)
		byStatus[st] = append(byStatus[st], toEntry(row, m))
	}
	if missing > 0 {
		p.logger.Warn().Int("count", missing).Int64("profileId", p.profileID).Msg("profiles: Some list entries have no metadata yet (AniList unreachable?)")
	}

	statusLists := make([]*anilist.AnimeCollection_MediaListCollection_Lists, 0, len(statusOrder))
	for _, st := range statusOrder {
		entries := byStatus[st]
		if len(entries) == 0 {
			continue
		}
		statusLists = append(statusLists, &anilist.AnimeCollection_MediaListCollection_Lists{
			Status:       new(st),
			Name:         new(listNames[st]),
			IsCustomList: new(false),
			Entries:      entries,
		})
	}

	customLists, err := p.customLists(ctx, byID, media)
	if err != nil {
		return nil, nil, err
	}

	raw = &anilist.AnimeCollection{MediaListCollection: &anilist.AnimeCollection_MediaListCollection{
		Lists: append(append([]*anilist.AnimeCollection_MediaListCollection_Lists{}, statusLists...), customLists...),
	}}
	filtered = &anilist.AnimeCollection{MediaListCollection: &anilist.AnimeCollection_MediaListCollection{
		Lists: statusLists,
	}}
	return raw, filtered, nil
}

func (p *Platform) customLists(ctx context.Context, byID map[int64]*EntryRow, media map[int]*anilist.BaseAnime) ([]*anilist.AnimeCollection_MediaListCollection_Lists, error) {
	var lists []CustomListRow
	if err := p.db.WithContext(ctx).Where("profile_id = ? AND media_type = ?", p.profileID, mediaTypeAnime).
		Order("position, id").Find(&lists).Error; err != nil {
		return nil, err
	}
	if len(lists) == 0 {
		return nil, nil
	}
	var items []customListItem
	// Scoped through custom_lists.profile_id (custom_list_items has no profile_id).
	if err := p.db.WithContext(ctx).Raw(`SELECT i.list_id, i.entry_id FROM custom_list_items i
JOIN custom_lists l ON l.id = i.list_id
JOIN list_entries e ON e.id = i.entry_id
WHERE l.profile_id = ? AND e.profile_id = ? ORDER BY i.added_at, i.entry_id`, p.profileID, p.profileID).Scan(&items).Error; err != nil {
		return nil, err
	}
	byList := map[int64][]int64{}
	for _, it := range items {
		byList[it.ListID] = append(byList[it.ListID], it.EntryID)
	}
	ret := make([]*anilist.AnimeCollection_MediaListCollection_Lists, 0, len(lists))
	for _, l := range lists {
		entries := make([]*anilist.AnimeCollection_MediaListCollection_Lists_Entries, 0)
		for _, eid := range byList[l.ID] {
			row, ok := byID[eid]
			if !ok {
				continue
			}
			m, ok := media[row.MediaID]
			if !ok {
				continue
			}
			entries = append(entries, toEntry(row, m))
		}
		ret = append(ret, &anilist.AnimeCollection_MediaListCollection_Lists{
			Name:         new(l.Name),
			IsCustomList: new(true),
			Entries:      entries,
		})
	}
	return ret, nil
}

func collectionMediaIDs(c *anilist.AnimeCollection) []int {
	ids := make([]int, 0)
	seen := map[int]struct{}{}
	for _, l := range c.GetMediaListCollection().GetLists() {
		for _, e := range l.GetEntries() {
			id := e.GetMedia().GetID()
			if _, ok := seen[id]; ok || id == 0 {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids
}
