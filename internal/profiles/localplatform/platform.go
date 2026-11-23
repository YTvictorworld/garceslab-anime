// Package localplatform is the local, per-profile implementation of
// platform.Platform (plan §3.4.1): the profile's lists live in profiles.db
// (list_entries, custom_lists, activity_log) and the anime metadata comes from the
// shared anonymous metadata service. It is modelled on the simulated platform.
package localplatform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"seanime/internal/api/anilist"
	"seanime/internal/customsource"
	"seanime/internal/hook"
	"seanime/internal/platforms/platform"
	"seanime/internal/profiles/metadata"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

var (
	// ErrMediaNotFound means the media is not in the profile's list.
	ErrMediaNotFound = errors.New("media not found")
	// ErrCustomSourceUnsupported: extension (custom source) media can't be saved in
	// a profile list yet (their lists are global upstream).
	ErrCustomSourceUnsupported = errors.New("custom source entries are not supported in profile lists yet")
)

const mediaTypeAnime = "ANIME"

// Platform is the platform of one profile.
type Platform struct {
	profileID int64
	db        *gorm.DB
	meta      *metadata.Service
	logger    *zerolog.Logger
	now       func() time.Time

	writeMu sync.Mutex // serializes the writes of this profile

	colMu    sync.RWMutex
	gen      uint64
	raw      *anilist.AnimeCollection // with custom lists
	filtered *anilist.AnimeCollection // status lists only
}

var _ platform.Platform = (*Platform)(nil)

type Options struct {
	ProfileID int64
	DB        *gorm.DB
	Meta      *metadata.Service
	Logger    *zerolog.Logger
	Now       func() time.Time
}

