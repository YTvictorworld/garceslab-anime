// Package metadata is the shared, anonymous AniList metadata service of the local
// profiles (plan §3.4.4): one anonymous client behind the existing CacheLayer, plus
// the global media_snapshots table, which lets the profile lists be built without
// asking AniList each time and keeps them readable while AniList is down.
package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"seanime/internal/api/anilist"
	"seanime/internal/customsource"
	"seanime/internal/platforms/shared_platform"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// BatchSize is the largest page AniList returns for SearchBaseAnimeByIds.
	BatchSize = 50
	// ReleasingMaxAge is how old the snapshot of an airing anime can get.
	ReleasingMaxAge = 6 * time.Hour
	// StaleMaxAge is how old any other snapshot can get.
	StaleMaxAge = 14 * 24 * time.Hour
	// BackgroundRatePerMinute caps background refresh requests (one per batch).
	BackgroundRatePerMinute = 20
)

// ErrAuthenticatedClient is returned when the service is given a client with a token.
var ErrAuthenticatedClient = errors.New("metadata: the shared AniList client must be anonymous")

// snapshot is a row of media_snapshots (global, no profile_id).
type snapshot struct {
	MediaID     int     `gorm:"column:media_id;primaryKey"`
	MediaType   string  `gorm:"column:media_type"`
	MediaStatus *string `gorm:"column:media_status"`
	Payload     string  `gorm:"column:payload"`
	FetchedAt   int64   `gorm:"column:fetched_at"`
}

func (snapshot) TableName() string { return "media_snapshots" }

type Options struct {
	// Client must be anonymous (wrap it in shared_platform.NewCacheLayer in production).
	Client anilist.AnilistClient
	DB     *gorm.DB
	// Helper runs the platform hooks and resolves custom sources (shared by all profiles).
	Helper *shared_platform.PlatformHelper
	Logger *zerolog.Logger
	Now    func() time.Time
	// RatePerMinute overrides BackgroundRatePerMinute (tests).
	RatePerMinute int
}

// Service is shared by every profile.
type Service struct {
	client anilist.AnilistClient
	db     *gorm.DB
	helper *shared_platform.PlatformHelper
	logger *zerolog.Logger
	now    func() time.Time
	bucket *TokenBucket

	refreshMu      sync.Mutex
	refreshPending map[int]struct{}
	refreshRunning bool
	onUpdated      []func(ids []int)
}

func New(opts Options) (*Service, error) {
	if opts.Client == nil || opts.DB == nil {
		return nil, errors.New("metadata: client and db are required")
	}
	if opts.Client.IsAuthenticated() {
		return nil, ErrAuthenticatedClient
	}
	logger := opts.Logger
	if logger == nil {
		l := zerolog.Nop()
		logger = &l
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	rate := opts.RatePerMinute
	if rate <= 0 {
		rate = BackgroundRatePerMinute
	}
	return &Service{
		client:         opts.Client,
		db:             opts.DB,
		helper:         opts.Helper,
		logger:         logger,
		now:            now,
		bucket:         NewTokenBucket(rate, time.Minute, now),
		refreshPending: map[int]struct{}{},
	}, nil
}

// Client returns the anonymous AniList client.
func (s *Service) Client() anilist.AnilistClient { return s.client }

// Helper returns the shared platform helper (may be nil in tests).
func (s *Service) Helper() *shared_platform.PlatformHelper { return s.helper }

// OnSnapshotsUpdated registers a callback run after a background refresh changed snapshots.
func (s *Service) OnSnapshotsUpdated(f func(ids []int)) {
	s.refreshMu.Lock()
	s.onUpdated = append(s.onUpdated, f)
	s.refreshMu.Unlock()
}

//----------------------------------------------------------------------------------------------------------------------
// Snapshots

func (s *Service) saveSnapshots(ctx context.Context, media []*anilist.BaseAnime) error {
	if len(media) == 0 {
		return nil
	}
	now := s.now().Unix()
	rows := make([]snapshot, 0, len(media))
	for _, m := range media {
		if m == nil || m.ID == 0 || customsource.IsExtensionId(m.ID) {
			continue
		}
		payload, err := json.Marshal(m)
		if err != nil {
			return err
		}
		var status *string
		if m.Status != nil {
			st := string(*m.Status)
			status = &st
		}
		rows = append(rows, snapshot{MediaID: m.ID, MediaType: "ANIME", MediaStatus: status, Payload: string(payload), FetchedAt: now})
	}
	if len(rows) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "media_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"media_type", "media_status", "payload", "fetched_at"}),
	}).Create(&rows).Error
}

