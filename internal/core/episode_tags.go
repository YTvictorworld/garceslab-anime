package core

// Garceslab Anime: canon/filler/recap episode tags from MyAnimeList data
// (GET /api/v1/anime/episode-tags/{id}, see internal/episodetags).

import (
	"os"
	"path/filepath"
	"seanime/internal/episodetags"
)

// initEpisodeTags starts the episode tags fetcher. The cache is global (not per
// profile): one JSON file per MAL anime in <data dir>/episode-tags.
// SEANIME_EPISODE_TAGS_DISABLED=true turns it off; SEANIME_EPISODE_TAGS_PROVIDERS
// picks the providers and their order.
func (a *App) initEpisodeTags() {
	if episodetags.Disabled() {
		a.Logger.Info().Msg("episodetags: Disabled, episode cards show no canon/filler tags")
		return
	}
	providers, skipped := episodetags.ParseProviders(os.Getenv(episodetags.ProvidersEnvVar))
	if len(skipped) > 0 {
		a.Logger.Warn().Strs("skipped", skipped).Msgf("episodetags: Unknown entries in %s", episodetags.ProvidersEnvVar)
	}
	a.EpisodeTags = episodetags.NewManager(episodetags.Options{
		Dir:       filepath.Join(a.Config.Data.AppDataDir, "episode-tags"),
		Logger:    a.Logger,
		Providers: providers,
		IsOffline: a.IsOffline,
	})
	a.EpisodeTags.Start()
	a.AddCleanupFunction(a.EpisodeTags.Stop)
}