func New(opts Options) *Platform {
	logger := opts.Logger
	if logger == nil {
		l := zerolog.Nop()
		logger = &l
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Platform{profileID: opts.ProfileID, db: opts.DB, meta: opts.Meta, logger: logger, now: now}
}

func (p *Platform) ProfileID() int64 { return p.profileID }

//----------------------------------------------------------------------------------------------------------------------
// Cache

// Invalidate drops the in-memory collections (they are rebuilt on the next read).
func (p *Platform) Invalidate() {
	p.colMu.Lock()
	p.gen++
	p.raw, p.filtered = nil, nil
	p.colMu.Unlock()
}

func (p *Platform) cached() (raw, filtered *anilist.AnimeCollection, gen uint64) {
	p.colMu.RLock()
	defer p.colMu.RUnlock()
	return p.raw, p.filtered, p.gen
}

func (p *Platform) collections(ctx context.Context) (raw, filtered *anilist.AnimeCollection, fresh bool, err error) {
	if raw, filtered, _ := p.cached(); raw != nil {
		return raw, filtered, false, nil
	}
	_, _, gen := p.cached()
	raw, filtered, err = p.build(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	p.colMu.Lock()
	if p.gen == gen {
		p.raw, p.filtered = raw, filtered
	}
	p.colMu.Unlock()
	return raw, filtered, true, nil
}

//----------------------------------------------------------------------------------------------------------------------
// platform.Platform: collections

func (p *Platform) SetUsername(string) {}
func (p *Platform) ClearCache()        { p.Invalidate() }
func (p *Platform) Close()             {}

func (p *Platform) GetAnilistClient() anilist.AnilistClient { return p.meta.Client() }

func (p *Platform) GetAnimeCollection(ctx context.Context, bypassCache bool) (*anilist.AnimeCollection, error) {
	if bypassCache {
		p.Invalidate()
	}
	_, filtered, fresh, err := p.collections(ctx)
	if err != nil {
		return nil, err
	}
	if !fresh {
		event := &platform.GetCachedAnimeCollectionEvent{AnimeCollection: filtered}
		if err := hook.GlobalHookManager.OnGetCachedAnimeCollection().Trigger(event); err != nil {
			return nil, err
		}
		return event.AnimeCollection, nil
	}
	event := &platform.GetAnimeCollectionEvent{AnimeCollection: filtered}
	if err := hook.GlobalHookManager.OnGetAnimeCollection().Trigger(event); err != nil {
		return nil, err
	}
	return event.AnimeCollection, nil
}

func (p *Platform) GetRawAnimeCollection(ctx context.Context, bypassCache bool) (*anilist.AnimeCollection, error) {
	if bypassCache {
		p.Invalidate()
	}
	raw, _, fresh, err := p.collections(ctx)
	if err != nil {
		return nil, err
	}
	if !fresh {
		event := &platform.GetCachedRawAnimeCollectionEvent{AnimeCollection: raw}
		if err := hook.GlobalHookManager.OnGetCachedRawAnimeCollection().Trigger(event); err != nil {
			return nil, err
		}
		return event.AnimeCollection, nil
	}
	event := &platform.GetRawAnimeCollectionEvent{AnimeCollection: raw}
	if err := hook.GlobalHookManager.OnGetRawAnimeCollection().Trigger(event); err != nil {
		return nil, err
	}
	return event.AnimeCollection, nil
}

// RefreshAnimeCollection rebuilds the collection from profiles.db and queues a
// rate-limited background refresh of the stale (airing or old) snapshots.
func (p *Platform) RefreshAnimeCollection(ctx context.Context) (*anilist.AnimeCollection, error) {
	p.Invalidate()
	raw, filtered, _, err := p.collections(ctx)
	if err != nil {
		return nil, err
	}
	p.meta.RefreshStaleAsync(collectionMediaIDs(raw))

	event := &platform.GetAnimeCollectionEvent{AnimeCollection: filtered}
	if err := hook.GlobalHookManager.OnGetAnimeCollection().Trigger(event); err != nil {
		return nil, err
	}
	event2 := &platform.GetRawAnimeCollectionEvent{AnimeCollection: raw}
	if err := hook.GlobalHookManager.OnGetRawAnimeCollection().Trigger(event2); err != nil {
		return nil, err
	}
	return event.AnimeCollection, nil
}

func (p *Platform) GetAnimeCollectionWithRelations(ctx context.Context) (*anilist.AnimeCollectionWithRelations, error) {
	_, filtered, _, err := p.collections(ctx)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	ret := &anilist.AnimeCollectionWithRelations{}
	if err := json.Unmarshal(data, ret); err != nil {
		return nil, err
	}
	return ret, nil
}

// Manga is out of scope (plan §3.1): the collections are always empty.

func EmptyMangaCollection() *anilist.MangaCollection {
	return &anilist.MangaCollection{MediaListCollection: &anilist.MangaCollection_MediaListCollection{
		Lists: []*anilist.MangaCollection_MediaListCollection_Lists{},
	}}
}

func (p *Platform) GetMangaCollection(context.Context, bool) (*anilist.MangaCollection, error) {
	return EmptyMangaCollection(), nil
}
func (p *Platform) GetRawMangaCollection(context.Context, bool) (*anilist.MangaCollection, error) {
	return EmptyMangaCollection(), nil
}
func (p *Platform) RefreshMangaCollection(context.Context) (*anilist.MangaCollection, error) {
	return EmptyMangaCollection(), nil
}

//----------------------------------------------------------------------------------------------------------------------
// platform.Platform: metadata (shared, anonymous)

func (p *Platform) GetAnime(ctx context.Context, id int) (*anilist.BaseAnime, error) {
	return p.meta.GetAnime(ctx, id)
}
func (p *Platform) GetAnimeByMalID(ctx context.Context, id int) (*anilist.BaseAnime, error) {
	return p.meta.GetAnimeByMalID(ctx, id)
}
func (p *Platform) GetAnimeWithRelations(ctx context.Context, id int) (*anilist.CompleteAnime, error) {
	return p.meta.GetAnimeWithRelations(ctx, id)
}
func (p *Platform) GetAnimeDetails(ctx context.Context, id int) (*anilist.AnimeDetailsById_Media, error) {
	return p.meta.GetAnimeDetails(ctx, id)
}
func (p *Platform) GetManga(ctx context.Context, id int) (*anilist.BaseManga, error) {
	return p.meta.GetManga(ctx, id)
}
func (p *Platform) GetMangaDetails(ctx context.Context, id int) (*anilist.MangaDetailsById_Media, error) {
	return p.meta.GetMangaDetails(ctx, id)
}
func (p *Platform) GetStudioDetails(ctx context.Context, id int) (*anilist.StudioDetails, error) {
	return p.meta.GetStudioDetails(ctx, id)
}

func (p *Platform) GetAnimeAiringSchedule(ctx context.Context) (*anilist.AnimeAiringSchedule, error) {
	collection, err := p.GetAnimeCollection(ctx, false)
	if err != nil {
		return nil, err
	}
	helper := p.meta.Helper()
	if helper == nil {
		return &anilist.AnimeAiringSchedule{}, nil
	}
	return helper.BuildAnimeAiringSchedule(ctx, collection, p.meta.Client())
}

//----------------------------------------------------------------------------------------------------------------------
// platform.Platform: writes

func (p *Platform) UpdateEntry(ctx context.Context, mediaID int, status *anilist.MediaListStatus, scoreRaw *int, progress *int, startedAt *anilist.FuzzyDateInput, completedAt *anilist.FuzzyDateInput) error {
	return p.withHelperUpdateEntry(ctx, mediaID, status, scoreRaw, progress, startedAt, completedAt, func(event *platform.PreUpdateEntryEvent) error {
		patch := entryPatch{
			Status:   event.Status,
			ScoreRaw: event.ScoreRaw,
			Progress: event.Progress,
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
}

func (p *Platform) UpdateEntryProgress(ctx context.Context, mediaID int, progress int, totalEpisodes *int) error {
	return p.withHelperProgress(ctx, mediaID, progress, totalEpisodes, func(event *platform.PreUpdateEntryProgressEvent) error {
		return p.updateProgress(ctx, mediaID, *event.Progress, event.TotalCount, event.Status)
	})
}

func (p *Platform) UpdateEntryRepeat(ctx context.Context, mediaID int, repeat int) error {
	return p.withHelperRepeat(ctx, mediaID, repeat, func(event *platform.PreUpdateEntryRepeatEvent) error {
		if *event.Repeat < 0 {
			return errors.New("repeat must be >= 0")
		}
		return p.upsert(ctx, mediaID, entryPatch{Repeat: event.Repeat, mustExist: true})
	})
}

func (p *Platform) DeleteEntry(ctx context.Context, mediaID int, entryID int) error {
	return p.withHelperDelete(ctx, mediaID, entryID, func(event *platform.PreDeleteEntryEvent) error {
		return p.deleteEntry(ctx, *event.MediaID, *event.EntryID)
	})
}

// AddMediaToCollection adds the media that aren't in the list as PLANNING.
func (p *Platform) AddMediaToCollection(ctx context.Context, ids []int) error {
	for _, id := range ids {
		if customsource.IsExtensionId(id) {
			continue
		}
		if _, err := p.findEntry(ctx, p.db, id); err == nil {
			continue
		}
		status := anilist.MediaListStatusPlanning
		if err := p.upsert(ctx, id, entryPatch{Status: &status}); err != nil {
			p.logger.Debug().Err(err).Int("mediaId", id).Msg("profiles: Could not add media to the list")
		}
	}
	return nil
}

//----------------------------------------------------------------------------------------------------------------------
// Hooks (same as the simulated platform; custom sources are refused instead of
// being written to the global custom source collections)

func (p *Platform) withHelperUpdateEntry(ctx context.Context, mediaID int, status *anilist.MediaListStatus, scoreRaw *int, progress *int, startedAt *anilist.FuzzyDateInput, completedAt *anilist.FuzzyDateInput, f func(*platform.PreUpdateEntryEvent) error) error {
	if customsource.IsExtensionId(mediaID) {
		return ErrCustomSourceUnsupported
	}
	if h := p.meta.Helper(); h != nil {
		return h.TriggerUpdateEntryHooks(ctx, mediaID, status, scoreRaw, progress, startedAt, completedAt, f)
	}
	return f(&platform.PreUpdateEntryEvent{MediaID: &mediaID, Status: status, ScoreRaw: scoreRaw, Progress: progress, StartedAt: startedAt, CompletedAt: completedAt})
}

func (p *Platform) withHelperProgress(ctx context.Context, mediaID int, progress int, total *int, f func(*platform.PreUpdateEntryProgressEvent) error) error {
	if customsource.IsExtensionId(mediaID) {
		return ErrCustomSourceUnsupported
	}
	if h := p.meta.Helper(); h != nil {
		return h.TriggerUpdateEntryProgressHooks(ctx, mediaID, progress, total, f)
	}
	st := anilist.MediaListStatusCurrent
	return f(&platform.PreUpdateEntryProgressEvent{MediaID: &mediaID, Progress: &progress, TotalCount: total, Status: &st})
}

func (p *Platform) withHelperRepeat(ctx context.Context, mediaID int, repeat int, f func(*platform.PreUpdateEntryRepeatEvent) error) error {
	if customsource.IsExtensionId(mediaID) {
		return ErrCustomSourceUnsupported
	}
	if h := p.meta.Helper(); h != nil {
		return h.TriggerUpdateEntryRepeatHooks(ctx, mediaID, repeat, f)
	}
	return f(&platform.PreUpdateEntryRepeatEvent{MediaID: &mediaID, Repeat: &repeat})
}

func (p *Platform) withHelperDelete(ctx context.Context, mediaID int, entryID int, f func(*platform.PreDeleteEntryEvent) error) error {
	if customsource.IsExtensionId(mediaID) {
		return ErrCustomSourceUnsupported
	}
	if h := p.meta.Helper(); h != nil {
		return h.TriggerDeleteEntryHooks(ctx, mediaID, entryID, f)
	}
	return f(&platform.PreDeleteEntryEvent{MediaID: &mediaID, EntryID: &entryID})
}

//----------------------------------------------------------------------------------------------------------------------
// Store

// findEntry returns the profile's anime entry for mediaID (q is p.db or a transaction).
func (p *Platform) findEntry(ctx context.Context, q *gorm.DB, mediaID int) (*EntryRow, error) {
	var rows []EntryRow
	if err := q.WithContext(ctx).Where("profile_id = ? AND media_id = ? AND media_type = ?", p.profileID, mediaID, mediaTypeAnime).Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrMediaNotFound
	}
	return &rows[0], nil
}

// Entries returns the profile's anime entries, most recently updated first.
func (p *Platform) Entries(ctx context.Context) ([]EntryRow, error) {
	var rows []EntryRow
	err := p.db.WithContext(ctx).Where("profile_id = ? AND media_type = ?", p.profileID, mediaTypeAnime).
		Order("updated_at DESC, id DESC").Find(&rows).Error
	return rows, err
}

// Entry returns the profile's entry for a media.
func (p *Platform) Entry(ctx context.Context, mediaID int) (*EntryRow, error) {
	return p.findEntry(ctx, p.db, mediaID)
}

type activity struct {
	ID        int64   `gorm:"column:id;primaryKey"`
	ProfileID int64   `gorm:"column:profile_id"`
	MediaType string  `gorm:"column:media_type"`
	MediaID   int     `gorm:"column:media_id"`
	Kind      string  `gorm:"column:kind"`
	Episode   *int    `gorm:"column:episode"`
	OldValue  *string `gorm:"column:old_value"`
	NewValue  *string `gorm:"column:new_value"`
	CreatedAt int64   `gorm:"column:created_at"`
}

func (activity) TableName() string { return "activity_log" }

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func optInt(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}

// entryPatch is a partial update. Nil = unchanged; a non-nil date with nil parts clears it.
type entryPatch struct {
	Status      *anilist.MediaListStatus
	ScoreRaw    *int
	Progress    *int
	Repeat      *int
	StartedAt   *ProfileFuzzyDate
	CompletedAt *ProfileFuzzyDate
	mustExist   bool
	// totalEpisodes clamps the progress when known.
	totalEpisodes *int
}

func validStatus(s anilist.MediaListStatus) bool {
	switch s {
	case anilist.MediaListStatusCurrent, anilist.MediaListStatusPlanning, anilist.MediaListStatusCompleted,
		anilist.MediaListStatusDropped, anilist.MediaListStatusPaused, anilist.MediaListStatusRepeating:
		return true
	}
	return false
}

// upsert applies patch to the profile's entry for mediaID (creating it, PLANNING by
// default, if needed) and logs the changes in activity_log, in one transaction.
func (p *Platform) upsert(ctx context.Context, mediaID int, patch entryPatch) error {
	if mediaID <= 0 {
		return errors.New("invalid media id")
	}
	if customsource.IsExtensionId(mediaID) {
		return ErrCustomSourceUnsupported
	}
	if patch.Status != nil && !validStatus(*patch.Status) {
		return fmt.Errorf("invalid status %q", *patch.Status)
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	// The media must exist on AniList before it goes in a list. Fetched outside the
	// transaction (profiles.db may have a single connection).
	var total *int
	if _, err := p.findEntry(ctx, p.db, mediaID); err != nil {
		if !errors.Is(err, ErrMediaNotFound) {
			return err
		}
		if patch.mustExist {
			return ErrMediaNotFound
		}
		media, err := p.meta.EnsureAnime(ctx, mediaID)
		if err != nil {
			return err
		}
		total = media.GetEpisodes()
	} else if got, err := p.meta.GetBaseAnimeMany(ctx, []int{mediaID}); err == nil {
		if m, ok := got[mediaID]; ok {
			total = m.GetEpisodes()
		}
	}
	if patch.totalEpisodes != nil && *patch.totalEpisodes > 0 {
		total = patch.totalEpisodes
	}

	now := p.now().Unix()
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := p.findEntry(ctx, tx, mediaID)
		isNew := false
		if errors.Is(err, ErrMediaNotFound) {
			isNew = true
			row = &EntryRow{ProfileID: p.profileID, MediaType: mediaTypeAnime, MediaID: mediaID,
				Status: string(anilist.MediaListStatusPlanning), CreatedAt: now}
		} else if err != nil {
			return err
		}
		before := *row
		logs := make([]activity, 0, 4)
		add := func(kind string, episode *int, oldV, newV string) {
			logs = append(logs, activity{ProfileID: p.profileID, MediaType: mediaTypeAnime, MediaID: mediaID, Kind: kind,
				Episode: episode, OldValue: strPtr(oldV), NewValue: strPtr(newV), CreatedAt: now})
		}

		if patch.Status != nil {
			row.Status = string(*patch.Status)
		}
		if patch.Progress != nil {
			v := max(0, *patch.Progress)
			if total != nil && *total > 0 && v > *total {
				v = *total
			}
			row.Progress = v
		}
		if patch.ScoreRaw != nil {
			row.ScoreRaw = normalizeScoreRaw(patch.ScoreRaw)
		}
		if patch.Repeat != nil {
			row.Repeat = max(0, *patch.Repeat)
		}
		if patch.StartedAt != nil {
			row.setStartedAt(*patch.StartedAt)
		}
		if patch.CompletedAt != nil {
			row.setCompletedAt(*patch.CompletedAt)
		}
		row.UpdatedAt = now

		if isNew {
			add("added", nil, "", row.Status)
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		} else {
			if before.Status != row.Status {
				add("status", nil, before.Status, row.Status)
			}
			if err := tx.Model(&EntryRow{}).Where("id = ? AND profile_id = ?", row.ID, p.profileID).Updates(map[string]any{
				"status": row.Status, "progress": row.Progress, "score_raw": row.ScoreRaw, "repeat": row.Repeat,
				"started_y": row.StartedY, "started_m": row.StartedM, "started_d": row.StartedD,
				"completed_y": row.CompletedY, "completed_m": row.CompletedM, "completed_d": row.CompletedD,
				"updated_at": row.UpdatedAt,
			}).Error; err != nil {
				return err
			}
		}
		if before.Progress != row.Progress {
			add("progress", new(row.Progress), strconv.Itoa(before.Progress), strconv.Itoa(row.Progress))
		}
		if optInt(before.ScoreRaw) != optInt(row.ScoreRaw) {
			add("score", nil, optInt(before.ScoreRaw), optInt(row.ScoreRaw))
		}
		if before.Repeat != row.Repeat {
			add("repeat", nil, strconv.Itoa(before.Repeat), strconv.Itoa(row.Repeat))
		}
		if dateString(before.StartedAt()) != dateString(row.StartedAt()) {
			add("started", nil, dateString(before.StartedAt()), dateString(row.StartedAt()))
		}
		if dateString(before.CompletedAt()) != dateString(row.CompletedAt()) {
			add("completed", nil, dateString(before.CompletedAt()), dateString(row.CompletedAt()))
		}
		if len(logs) > 0 {
			return tx.Create(&logs).Error
		}
		return nil
	})
	if err != nil {
		return err
	}
	p.Invalidate()
	return nil
}

// updateProgress is UpdateEntryProgress after the hooks: COMPLETED when the last
// episode is reached (REPEATING -> COMPLETED with repeat+1), REPEATING stays
// REPEATING before that, anything else becomes CURRENT (hookStatus, default CURRENT).
func (p *Platform) updateProgress(ctx context.Context, mediaID int, progress int, totalCount *int, hookStatus *anilist.MediaListStatus) error {
	current, err := p.findEntry(ctx, p.db, mediaID)
	if err != nil && !errors.Is(err, ErrMediaNotFound) {
		return err
	}

	total := totalCount
	if total == nil || *total <= 0 {
		if got, gerr := p.meta.GetBaseAnimeMany(ctx, []int{mediaID}); gerr == nil {
			if m, ok := got[mediaID]; ok {
				total = m.GetEpisodes()
			}
		}
	}

	status := anilist.MediaListStatusCurrent
	if hookStatus != nil && validStatus(*hookStatus) {
		status = *hookStatus
	}
	wasRepeating := current != nil && current.Status == string(anilist.MediaListStatusRepeating)
	if wasRepeating && status == anilist.MediaListStatusCurrent {
		status = anilist.MediaListStatusRepeating
	}
	patch := entryPatch{Progress: &progress, totalEpisodes: total}
	today := Today(p.now())
	if total != nil && *total > 0 && progress >= *total {
		status = anilist.MediaListStatusCompleted
		if wasRepeating {
			patch.Repeat = new(current.Repeat + 1)
		}
		if current == nil || current.CompletedAt().IsZero() {
			patch.CompletedAt = &today
		}
	}
	if (current == nil || current.StartedAt().IsZero()) && progress > 0 && !wasRepeating {
		patch.StartedAt = &today
	}
	patch.Status = &status
	return p.upsert(ctx, mediaID, patch)
}

func (p *Platform) deleteEntry(ctx context.Context, mediaID int, entryID int) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	now := p.now().Unix()
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []EntryRow
		q := tx.Where("profile_id = ? AND media_type = ?", p.profileID, mediaTypeAnime)
		if entryID > 0 {
			q = q.Where("id = ?", entryID)
		} else {
			q = q.Where("media_id = ?", mediaID)
		}
		if err := q.Limit(1).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return ErrMediaNotFound
		}
		row := rows[0]
		if err := tx.Where("id = ? AND profile_id = ?", row.ID, p.profileID).Delete(&EntryRow{}).Error; err != nil {
			return err
		}
		return tx.Create(&activity{ProfileID: p.profileID, MediaType: mediaTypeAnime, MediaID: row.MediaID, Kind: "deleted",
			OldValue: strPtr(row.Status), CreatedAt: now}).Error
	})
	if err != nil {
		return err
	}
	p.Invalidate()
	return nil
}