func (s *Service) loadSnapshots(ctx context.Context, ids []int) (map[int]snapshot, error) {
	ret := make(map[int]snapshot, len(ids))
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		var rows []snapshot
		if err := s.db.WithContext(ctx).Where("media_type = 'ANIME' AND media_id IN ?", ids[start:end]).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			ret[r.MediaID] = r
		}
	}
	return ret, nil
}

func decode(row snapshot) (*anilist.BaseAnime, error) {
	var m anilist.BaseAnime
	if err := json.Unmarshal([]byte(row.Payload), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// fetchBatch asks AniList for up to BatchSize anime. It sends the generated
// SearchBaseAnimeByIds document as a custom query with only the variables it
// needs: AniList answers 500 to `status_in: null` and nothing to `season: null` /
// `format: null`, which the generated client always sends.
func (s *Service) fetchBatch(ctx context.Context, ids []int) ([]*anilist.BaseAnime, error) {
	body, err := json.Marshal(map[string]any{
		"query":     anilist.SearchBaseAnimeByIdsDocument,
		"variables": map[string]any{"ids": ids, "page": 1, "perPage": BatchSize},
	})
	if err != nil {
		return nil, err
	}
	data, err := s.client.CustomQuery(body, s.logger)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var res anilist.SearchBaseAnimeByIds
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("metadata: decode SearchBaseAnimeByIds: %w", err)
	}
	return res.GetPage().GetMedia(), nil
}

// GetBaseAnimeMany returns the anime for ids, from media_snapshots when present and
// from AniList (batches of BatchSize) otherwise. Missing ids are left out.
func (s *Service) GetBaseAnimeMany(ctx context.Context, ids []int) (map[int]*anilist.BaseAnime, error) {
	ids = uniqueIDs(ids)
	ret := make(map[int]*anilist.BaseAnime, len(ids))
	if len(ids) == 0 {
		return ret, nil
	}
	rows, err := s.loadSnapshots(ctx, ids)
	if err != nil {
		return nil, err
	}
	missing := make([]int, 0)
	for _, id := range ids {
		if row, ok := rows[id]; ok {
			if m, err := decode(row); err == nil {
				ret[id] = m
				continue
			}
		}
		if customsource.IsExtensionId(id) {
			continue // extension media are not stored in snapshots
		}
		missing = append(missing, id)
	}
	var fetchErr error
	for start := 0; start < len(missing); start += BatchSize {
		end := min(start+BatchSize, len(missing))
		media, err := s.fetchBatch(ctx, missing[start:end])
		if err != nil {
			fetchErr = err
			s.logger.Warn().Err(err).Int("count", end-start).Msg("profiles metadata: Could not fetch anime")
			continue
		}
		if err := s.saveSnapshots(ctx, media); err != nil {
			return nil, err
		}
		for _, m := range media {
			if m != nil {
				ret[m.ID] = m
			}
		}
	}
	if len(ret) == 0 && fetchErr != nil {
		return nil, fetchErr
	}
	return ret, nil
}

// EnsureAnime returns the anime and makes sure it has a snapshot (used before a
// media is added to a list, so it must exist on AniList).
func (s *Service) EnsureAnime(ctx context.Context, id int) (*anilist.BaseAnime, error) {
	got, err := s.GetBaseAnimeMany(ctx, []int{id})
	if err != nil {
		return nil, err
	}
	if m, ok := got[id]; ok {
		return m, nil
	}
	// SearchBaseAnimeByIds hides adult media unless asked: fall back to the single query.
	res, err := s.client.BaseAnimeByID(ctx, &id)
	if err != nil {
		return nil, fmt.Errorf("media %d not found on AniList: %w", id, err)
	}
	m := res.GetMedia()
	if m == nil {
		return nil, fmt.Errorf("media %d not found on AniList", id)
	}
	if err := s.saveSnapshots(ctx, []*anilist.BaseAnime{m}); err != nil {
		return nil, err
	}
	return m, nil
}

// HasSnapshot reports whether a media has a snapshot.
func (s *Service) HasSnapshot(ctx context.Context, id int) bool {
	var n int64
	_ = s.db.WithContext(ctx).Model(&snapshot{}).Where("media_id = ?", id).Count(&n).Error
	return n > 0
}

//----------------------------------------------------------------------------------------------------------------------
// Public metadata reads (same answer for every profile)

func (s *Service) GetAnime(ctx context.Context, id int) (*anilist.BaseAnime, error) {
	if s.helper != nil {
		if cached, ok := s.helper.GetCachedBaseAnime(id); ok {
			return s.helper.TriggerGetAnimeEvent(cached)
		}
		if media, isCustom, err := s.helper.HandleCustomSourceAnime(ctx, id); isCustom {
			if err != nil {
				return nil, err
			}
			triggered, err := s.helper.TriggerGetAnimeEvent(media)
			if err != nil {
				return nil, err
			}
			s.helper.SetCachedBaseAnime(id, triggered)
			return triggered, nil
		}
	}

	res, err := s.client.BaseAnimeByID(ctx, &id)
	if err != nil {
		// AniList down: answer from the snapshot when there is one.
		if rows, lerr := s.loadSnapshots(ctx, []int{id}); lerr == nil {
			if row, ok := rows[id]; ok {
				if m, derr := decode(row); derr == nil {
					return s.trigger(m)
				}
			}
		}
		return nil, err
	}
	media := res.GetMedia()
	if media != nil && s.HasSnapshot(ctx, id) {
		_ = s.saveSnapshots(ctx, []*anilist.BaseAnime{media}) // keep list metadata fresh
	}
	triggered, err := s.trigger(media)
	if err != nil {
		return nil, err
	}
	if s.helper != nil {
		s.helper.SetCachedBaseAnime(id, triggered)
	}
	return triggered, nil
}

func (s *Service) trigger(m *anilist.BaseAnime) (*anilist.BaseAnime, error) {
	if s.helper == nil {
		return m, nil
	}
	return s.helper.TriggerGetAnimeEvent(m)
}

func (s *Service) GetAnimeByMalID(ctx context.Context, malID int) (*anilist.BaseAnime, error) {
	res, err := s.client.BaseAnimeByMalID(ctx, &malID)
	if err != nil {
		return nil, err
	}
	return s.trigger(res.GetMedia())
}

func (s *Service) GetAnimeDetails(ctx context.Context, id int) (*anilist.AnimeDetailsById_Media, error) {
	if s.helper != nil {
		if media, isCustom, err := s.helper.HandleCustomSourceAnimeDetails(ctx, id); isCustom {
			if err != nil {
				return nil, err
			}
			return s.helper.TriggerGetAnimeDetailsEvent(media)
		}
	}
	res, err := s.client.AnimeDetailsByID(ctx, &id)
	if err != nil {
		return nil, err
	}
	if s.helper == nil {
		return res.GetMedia(), nil
	}
	return s.helper.TriggerGetAnimeDetailsEvent(res.GetMedia())
}

func (s *Service) GetAnimeWithRelations(ctx context.Context, id int) (*anilist.CompleteAnime, error) {
	if s.helper != nil {
		if cached, ok := s.helper.GetCachedCompleteAnime(id); ok {
			return cached, nil
		}
		if media, isCustom, err := s.helper.HandleCustomSourceAnimeWithRelations(ctx, id); isCustom {
			if err != nil {
				return nil, err
			}
			s.helper.SetCachedCompleteAnime(id, media)
			return media, nil
		}
	}
	res, err := s.client.CompleteAnimeByID(ctx, &id)
	if err != nil {
		return nil, err
	}
	if s.helper != nil {
		s.helper.SetCachedCompleteAnime(id, res.GetMedia())
	}
	return res.GetMedia(), nil
}

func (s *Service) GetManga(ctx context.Context, id int) (*anilist.BaseManga, error) {
	if s.helper != nil {
		if cached, ok := s.helper.GetCachedBaseManga(id); ok {
			return s.helper.TriggerGetMangaEvent(cached)
		}
		if media, isCustom, err := s.helper.HandleCustomSourceManga(ctx, id); isCustom {
			if err != nil {
				return nil, err
			}
			return s.helper.TriggerGetMangaEvent(media)
		}
	}
	res, err := s.client.BaseMangaByID(ctx, &id)
	if err != nil {
		return nil, err
	}
	if s.helper == nil {
		return res.GetMedia(), nil
	}
	m, err := s.helper.TriggerGetMangaEvent(res.GetMedia())
	if err == nil {
		s.helper.SetCachedBaseManga(id, m)
	}
	return m, err
}

func (s *Service) GetMangaDetails(ctx context.Context, id int) (*anilist.MangaDetailsById_Media, error) {
	if s.helper != nil {
		if media, isCustom, err := s.helper.HandleCustomSourceMangaDetails(ctx, id); isCustom {
			return media, err
		}
	}
	res, err := s.client.MangaDetailsByID(ctx, &id)
	if err != nil {
		return nil, err
	}
	return res.GetMedia(), nil
}

func (s *Service) GetStudioDetails(ctx context.Context, id int) (*anilist.StudioDetails, error) {
	res, err := s.client.StudioDetails(ctx, &id)
	if err != nil {
		return nil, err
	}
	if s.helper == nil {
		return res, nil
	}
	return s.helper.TriggerGetStudioDetailsEvent(res)
}

//----------------------------------------------------------------------------------------------------------------------
// Background refresh

// NeedsRefresh reports whether a snapshot should be refreshed: airing media every
// ReleasingMaxAge, everything else every StaleMaxAge.
func NeedsRefresh(status *string, fetchedAt int64, now time.Time) bool {
	age := now.Sub(time.Unix(fetchedAt, 0))
	if status != nil && (*status == string(anilist.MediaStatusReleasing) || *status == string(anilist.MediaStatusNotYetReleased)) {
		return age >= ReleasingMaxAge
	}
	return age >= StaleMaxAge
}

// StaleIDs returns the ids among ids whose snapshot needs a refresh.
func (s *Service) StaleIDs(ctx context.Context, ids []int) ([]int, error) {
	rows, err := s.loadSnapshots(ctx, uniqueIDs(ids))
	if err != nil {
		return nil, err
	}
	now := s.now()
	ret := make([]int, 0)
	for id, row := range rows {
		if NeedsRefresh(row.MediaStatus, row.FetchedAt, now) {
			ret = append(ret, id)
		}
	}
	sort.Ints(ret)
	return ret, nil
}

// RefreshStale refreshes the stale snapshots among ids, one rate-limited request
// per batch. It blocks; RefreshStaleAsync is the non-blocking variant.
func (s *Service) RefreshStale(ctx context.Context, ids []int) (refreshed []int, err error) {
	stale, err := s.StaleIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(stale); start += BatchSize {
		end := min(start+BatchSize, len(stale))
		if err := s.bucket.Wait(ctx); err != nil {
			return refreshed, err
		}
		media, err := s.fetchBatch(ctx, stale[start:end])
		if err != nil {
			s.logger.Warn().Err(err).Msg("profiles metadata: Background refresh failed")
			return refreshed, err
		}
		if err := s.saveSnapshots(ctx, media); err != nil {
			return refreshed, err
		}
		for _, m := range media {
			if m != nil {
				refreshed = append(refreshed, m.ID)
				if s.helper != nil {
					s.helper.SetCachedBaseAnime(m.ID, m)
				}
			}
		}
	}
	return refreshed, nil
}

// RefreshStaleAsync queues ids for a background refresh (deduplicated, one worker).
func (s *Service) RefreshStaleAsync(ids []int) {
	s.refreshMu.Lock()
	for _, id := range ids {
		if !customsource.IsExtensionId(id) {
			s.refreshPending[id] = struct{}{}
		}
	}
	if s.refreshRunning || len(s.refreshPending) == 0 {
		s.refreshMu.Unlock()
		return
	}
	s.refreshRunning = true
	s.refreshMu.Unlock()

	go func() {
		for {
			s.refreshMu.Lock()
			if len(s.refreshPending) == 0 {
				s.refreshRunning = false
				s.refreshMu.Unlock()
				return
			}
			batch := make([]int, 0, len(s.refreshPending))
			for id := range s.refreshPending {
				batch = append(batch, id)
			}
			s.refreshPending = map[int]struct{}{}
			callbacks := append([]func([]int){}, s.onUpdated...)
			s.refreshMu.Unlock()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			refreshed, _ := s.RefreshStale(ctx, batch)
			cancel()
			if len(refreshed) > 0 {
				for _, f := range callbacks {
					f(refreshed)
				}
			}
		}
	}()
}

func uniqueIDs(ids []int) []int {
	seen := make(map[int]struct{}, len(ids))
	ret := make([]int, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ret = append(ret, id)
	}
	return ret
}

// SaveSnapshots stores (or replaces) the snapshots of media (imports carry them).
func (s *Service) SaveSnapshots(ctx context.Context, media []*anilist.BaseAnime) error {
	return s.saveSnapshots(ctx, media)
}
