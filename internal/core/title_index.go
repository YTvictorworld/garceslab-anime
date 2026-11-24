package core

// Garceslab Anime: local anime title index for search suggestions
// (GET /api/v1/search/suggest, see internal/titleindex).

import (
	"path/filepath"
	"seanime/internal/titleindex"
)

// initTitleIndex loads the cached anime-offline-database from the data directory
// and refreshes it in the background (weekly). Disabled with SEANIME_TITLE_INDEX_DISABLED.
func (a *App) initTitleIndex() {
	if titleindex.Disabled() {
		a.Logger.Info().Msg("titleindex: Disabled, search suggestions fall back to AniList")
		return
	}
	a.TitleIndex = titleindex.NewManager(titleindex.Options{
		Dir:       filepath.Join(a.Config.Data.AppDataDir, "title-index"),
		Logger:    a.Logger,
		IsOffline: a.IsOffline,
	})
	a.TitleIndex.Start()
	a.AddCleanupFunction(a.TitleIndex.Stop)
}
