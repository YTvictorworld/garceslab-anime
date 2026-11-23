// Package profilestest holds test helpers shared by the profiles packages: a fake
// anonymous AniList client backed by synthetic fixtures (no real user data).
package profilestest

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"seanime/internal/api/anilist"
	"strings"
	"sync"

	"github.com/gqlgo/gqlgenc/clientv2"
	"github.com/rs/zerolog"
)

//go:embed fixtures/*.json
var fixtures embed.FS

// FixtureUserName is the only public AniList user the fake client knows.
const FixtureUserName = "fixture-user"

// Fixture media ids (see fixtures/media.json).
const (
	MediaAlpha   = 1001 // TV, FINISHED, 12 episodes
	MediaBeta    = 1002 // TV, RELEASING, unknown episode count
	MediaGamma   = 1003 // MOVIE, FINISHED, 1 episode
	MediaDelta   = 1004 // TV, FINISHED, 24 episodes
	MediaEpsilon = 1005 // OVA, FINISHED, 2 episodes
	MediaZeta    = 1006 // TV, NOT_YET_RELEASED, 13 episodes
)

// ErrViolation is returned (and reported) when the fake receives a token or a
// mutation: the profiles code must only ever use AniList anonymously.
var ErrViolation = errors.New("profilestest: anonymous AniList client received a token or a mutation")

// Reporter is the subset of testing.TB the fake needs.
type Reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// FakeAnilist is an anonymous AniList client. Every mutation, viewer query or
// token use fails the test.
type FakeAnilist struct {
	t     Reporter
	mu    sync.Mutex
	media map[int]*anilist.BaseAnime
	calls map[string]int
	// Fail makes every query fail (AniList down).
	Fail bool
}

func NewFakeAnilist(t Reporter) *FakeAnilist {
	f := &FakeAnilist{t: t, media: map[int]*anilist.BaseAnime{}, calls: map[string]int{}}
	for _, m := range FixtureMedia() {
		f.media[m.ID] = m
	}
	return f
}

// FixtureMedia returns fresh copies of the synthetic media.
func FixtureMedia() []*anilist.BaseAnime {
	data, err := fixtures.ReadFile("fixtures/media.json")
	if err != nil {
		panic(err)
	}
	var ret []*anilist.BaseAnime
	if err := json.Unmarshal(data, &ret); err != nil {
		panic(err)
	}
	return ret
}

// FixtureAnimeCollectionJSON returns the raw synthetic AniList AnimeCollection response.
func FixtureAnimeCollectionJSON() []byte {
	data, err := fixtures.ReadFile("fixtures/anime_collection.json")
	if err != nil {
		panic(err)
	}
	return data
}

// FixtureAnimeCollection returns the synthetic AniList collection of FixtureUserName.
func FixtureAnimeCollection() *anilist.AnimeCollection {
	var ret anilist.AnimeCollection
	if err := json.Unmarshal(FixtureAnimeCollectionJSON(), &ret); err != nil {
		panic(err)
	}
	return &ret
}

// SetMedia adds or replaces a media (e.g. to simulate a metadata update).
func (f *FakeAnilist) SetMedia(m *anilist.BaseAnime) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.media[m.ID] = m
}

// Calls returns how many times a method was called.
func (f *FakeAnilist) Calls(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *FakeAnilist) record(method string) error {
	f.mu.Lock()
	f.calls[method]++
	fail := f.Fail
	f.mu.Unlock()
	if fail {
		return errors.New("profilestest: AniList is down")
	}
	return nil
}

func (f *FakeAnilist) violation(what string) error {
	if f.t != nil {
		f.t.Helper()
		f.t.Errorf("%v: %s", ErrViolation, what)
	}
	return fmt.Errorf("%w: %s", ErrViolation, what)
}

func (f *FakeAnilist) get(id int) (*anilist.BaseAnime, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.media[id]
	if !ok {
		return nil, fmt.Errorf("profilestest: media %d not found", id)
	}
	// copy, like a real response
	data, _ := json.Marshal(m)
	var cp anilist.BaseAnime
	_ = json.Unmarshal(data, &cp)
	return &cp, nil
}

