package plugin

import (
	"errors"
	"seanime/internal/api/anilist"
	"seanime/internal/extension"
	"seanime/internal/extension_repo/prompt"
	"seanime/internal/goja/goja_bindings"
	"seanime/internal/library/anime"
	gojautil "seanime/internal/util/goja"

	"github.com/dop251/goja"
	"github.com/rs/zerolog"
)

type Anilist struct {
	ctx       *AppContextImpl
	ext       *extension.Extension
	logger    *zerolog.Logger
	scheduler *gojautil.Scheduler
}

// BindAnilist binds the anilist API to the Goja runtime.
// Permissions need to be checked by the caller.
// Permissions needed: anilist
func (a *AppContextImpl) BindAnilist(vm *goja.Runtime, logger *zerolog.Logger, ext *extension.Extension, scheduler *gojautil.Scheduler) {
	al := &Anilist{
		ctx:       a,
		ext:       ext,
		logger:    new(logger.With().Str("id", ext.ID).Logger()),
		scheduler: scheduler,
	}
	anilistObj := getAnilistObj(vm)
	_ = anilistObj.Set("refreshAnimeCollection", al.RefreshAnimeCollection)
	_ = anilistObj.Set("refreshMangaCollection", al.RefreshMangaCollection)
	_ = anilistObj.Set("getRequestProvider", func() string {
		return anilist.CurrentRequestProviderName()
	})

	// Bind anilist platform
	anilistPlatformRef, ok := a.anilistPlatformRef.Get()
	if ok {
		_ = anilistObj.Set("updateEntry", func(mediaID int, status *anilist.MediaListStatus, scoreRaw *int, progress *int, startedAt *anilist.FuzzyDateInput, completedAt *anilist.FuzzyDateInput) error {
			return anilistPlatformRef.Get().UpdateEntry(listWriteContext(al.logger, "UpdateEntry", mediaID), mediaID, status, scoreRaw, progress, startedAt, completedAt)
		})
		_ = anilistObj.Set("updateEntryProgress", func(mediaID int, progress int, totalEpisodes *int) error {
			return anilistPlatformRef.Get().UpdateEntryProgress(listWriteContext(al.logger, "UpdateEntryProgress", mediaID), mediaID, progress, totalEpisodes)
		})
		_ = anilistObj.Set("updateEntryRepeat", func(mediaID int, repeat int) error {
			return anilistPlatformRef.Get().UpdateEntryRepeat(listWriteContext(al.logger, "UpdateEntryRepeat", mediaID), mediaID, repeat)
		})
		_ = anilistObj.Set("deleteEntry", func(mediaID int, entryId int) error {
			return anilistPlatformRef.Get().DeleteEntry(listWriteContext(al.logger, "DeleteEntry", mediaID), mediaID, entryId)
		})
		_ = anilistObj.Set("getAnimeCollection", func(bypassCache bool) (*anilist.AnimeCollection, error) {
			return anilistPlatformRef.Get().GetAnimeCollection(ProfileContext(), bypassCache)
		})
		_ = anilistObj.Set("getRawAnimeCollection", func(bypassCache bool) (*anilist.AnimeCollection, error) {
			return anilistPlatformRef.Get().GetRawAnimeCollection(ProfileContext(), bypassCache)
		})
		_ = anilistObj.Set("getMangaCollection", func(bypassCache bool) (*anilist.MangaCollection, error) {
			return anilistPlatformRef.Get().GetMangaCollection(ProfileContext(), bypassCache)
		})
		_ = anilistObj.Set("getRawMangaCollection", func(bypassCache bool) (*anilist.MangaCollection, error) {
			return anilistPlatformRef.Get().GetRawMangaCollection(ProfileContext(), bypassCache)
		})
		_ = anilistObj.Set("getAnime", func(mediaID int) (*anilist.BaseAnime, error) {
			return anilistPlatformRef.Get().GetAnime(ProfileContext(), mediaID)
		})
		_ = anilistObj.Set("getManga", func(mediaID int) (*anilist.BaseManga, error) {
			return anilistPlatformRef.Get().GetManga(ProfileContext(), mediaID)
		})
		_ = anilistObj.Set("getAnimeDetails", func(mediaID int) (*anilist.AnimeDetailsById_Media, error) {
			return anilistPlatformRef.Get().GetAnimeDetails(ProfileContext(), mediaID)
		})
		_ = anilistObj.Set("getMangaDetails", func(mediaID int) (*anilist.MangaDetailsById_Media, error) {
			return anilistPlatformRef.Get().GetMangaDetails(ProfileContext(), mediaID)
		})
		_ = anilistObj.Set("getAnimeCollectionWithRelations", func() (*anilist.AnimeCollectionWithRelations, error) {
			return anilistPlatformRef.Get().GetAnimeCollectionWithRelations(ProfileContext())
		})
		_ = anilistObj.Set("addMediaToCollection", func(mIds []int) error {
			// profiles-hook: attributed like a write of the first anime; the platform
			// refuses it without a profile.
			mediaID := 0
			if len(mIds) > 0 {
				mediaID = mIds[0]
			}
			return anilistPlatformRef.Get().AddMediaToCollection(listWriteContext(al.logger, "AddMediaToCollection", mediaID), mIds)
		})
		_ = anilistObj.Set("getStudioDetails", func(studioID int) (*anilist.StudioDetails, error) {
			return anilistPlatformRef.Get().GetStudioDetails(ProfileContext(), studioID)
		})
		_ = anilistObj.Set("listAnime", func(page *int, search *string, perPage *int, sort []*anilist.MediaSort, status []*anilist.MediaStatus, genres []*string, tags []*string, averageScoreGreater *int, season *anilist.MediaSeason, seasonYear *int, format *anilist.MediaFormat, isAdult *bool) (*anilist.ListAnime, error) {
			return anilistPlatformRef.Get().GetAnilistClient().ListAnime(ProfileContext(), page, search, perPage, sort, status, genres, tags, averageScoreGreater, season, seasonYear, format, isAdult)
		})
		_ = anilistObj.Set("listManga", func(page *int, search *string, perPage *int, sort []*anilist.MediaSort, status []*anilist.MediaStatus, genres []*string, tags []*string, averageScoreGreater *int, startDateGreater *string, startDateLesser *string, format *anilist.MediaFormat, countryOfOrigin *string, isAdult *bool) (*anilist.ListManga, error) {
			return anilistPlatformRef.Get().GetAnilistClient().ListManga(ProfileContext(), page, search, perPage, sort, status, genres, tags, averageScoreGreater, startDateGreater, startDateLesser, format, countryOfOrigin, isAdult)
		})
		_ = anilistObj.Set("listRecentAnime", func(page *int, perPage *int, airingAtGreater *int, airingAtLesser *int, notYetAired *bool) (*anilist.ListRecentAnime, error) {
			return anilistPlatformRef.Get().GetAnilistClient().ListRecentAnime(ProfileContext(), page, perPage, airingAtGreater, airingAtLesser, notYetAired)
		})
		_ = anilistObj.Set("clearCache", func() {
			anilistPlatformRef.Get().ClearCache()
			anime.ClearEpisodeCollectionCache()
			anime.ClearMissingEpisodesCache()
			anime.ClearScheduleCache()
		})
		_ = anilistObj.Set("customQuery", func(body map[string]interface{}, token string) (interface{}, error) {
			return anilist.CustomQuery(body, a.logger, token)
		})

	}

	_ = vm.Set("$anilist", anilistObj)
}

