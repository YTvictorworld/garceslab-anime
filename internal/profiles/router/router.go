// Package router implements RouterPlatform (plan §3.4.2): the platform.Platform
// installed behind App.AnilistPlatformRef. Every call is routed to the local
// platform of the profile in the context. Without a profile, metadata reads go to
// the shared anonymous metadata service, collection reads are empty and writes fail
// with ErrNoProfile, so nothing is ever written to the wrong profile.
package router

import (
	"context"
	"fmt"
	"seanime/internal/api/anilist"
	"seanime/internal/platforms/platform"
	"seanime/internal/profiles"
	"seanime/internal/profiles/localplatform"
	"seanime/internal/profiles/metadata"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gorm.io/gorm"
)

// ErrNoProfile is returned by writes without a profile in the context.
var ErrNoProfile = fmt.Errorf("%w: no profile selected, the change was not saved", profiles.ErrProfileRequired)

type Options struct {
	Meta   *metadata.Service
	DB     *gorm.DB
	Logger *zerolog.Logger
	Now    func() time.Time
}

type Router struct {
	meta   *metadata.Service
	db     *gorm.DB
	logger *zerolog.Logger
	now    func() time.Time

	mu        sync.Mutex
	platforms map[int64]*localplatform.Platform
}

var _ platform.Platform = (*Router)(nil)

func New(opts Options) *Router {
	logger := opts.Logger
	if logger == nil {
		l := zerolog.Nop()
		logger = &l
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	r := &Router{meta: opts.Meta, db: opts.DB, logger: logger, now: now, platforms: map[int64]*localplatform.Platform{}}
	opts.Meta.OnSnapshotsUpdated(func([]int) { r.InvalidateAll() })
	return r
}

// Meta returns the shared metadata service.
func (r *Router) Meta() *metadata.Service { return r.meta }

// For returns the local platform of a profile (created on first use).
func (r *Router) For(profileID int64) *localplatform.Platform {
	if profileID <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.platforms[profileID]
	if !ok {
		p = localplatform.New(localplatform.Options{ProfileID: profileID, DB: r.db, Meta: r.meta, Logger: r.logger, Now: r.now})
		r.platforms[profileID] = p
	}
	return p
}

// PlatformFor returns the platform of the profile in ctx.
func (r *Router) PlatformFor(ctx context.Context) (*localplatform.Platform, bool) {
	id := profiles.IDFromContext(ctx)
	if id <= 0 {
		return nil, false
	}
	return r.For(id), true
}

// ForgetProfile drops the cached platform of a deleted profile.
func (r *Router) ForgetProfile(profileID int64) {
	r.mu.Lock()
	delete(r.platforms, profileID)
	r.mu.Unlock()
}

// InvalidateAll drops every profile's in-memory collection (metadata changed).
func (r *Router) InvalidateAll() {
	r.mu.Lock()
	ps := make([]*localplatform.Platform, 0, len(r.platforms))
	for _, p := range r.platforms {
		ps = append(ps, p)
	}
	r.mu.Unlock()
	for _, p := range ps {
		p.Invalidate()
	}
}

func (r *Router) noProfileWrite(op string, mediaID int) error {
	r.logger.Warn().Str("op", op).Int("mediaId", mediaID).Msg("profiles: List write without a profile refused")
	return ErrNoProfile
}

func emptyAnimeCollection() *anilist.AnimeCollection {
	return &anilist.AnimeCollection{MediaListCollection: &anilist.AnimeCollection_MediaListCollection{
		Lists: []*anilist.AnimeCollection_MediaListCollection_Lists{},
	}}
}

//----------------------------------------------------------------------------------------------------------------------
// platform.Platform

func (r *Router) SetUsername(string) {}

func (r *Router) UpdateEntry(ctx context.Context, mediaID int, status *anilist.MediaListStatus, scoreRaw *int, progress *int, startedAt *anilist.FuzzyDateInput, completedAt *anilist.FuzzyDateInput) error {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.UpdateEntry(ctx, mediaID, status, scoreRaw, progress, startedAt, completedAt)
	}
	return r.noProfileWrite("UpdateEntry", mediaID)
}

