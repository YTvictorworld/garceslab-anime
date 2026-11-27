package core

// Garceslab Anime: proxy to an optional, self-hosted TV app updater
// (internal/tvupdates, docs/tv-updates.md).

import "seanime/internal/tvupdates"

// initTVUpdates loads the TV updater settings (<data dir>/tv-updates.json and
// SEANIME_TV_UPDATER_URL / SEANIME_TV_UPDATER_TOKEN). Nothing is contacted
// until a client asks for the status.
func (a *App) initTVUpdates() {
	a.TVUpdates = tvupdates.New(tvupdates.Options{
		DataDir: a.Config.Data.AppDataDir,
		Logger:  a.Logger,
	})
	if a.TVUpdates.Configured() {
		a.Logger.Info().Msg("tvupdates: TV updater configured")
	}
}