// BindAnilistCustomClient binds runtime AniList client swap APIs to the Goja runtime.
// Permissions need to be checked by the caller.
// Permissions needed: custom-client
func (a *AppContextImpl) BindAnilistCustomClient(vm *goja.Runtime, logger *zerolog.Logger, ext *extension.Extension, scheduler *gojautil.Scheduler) {
	al := &Anilist{
		ctx:       a,
		ext:       ext,
		logger:    new(logger.With().Str("id", ext.ID).Logger()),
		scheduler: scheduler,
	}
	anilistObj := getAnilistObj(vm)
	_ = anilistObj.Set("getRequestProvider", func() string {
		return anilist.CurrentRequestProviderName()
	})
	_ = anilistObj.Set("useOfficialApi", func() goja.Value {
		return al.runAction(vm, func() error {
			if err := al.ctx.ask(al.ext, prompt.Options{
				Kind:       "custom-client",
				Action:     "restore official AniList client",
				Resource:   "AniList client",
				Message:    "Allow \"" + al.ext.Name + "\" to restore the official AniList client?",
				AllowLabel: "Restore",
			}); err != nil {
				return err
			}

			if al.ctx.anilist.UseOfficialClient == nil {
				return errors.New("anilist runtime switch is not configured")
			}

			return al.ctx.anilist.UseOfficialClient()
		})
	})
	_ = anilistObj.Set("useCustomApi", func(value goja.Value) goja.Value {
		return al.runAction(vm, func() error {
			config, err := readCustomClientConfig(vm, value)
			if err != nil {
				return err
			}
			if err := al.ctx.ask(al.ext, customClientPromptOptions(al.ext, config)); err != nil {
				return err
			}

			if al.ctx.anilist.UseCustomClient == nil {
				return errors.New("anilist runtime switch is not configured")
			}

			return al.ctx.anilist.UseCustomClient(config)
		})
	})

	_ = vm.Set("$anilist", anilistObj)
}

