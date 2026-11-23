package router

import (
	"context"
	"seanime/internal/api/anilist"
	"seanime/internal/platforms/platform"
)

// LanePlatform is the platform given to the server-side playback modules
// (playback manager, media core, direct stream, video core...). They call the
// platform without a request context; LanePlatform adds the profile that owns the
// playback lane, so the progress goes to the profile that started the playback.
// A context that already carries a profile is left alone.
type LanePlatform struct {
	inner   platform.Platform
	withCtx func(ctx context.Context) context.Context
}

var _ platform.Platform = (*LanePlatform)(nil)

// NewLanePlatform wraps inner; withCtx adds the lane owner (profiles.PlaybackLane.Context).
func NewLanePlatform(inner platform.Platform, withCtx func(ctx context.Context) context.Context) *LanePlatform {
	return &LanePlatform{inner: inner, withCtx: withCtx}
}

func (l *LanePlatform) c(ctx context.Context) context.Context { return l.withCtx(ctx) }

func (l *LanePlatform) SetUsername(u string) { l.inner.SetUsername(u) }
func (l *LanePlatform) UpdateEntry(ctx context.Context, mediaID int, status *anilist.MediaListStatus, scoreRaw *int, progress *int, startedAt *anilist.FuzzyDateInput, completedAt *anilist.FuzzyDateInput) error {
	return l.inner.UpdateEntry(l.c(ctx), mediaID, status, scoreRaw, progress, startedAt, completedAt)
}
func (l *LanePlatform) UpdateEntryProgress(ctx context.Context, mediaID int, progress int, totalEpisodes *int) error {
	return l.inner.UpdateEntryProgress(l.c(ctx), mediaID, progress, totalEpisodes)
}
func (l *LanePlatform) UpdateEntryRepeat(ctx context.Context, mediaID int, repeat int) error {
	return l.inner.UpdateEntryRepeat(l.c(ctx), mediaID, repeat)
}
func (l *LanePlatform) DeleteEntry(ctx context.Context, mediaID int, entryID int) error {
	return l.inner.DeleteEntry(l.c(ctx), mediaID, entryID)
}
func (l *LanePlatform) GetAnime(ctx context.Context, id int) (*anilist.BaseAnime, error) {
	return l.inner.GetAnime(l.c(ctx), id)
}
func (l *LanePlatform) GetAnimeByMalID(ctx context.Context, id int) (*anilist.BaseAnime, error) {
	return l.inner.GetAnimeByMalID(l.c(ctx), id)
}
func (l *LanePlatform) GetAnimeWithRelations(ctx context.Context, id int) (*anilist.CompleteAnime, error) {
	return l.inner.GetAnimeWithRelations(l.c(ctx), id)
}
func (l *LanePlatform) GetAnimeDetails(ctx context.Context, id int) (*anilist.AnimeDetailsById_Media, error) {
	return l.inner.GetAnimeDetails(l.c(ctx), id)
}
func (l *LanePlatform) GetManga(ctx context.Context, id int) (*anilist.BaseManga, error) {
	return l.inner.GetManga(l.c(ctx), id)
}
func (l *LanePlatform) GetAnimeCollection(ctx context.Context, bypassCache bool) (*anilist.AnimeCollection, error) {
	return l.inner.GetAnimeCollection(l.c(ctx), bypassCache)
}
func (l *LanePlatform) GetRawAnimeCollection(ctx context.Context, bypassCache bool) (*anilist.AnimeCollection, error) {
	return l.inner.GetRawAnimeCollection(l.c(ctx), bypassCache)
}
func (l *LanePlatform) GetMangaDetails(ctx context.Context, id int) (*anilist.MangaDetailsById_Media, error) {
	return l.inner.GetMangaDetails(l.c(ctx), id)
}
func (l *LanePlatform) GetAnimeCollectionWithRelations(ctx context.Context) (*anilist.AnimeCollectionWithRelations, error) {
	return l.inner.GetAnimeCollectionWithRelations(l.c(ctx))
}
func (l *LanePlatform) GetMangaCollection(ctx context.Context, bypassCache bool) (*anilist.MangaCollection, error) {
	return l.inner.GetMangaCollection(l.c(ctx), bypassCache)
}
func (l *LanePlatform) GetRawMangaCollection(ctx context.Context, bypassCache bool) (*anilist.MangaCollection, error) {
	return l.inner.GetRawMangaCollection(l.c(ctx), bypassCache)
}
func (l *LanePlatform) AddMediaToCollection(ctx context.Context, ids []int) error {
	return l.inner.AddMediaToCollection(l.c(ctx), ids)
}
func (l *LanePlatform) GetStudioDetails(ctx context.Context, id int) (*anilist.StudioDetails, error) {
	return l.inner.GetStudioDetails(l.c(ctx), id)
}
func (l *LanePlatform) GetAnilistClient() anilist.AnilistClient { return l.inner.GetAnilistClient() }
func (l *LanePlatform) RefreshAnimeCollection(ctx context.Context) (*anilist.AnimeCollection, error) {
	return l.inner.RefreshAnimeCollection(l.c(ctx))
}
func (l *LanePlatform) RefreshMangaCollection(ctx context.Context) (*anilist.MangaCollection, error) {
	return l.inner.RefreshMangaCollection(l.c(ctx))
}
func (l *LanePlatform) GetViewerStats(ctx context.Context) (*anilist.ViewerStats, error) {
	return l.inner.GetViewerStats(l.c(ctx))
}
func (l *LanePlatform) GetAnimeAiringSchedule(ctx context.Context) (*anilist.AnimeAiringSchedule, error) {
	return l.inner.GetAnimeAiringSchedule(l.c(ctx))
}
func (l *LanePlatform) ClearCache() { l.inner.ClearCache() }
func (l *LanePlatform) Close()      {}