func (r *Router) UpdateEntryProgress(ctx context.Context, mediaID int, progress int, totalEpisodes *int) error {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.UpdateEntryProgress(ctx, mediaID, progress, totalEpisodes)
	}
	return r.noProfileWrite("UpdateEntryProgress", mediaID)
}

func (r *Router) UpdateEntryRepeat(ctx context.Context, mediaID int, repeat int) error {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.UpdateEntryRepeat(ctx, mediaID, repeat)
	}
	return r.noProfileWrite("UpdateEntryRepeat", mediaID)
}

func (r *Router) DeleteEntry(ctx context.Context, mediaID int, entryID int) error {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.DeleteEntry(ctx, mediaID, entryID)
	}
	return r.noProfileWrite("DeleteEntry", mediaID)
}

func (r *Router) AddMediaToCollection(ctx context.Context, ids []int) error {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.AddMediaToCollection(ctx, ids)
	}
	return r.noProfileWrite("AddMediaToCollection", 0)
}

func (r *Router) GetAnime(ctx context.Context, id int) (*anilist.BaseAnime, error) {
	return r.meta.GetAnime(ctx, id)
}

func (r *Router) GetAnimeByMalID(ctx context.Context, id int) (*anilist.BaseAnime, error) {
	return r.meta.GetAnimeByMalID(ctx, id)
}

func (r *Router) GetAnimeWithRelations(ctx context.Context, id int) (*anilist.CompleteAnime, error) {
	return r.meta.GetAnimeWithRelations(ctx, id)
}

func (r *Router) GetAnimeDetails(ctx context.Context, id int) (*anilist.AnimeDetailsById_Media, error) {
	return r.meta.GetAnimeDetails(ctx, id)
}

func (r *Router) GetManga(ctx context.Context, id int) (*anilist.BaseManga, error) {
	return r.meta.GetManga(ctx, id)
}

func (r *Router) GetMangaDetails(ctx context.Context, id int) (*anilist.MangaDetailsById_Media, error) {
	return r.meta.GetMangaDetails(ctx, id)
}

func (r *Router) GetStudioDetails(ctx context.Context, id int) (*anilist.StudioDetails, error) {
	return r.meta.GetStudioDetails(ctx, id)
}

func (r *Router) GetAnimeCollection(ctx context.Context, bypassCache bool) (*anilist.AnimeCollection, error) {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.GetAnimeCollection(ctx, bypassCache)
	}
	return emptyAnimeCollection(), nil
}

func (r *Router) GetRawAnimeCollection(ctx context.Context, bypassCache bool) (*anilist.AnimeCollection, error) {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.GetRawAnimeCollection(ctx, bypassCache)
	}
	return emptyAnimeCollection(), nil
}

func (r *Router) RefreshAnimeCollection(ctx context.Context) (*anilist.AnimeCollection, error) {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.RefreshAnimeCollection(ctx)
	}
	return emptyAnimeCollection(), nil
}

func (r *Router) GetAnimeCollectionWithRelations(ctx context.Context) (*anilist.AnimeCollectionWithRelations, error) {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.GetAnimeCollectionWithRelations(ctx)
	}
	return &anilist.AnimeCollectionWithRelations{MediaListCollection: &anilist.AnimeCollectionWithRelations_MediaListCollection{
		Lists: []*anilist.AnimeCollectionWithRelations_MediaListCollection_Lists{},
	}}, nil
}

func (r *Router) GetMangaCollection(context.Context, bool) (*anilist.MangaCollection, error) {
	return localplatform.EmptyMangaCollection(), nil
}

func (r *Router) GetRawMangaCollection(context.Context, bool) (*anilist.MangaCollection, error) {
	return localplatform.EmptyMangaCollection(), nil
}

func (r *Router) RefreshMangaCollection(context.Context) (*anilist.MangaCollection, error) {
	return localplatform.EmptyMangaCollection(), nil
}

func (r *Router) GetAnilistClient() anilist.AnilistClient { return r.meta.Client() }