func getAnilistObj(vm *goja.Runtime) *goja.Object {
	value := vm.Get("$anilist")
	if value != nil && !goja.IsUndefined(value) && !goja.IsNull(value) {
		return value.ToObject(vm)
	}

	obj := vm.NewObject()
	_ = vm.Set("$anilist", obj)
	return obj
}

func customClientPromptOptions(ext *extension.Extension, config anilist.CustomClientConfig) prompt.Options {
	name := config.Name
	if name == "" {
		name = anilist.CustomRequestProviderName
	}

	details := []string{"Endpoint: " + config.Endpoint}
	if config.Authenticated || config.Token != "" || len(config.Headers) > 0 {
		details = append(details, "May authenticate requests")
	}

	return prompt.Options{
		Kind:       "custom-client",
		Action:     "switch AniList client to \"" + name + "\"",
		Resource:   "AniList client",
		Message:    "Allow \"" + ext.Name + "\" to switch Seanime's AniList client to \"" + name + "\"?",
		Details:    details,
		AllowLabel: "Switch",
	}
}

func readCustomClientConfig(vm *goja.Runtime, value goja.Value) (anilist.CustomClientConfig, error) {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return anilist.CustomClientConfig{}, errors.New("anilist custom client options are required")
	}

	obj := value.ToObject(vm)
	config := anilist.CustomClientConfig{
		Name:          readString(obj, "name"),
		Endpoint:      readString(obj, "endpoint"),
		Token:         readString(obj, "token"),
		Authenticated: readBool(obj, "authenticated"),
		Headers:       readStringMap(vm, obj.Get("headers")),
	}

	return config, nil
}

func readString(obj *goja.Object, key string) string {
	value := obj.Get(key)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return ""
	}

	return value.String()
}

func readBool(obj *goja.Object, key string) bool {
	value := obj.Get(key)
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return false
	}

	return value.ToBoolean()
}

func readStringMap(vm *goja.Runtime, value goja.Value) map[string]string {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}

	obj := value.ToObject(vm)
	ret := make(map[string]string)
	for _, key := range obj.Keys() {
		ret[key] = readString(obj, key)
	}

	return ret
}

func (a *Anilist) RefreshAnimeCollection() {
	a.logger.Trace().Msg("plugin: Refreshing anime collection")
	onRefreshAnilistAnimeCollection, ok := a.ctx.onRefreshAnilistAnimeCollection.Get()
	if !ok {
		return
	}

	onRefreshAnilistAnimeCollection()
}

func (a *Anilist) RefreshMangaCollection() {
	a.logger.Trace().Msg("plugin: Refreshing manga collection")
	onRefreshAnilistMangaCollection, ok := a.ctx.onRefreshAnilistMangaCollection.Get()
	if !ok {
		return
	}

	onRefreshAnilistMangaCollection()
}

func (a *Anilist) runAction(vm *goja.Runtime, run func() error) goja.Value {
	promise, resolve, reject := vm.NewPromise()

	go func() {
		err := run()

		a.scheduler.ScheduleAsync(func() error {
			if err != nil {
				reject(goja_bindings.NewErrorString(vm, err.Error()))
				return nil
			}

			resolve(goja.Undefined())
			return nil
		})
	}()

	return vm.ToValue(promise)
}
