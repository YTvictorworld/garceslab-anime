package handlers

// Garceslab Anime: canon/filler/recap episode tags (internal/episodetags).

import (
	"seanime/internal/api/anilist"
	"seanime/internal/episodetags"
	"strconv"

	"github.com/labstack/echo/v4"
)

// HandleGetAnimeEpisodeTags
//
//	@summary returns canon/filler/recap tags for the episodes of an anime.
//	@desc Tags come from MyAnimeList data via Jikan-compatible APIs (Tenrai, jikan-edge, Jikan), looked up by the media's MAL id.
//	@desc It never waits for the network: "status" is "ready" (tags in "episodes"), "pending" (nothing cached yet, a background
//	@desc fetch is running; ask again in a few seconds) or "unavailable" (no MAL id, not on MAL, providers down, disabled or offline).
//	@desc "episodes" maps the episode number (MAL numbering = the episode list's episodeNumber) to "canon", "filler", "recap" or "mixed".
//	@desc "hasNonCanon" is true when at least one episode is not canon; clients show tags only then.
//	@desc Tags are cached in the data directory: refreshed daily while the anime airs, every 30 days once finished.
//	@param id - int - true - "AniList anime media ID"
//	@route /api/v1/anime/episode-tags/{id} [GET]
//	@returns episodetags.Response
func (h *Handler) HandleGetAnimeEpisodeTags(c echo.Context) error {
	mId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return h.RespondWithError(c, err)
	}
	if h.App == nil || h.App.EpisodeTags == nil {
		return h.RespondWithData(c, episodetags.Unavailable())
	}

	// GetAnime is cached by the platform. Any failure means "no tags", never an error.
	media, err := h.App.AnilistPlatformRef.Get().GetAnime(c.Request().Context(), mId)
	if err != nil || media == nil || media.IDMal == nil || *media.IDMal <= 0 {
		return h.RespondWithData(c, episodetags.Unavailable())
	}
	// Unknown status counts as airing (the shorter refresh interval).
	airing := media.Status == nil ||
		*media.Status == anilist.MediaStatusReleasing ||
		*media.Status == anilist.MediaStatusNotYetReleased
	return h.RespondWithData(c, h.App.EpisodeTags.Get(*media.IDMal, airing))
}