func (r *Router) GetViewerStats(ctx context.Context) (*anilist.ViewerStats, error) {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.GetViewerStats(ctx)
	}
	return &anilist.ViewerStats{Viewer: &anilist.ViewerStats_Viewer{Statistics: &anilist.ViewerStats_Viewer_Statistics{
		Anime: &anilist.ViewerStats_Viewer_Statistics_Anime{},
		Manga: &anilist.ViewerStats_Viewer_Statistics_Manga{},
	}}}, nil
}

func (r *Router) GetAnimeAiringSchedule(ctx context.Context) (*anilist.AnimeAiringSchedule, error) {
	if p, ok := r.PlatformFor(ctx); ok {
		return p.GetAnimeAiringSchedule(ctx)
	}
	return &anilist.AnimeAiringSchedule{}, nil
}

func (r *Router) ClearCache() {
	if h := r.meta.Helper(); h != nil {
		h.ClearCache()
	}
	r.InvalidateAll()
}

func (r *Router) Close() {}

//----------------------------------------------------------------------------------------------------------------------
// Union collection (global modules)

var statusRank = map[anilist.MediaListStatus]int{
	anilist.MediaListStatusCurrent:   0,
	anilist.MediaListStatusRepeating: 1,
	anilist.MediaListStatusPlanning:  2,
	anilist.MediaListStatusPaused:    3,
	anilist.MediaListStatusCompleted: 4,
	anilist.MediaListStatusDropped:   5,
}

// ActiveProfileIDs returns the ids of the profiles that aren't in the trash.
func (r *Router) ActiveProfileIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Raw("SELECT id FROM profiles WHERE deleted_at IS NULL ORDER BY id").Scan(&ids).Error
	return ids, err
}

// UnionAnimeCollection merges the lists of every profile for the global modules
// (auto downloader, auto scanner, library explorer, plan §3.4.3): each media
// appears once, in its most "active" status across profiles.
func (r *Router) UnionAnimeCollection(ctx context.Context) (*anilist.AnimeCollection, error) {
	ids, err := r.ActiveProfileIDs(ctx)
	if err != nil {
		return nil, err
	}
	type pick struct {
		entry *anilist.AnimeCollection_MediaListCollection_Lists_Entries
		rank  int
	}
	best := map[int]pick{}
	for _, id := range ids {
		col, err := r.For(id).GetAnimeCollection(profiles.WithProfile(ctx, &profiles.Profile{ID: id}), false)
		if err != nil {
			return nil, err
		}
		for _, l := range col.GetMediaListCollection().GetLists() {
			for _, e := range l.GetEntries() {
				if e == nil || e.GetMedia() == nil || e.GetStatus() == nil {
					continue
				}
				rank, ok := statusRank[*e.GetStatus()]
				if !ok {
					rank = 99
				}
				mid := e.GetMedia().GetID()
				if cur, ok := best[mid]; !ok || rank < cur.rank {
					best[mid] = pick{entry: e, rank: rank}
				}
			}
		}
	}
	byStatus := map[anilist.MediaListStatus][]*anilist.AnimeCollection_MediaListCollection_Lists_Entries{}
	mids := make([]int, 0, len(best))
	for mid := range best {
		mids = append(mids, mid)
	}
	sort.Ints(mids)
	for _, mid := range mids {
		e := best[mid].entry
		byStatus[*e.GetStatus()] = append(byStatus[*e.GetStatus()], e)
	}
	ret := emptyAnimeCollection()
	for _, st := range []anilist.MediaListStatus{anilist.MediaListStatusCurrent, anilist.MediaListStatusRepeating,
		anilist.MediaListStatusPlanning, anilist.MediaListStatusPaused, anilist.MediaListStatusCompleted, anilist.MediaListStatusDropped} {
		if len(byStatus[st]) == 0 {
			continue
		}
		ret.MediaListCollection.Lists = append(ret.MediaListCollection.Lists, &anilist.AnimeCollection_MediaListCollection_Lists{
			Status: new(st), Name: new(string(st)), IsCustomList: new(false), Entries: byStatus[st],
		})
	}
	return ret, nil
}