var _ anilist.AnilistClient = (*FakeAnilist)(nil)

func (f *FakeAnilist) IsAuthenticated() bool { return false }
func (f *FakeAnilist) GetCacheDir() string   { return "" }

func (f *FakeAnilist) CustomQuery(body []byte, logger *zerolog.Logger, token ...string) (interface{}, error) {
	for _, t := range token {
		if t != "" {
			return nil, f.violation("CustomQuery with a token")
		}
	}
	var req struct {
		Query     string `json:"query"`
		Variables struct {
			IDs     []int `json:"ids"`
			PerPage *int  `json:"perPage"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if strings.HasPrefix(strings.TrimSpace(req.Query), "mutation") {
		return nil, f.violation("mutation in a custom query")
	}
	if req.Query != anilist.SearchBaseAnimeByIdsDocument {
		return nil, errors.New("profilestest: unsupported custom query")
	}
	ptrs := make([]*int, 0, len(req.Variables.IDs))
	for i := range req.Variables.IDs {
		ptrs = append(ptrs, &req.Variables.IDs[i])
	}
	res, err := f.SearchBaseAnimeByIds(context.Background(), ptrs, nil, req.Variables.PerPage, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	// Like the real client: the decoded "data" object.
	data, _ := json.Marshal(res)
	var out any
	_ = json.Unmarshal(data, &out)
	return out, nil
}

func (f *FakeAnilist) AnimeCollection(ctx context.Context, userName *string, interceptors ...clientv2.RequestInterceptor) (*anilist.AnimeCollection, error) {
	if err := f.record("AnimeCollection"); err != nil {
		return nil, err
	}
	if userName == nil || *userName == "" {
		return nil, f.violation("AnimeCollection without a user name (viewer query)")
	}
	if *userName != FixtureUserName {
		return nil, errors.New("Private User") // what AniList answers for private/unknown lists
	}
	return FixtureAnimeCollection(), nil
}

func (f *FakeAnilist) AnimeCollectionTags(ctx context.Context, userName *string, interceptors ...clientv2.RequestInterceptor) (*anilist.AnimeCollectionTags, error) {
	if userName == nil || *userName == "" {
		return nil, f.violation("AnimeCollectionTags without a user name")
	}
	return &anilist.AnimeCollectionTags{}, nil
}

func (f *FakeAnilist) AnimeCollectionWithRelations(ctx context.Context, userName *string, interceptors ...clientv2.RequestInterceptor) (*anilist.AnimeCollectionWithRelations, error) {
	if userName == nil || *userName == "" {
		return nil, f.violation("AnimeCollectionWithRelations without a user name")
	}
	return &anilist.AnimeCollectionWithRelations{}, nil
}

func (f *FakeAnilist) BaseAnimeByMalID(ctx context.Context, id *int, interceptors ...clientv2.RequestInterceptor) (*anilist.BaseAnimeByMalID, error) {
	if err := f.record("BaseAnimeByMalID"); err != nil {
		return nil, err
	}
	if id == nil {
		return nil, errors.New("nil id")
	}
	m, err := f.get(*id - 50000)
	if err != nil {
		return nil, err
	}
	return &anilist.BaseAnimeByMalID{Media: m}, nil
}

func (f *FakeAnilist) BaseAnimeByID(ctx context.Context, id *int, interceptors ...clientv2.RequestInterceptor) (*anilist.BaseAnimeByID, error) {
	if err := f.record("BaseAnimeByID"); err != nil {
		return nil, err
	}
	if id == nil {
		return nil, errors.New("nil id")
	}
	m, err := f.get(*id)
	if err != nil {
		return nil, err
	}
	return &anilist.BaseAnimeByID{Media: m}, nil
}

func (f *FakeAnilist) SearchBaseAnimeByIds(ctx context.Context, ids []*int, page *int, perPage *int, status []*anilist.MediaStatus, inCollection *bool, sort []*anilist.MediaSort, season *anilist.MediaSeason, year *int, genre *string, format *anilist.MediaFormat, interceptors ...clientv2.RequestInterceptor) (*anilist.SearchBaseAnimeByIds, error) {
	if err := f.record("SearchBaseAnimeByIds"); err != nil {
		return nil, err
	}
	if inCollection != nil {
		return nil, f.violation("SearchBaseAnimeByIds with inCollection (needs a viewer)")
	}
	if len(ids) > 50 {
		return nil, fmt.Errorf("profilestest: SearchBaseAnimeByIds got %d ids, AniList pages hold 50", len(ids))
	}
	ret := &anilist.SearchBaseAnimeByIds{Page: &anilist.SearchBaseAnimeByIds_Page{}}
	for _, id := range ids {
		if id == nil {
			continue
		}
		if m, err := f.get(*id); err == nil {
			ret.Page.Media = append(ret.Page.Media, m)
		}
	}
	return ret, nil
}

func (f *FakeAnilist) CompleteAnimeByID(ctx context.Context, id *int, interceptors ...clientv2.RequestInterceptor) (*anilist.CompleteAnimeByID, error) {
	if err := f.record("CompleteAnimeByID"); err != nil {
		return nil, err
	}
	m, err := f.get(*id)
	if err != nil {
		return nil, err
	}
	data, _ := json.Marshal(m)
	var cm anilist.CompleteAnime
	_ = json.Unmarshal(data, &cm)
	return &anilist.CompleteAnimeByID{Media: &cm}, nil
}

func (f *FakeAnilist) AnimeDetailsByID(ctx context.Context, id *int, interceptors ...clientv2.RequestInterceptor) (*anilist.AnimeDetailsByID, error) {
	if err := f.record("AnimeDetailsByID"); err != nil {
		return nil, err
	}
	if _, err := f.get(*id); err != nil {
		return nil, err
	}
	return &anilist.AnimeDetailsByID{Media: &anilist.AnimeDetailsById_Media{ID: *id}}, nil
}

func (f *FakeAnilist) ListAnime(ctx context.Context, page *int, search *string, perPage *int, sort []*anilist.MediaSort, status []*anilist.MediaStatus, genres []*string, tags []*string, averageScoreGreater *int, season *anilist.MediaSeason, seasonYear *int, format *anilist.MediaFormat, isAdult *bool, interceptors ...clientv2.RequestInterceptor) (*anilist.ListAnime, error) {
	if err := f.record("ListAnime"); err != nil {
		return nil, err
	}
	return &anilist.ListAnime{}, nil
}

func (f *FakeAnilist) ListRecentAnime(ctx context.Context, page *int, perPage *int, airingAtGreater *int, airingAtLesser *int, notYetAired *bool, interceptors ...clientv2.RequestInterceptor) (*anilist.ListRecentAnime, error) {
	if err := f.record("ListRecentAnime"); err != nil {
		return nil, err
	}
	return &anilist.ListRecentAnime{}, nil
}

func (f *FakeAnilist) UpdateMediaListEntry(ctx context.Context, mediaID *int, status *anilist.MediaListStatus, scoreRaw *int, progress *int, startedAt *anilist.FuzzyDateInput, completedAt *anilist.FuzzyDateInput, interceptors ...clientv2.RequestInterceptor) (*anilist.UpdateMediaListEntry, error) {
	return nil, f.violation("UpdateMediaListEntry mutation")
}

func (f *FakeAnilist) UpdateMediaListEntryProgress(ctx context.Context, mediaID *int, progress *int, status *anilist.MediaListStatus, interceptors ...clientv2.RequestInterceptor) (*anilist.UpdateMediaListEntryProgress, error) {
	return nil, f.violation("UpdateMediaListEntryProgress mutation")
}

func (f *FakeAnilist) UpdateMediaListEntryRepeat(ctx context.Context, mediaID *int, repeat *int, interceptors ...clientv2.RequestInterceptor) (*anilist.UpdateMediaListEntryRepeat, error) {
	return nil, f.violation("UpdateMediaListEntryRepeat mutation")
}

func (f *FakeAnilist) DeleteEntry(ctx context.Context, mediaListEntryID *int, interceptors ...clientv2.RequestInterceptor) (*anilist.DeleteEntry, error) {
	return nil, f.violation("DeleteEntry mutation")
}

func (f *FakeAnilist) MangaCollection(ctx context.Context, userName *string, interceptors ...clientv2.RequestInterceptor) (*anilist.MangaCollection, error) {
	if userName == nil || *userName == "" {
		return nil, f.violation("MangaCollection without a user name (viewer query)")
	}
	return &anilist.MangaCollection{}, nil
}

func (f *FakeAnilist) MangaCollectionTags(ctx context.Context, userName *string, interceptors ...clientv2.RequestInterceptor) (*anilist.MangaCollectionTags, error) {
	if userName == nil || *userName == "" {
		return nil, f.violation("MangaCollectionTags without a user name")
	}
	return &anilist.MangaCollectionTags{}, nil
}

func (f *FakeAnilist) SearchBaseManga(ctx context.Context, page *int, perPage *int, sort []*anilist.MediaSort, search *string, status []*anilist.MediaStatus, interceptors ...clientv2.RequestInterceptor) (*anilist.SearchBaseManga, error) {
	return &anilist.SearchBaseManga{}, nil
}

func (f *FakeAnilist) BaseMangaByID(ctx context.Context, id *int, interceptors ...clientv2.RequestInterceptor) (*anilist.BaseMangaByID, error) {
	return nil, errors.New("profilestest: no manga fixtures")
}

func (f *FakeAnilist) MangaDetailsByID(ctx context.Context, id *int, interceptors ...clientv2.RequestInterceptor) (*anilist.MangaDetailsByID, error) {
	return nil, errors.New("profilestest: no manga fixtures")
}

func (f *FakeAnilist) ListManga(ctx context.Context, page *int, search *string, perPage *int, sort []*anilist.MediaSort, status []*anilist.MediaStatus, genres []*string, tags []*string, averageScoreGreater *int, startDateGreater *string, startDateLesser *string, format *anilist.MediaFormat, countryOfOrigin *string, isAdult *bool, interceptors ...clientv2.RequestInterceptor) (*anilist.ListManga, error) {
	return &anilist.ListManga{}, nil
}

func (f *FakeAnilist) ViewerStats(ctx context.Context, interceptors ...clientv2.RequestInterceptor) (*anilist.ViewerStats, error) {
	return nil, f.violation("ViewerStats (needs a viewer token)")
}

func (f *FakeAnilist) StudioDetails(ctx context.Context, id *int, interceptors ...clientv2.RequestInterceptor) (*anilist.StudioDetails, error) {
	if err := f.record("StudioDetails"); err != nil {
		return nil, err
	}
	return &anilist.StudioDetails{}, nil
}

func (f *FakeAnilist) GetViewer(ctx context.Context, interceptors ...clientv2.RequestInterceptor) (*anilist.GetViewer, error) {
	return nil, f.violation("GetViewer (needs a viewer token)")
}

func (f *FakeAnilist) AnimeAiringSchedule(ctx context.Context, ids []*int, season *anilist.MediaSeason, seasonYear *int, previousSeason *anilist.MediaSeason, previousSeasonYear *int, nextSeason *anilist.MediaSeason, nextSeasonYear *int, interceptors ...clientv2.RequestInterceptor) (*anilist.AnimeAiringSchedule, error) {
	if err := f.record("AnimeAiringSchedule"); err != nil {
		return nil, err
	}
	return &anilist.AnimeAiringSchedule{}, nil
}

func (f *FakeAnilist) AnimeAiringScheduleRaw(ctx context.Context, ids []*int, interceptors ...clientv2.RequestInterceptor) (*anilist.AnimeAiringScheduleRaw, error) {
	if err := f.record("AnimeAiringScheduleRaw"); err != nil {
		return nil, err
	}
	return &anilist.AnimeAiringScheduleRaw{}, nil
}
