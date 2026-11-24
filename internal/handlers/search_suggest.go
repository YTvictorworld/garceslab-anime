package handlers

// Garceslab Anime: prefix typeahead suggestions from the local title index.

import (
	"seanime/internal/titleindex"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

const (
	searchSuggestDefaultLimit = 8
	searchSuggestMaxLimit     = 25
	searchSuggestMaxQuery     = 100
)

// HandleSearchSuggest
//
//	@summary returns anime that match the start of a title, for search as you type.
//	@desc Query parameters: "q" (the typed text) and "limit" (1-25, default 8).
//	@desc Matches every title and synonym of the local title index (manami-project's anime-offline-database, ODbL),
//	@desc ranked exact > prefix > word prefix > substring, then TV first and by popularity. "mediaId" is the AniList id.
//	@desc Returns an empty list while the index isn't downloaded or when it is disabled; clients then fall back to the AniList search.
//	@route /api/v1/search/suggest [GET]
//	@returns []titleindex.Suggestion
func (h *Handler) HandleSearchSuggest(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	if len(q) > searchSuggestMaxQuery {
		q = q[:searchSuggestMaxQuery]
	}
	limit := searchSuggestDefaultLimit
	if v, err := strconv.Atoi(c.QueryParam("limit")); err == nil && v > 0 {
		limit = min(v, searchSuggestMaxLimit)
	}

	var ret []titleindex.Suggestion
	if h.App != nil && h.App.TitleIndex != nil {
		ret = h.App.TitleIndex.Search(q, limit)
	}
	if ret == nil {
		ret = []titleindex.Suggestion{}
	}
	return h.RespondWithData(c, ret)
}
