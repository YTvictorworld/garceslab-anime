package handlers

// Garceslab Anime phase 3: AniList-like features stored per profile (plan §3.11):
// favourites, the full entry editor, custom list rename/reorder and "recently
// watched". See internal/profiles/HOOKS.md.

import (
	"errors"
	"net/http"
	"seanime/internal/profiles/importer"
	"seanime/internal/profiles/localplatform"
	"strconv"

	"github.com/labstack/echo/v4"
)

// profileImportSourcesForTest replaces the AniList readers of the import endpoint
// in tests (fake AniList, no network, no real token).
var profileImportSourcesForTest func() importer.AnilistSources

func (h *Handler) registerProfileLibraryRoutes(v1 *echo.Group) {
	v1.GET("/profiles/me/favourites", h.HandleGetMyFavourites)
	v1.POST("/profiles/me/favourites/:kind/:refId", h.HandleAddMyFavourite)
	v1.DELETE("/profiles/me/favourites/:kind/:refId", h.HandleRemoveMyFavourite)
	v1.PATCH("/profiles/me/lists/:listId", h.HandleUpdateMyCustomList)
	v1.GET("/profiles/me/entries/:mediaId", h.HandleGetMyListEntry)
	v1.PATCH("/profiles/me/entries/:mediaId", h.HandleEditMyListEntry)
	v1.GET("/profiles/me/recently-watched", h.HandleGetMyRecentlyWatched)
}

func (h *Handler) respondLibraryError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, localplatform.ErrInvalidFavouriteKind), errors.Is(err, localplatform.ErrInvalidEntry),
		errors.Is(err, localplatform.ErrTooManyFavourites), errors.Is(err, localplatform.ErrCustomSourceUnsupported):
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	case errors.Is(err, localplatform.ErrDuplicateListName):
		return h.RespondWithStatusError(c, http.StatusConflict, err)
	}
	return h.respondListError(c, err)
}

func mediaIDParam(c echo.Context, name string) (int, error) {
	id, err := strconv.Atoi(c.Param(name))
	if err != nil || id <= 0 {
		return 0, localplatform.ErrMediaNotFound
	}
	return id, nil
}

//----------------------------------------------------------------------------------------------------------------------
// Favourites

// HandleGetMyFavourites
//
//	@summary returns the current profile's favourites (anime favourites carry their media).
//	@desc Optional query `kind`: ANIME, MANGA, CHARACTER, STAFF or STUDIO.
//	@route /api/v1/profiles/me/favourites [GET]
//	@returns []localplatform.ProfileFavourite
func (h *Handler) HandleGetMyFavourites(c echo.Context) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	favs, err := p.Favourites(c.Request().Context(), c.QueryParam("kind"))
	if err != nil {
		return h.respondLibraryError(c, err)
	}
	return h.RespondWithData(c, favs)
}

// HandleAddMyFavourite
//
//	@summary adds a favourite to the current profile.
//	@route /api/v1/profiles/me/favourites/{kind}/{refId} [POST]
//	@param kind - string - true - "ANIME, MANGA, CHARACTER, STAFF or STUDIO"
//	@param refId - int - true - "The AniList ID"
//	@returns bool
func (h *Handler) HandleAddMyFavourite(c echo.Context) error {
	return h.setMyFavourite(c, true)
}

// HandleRemoveMyFavourite
//
//	@summary removes a favourite from the current profile.
//	@route /api/v1/profiles/me/favourites/{kind}/{refId} [DELETE]
//	@param kind - string - true - "ANIME, MANGA, CHARACTER, STAFF or STUDIO"
//	@param refId - int - true - "The AniList ID"
//	@returns bool
func (h *Handler) HandleRemoveMyFavourite(c echo.Context) error {
	return h.setMyFavourite(c, false)
}

func (h *Handler) setMyFavourite(c echo.Context, add bool) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	refID, err := mediaIDParam(c, "refId")
	if err != nil {
		return h.respondLibraryError(c, err)
	}
	ctx := c.Request().Context()
	if add {
		err = p.AddFavourite(ctx, c.Param("kind"), refID)
	} else {
		err = p.RemoveFavourite(ctx, c.Param("kind"), refID)
	}
	if err != nil {
		return h.respondLibraryError(c, err)
	}
	return h.RespondWithData(c, true)
}

//----------------------------------------------------------------------------------------------------------------------
// Custom lists

// HandleUpdateMyCustomList
//
//	@summary renames and/or moves one of the current profile's custom lists.
//	@desc Body: {"name"?: string, "position"?: int (0-based)}.
//	@route /api/v1/profiles/me/lists/{listId} [PATCH]
//	@param listId - int - true - "The custom list ID"
//	@returns []localplatform.ProfileCustomList
func (h *Handler) HandleUpdateMyCustomList(c echo.Context) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	listID, _, err := listParams(c)
	if err != nil {
		return h.respondLibraryError(c, err)
	}
	var b struct {
		Name     *string `json:"name"`
		Position *int    `json:"position"`
	}
	if err := c.Bind(&b); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	if err := p.UpdateCustomList(c.Request().Context(), listID, b.Name, b.Position); err != nil {
		return h.respondLibraryError(c, err)
	}
	h.afterListChange(c)
	return h.HandleGetMyCustomLists(c)
}

//----------------------------------------------------------------------------------------------------------------------
// Entry editor

// HandleGetMyListEntry
//
//	@summary returns the current profile's list entry for a media with every editable field (null if it isn't in the list).
//	@route /api/v1/profiles/me/entries/{mediaId} [GET]
//	@param mediaId - int - true - "The AniList media ID"
//	@returns localplatform.ProfileEntryDetails
func (h *Handler) HandleGetMyListEntry(c echo.Context) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	mediaID, err := mediaIDParam(c, "mediaId")
	if err != nil {
		return h.respondLibraryError(c, err)
	}
	details, err := p.EntryDetails(c.Request().Context(), mediaID)
	if errors.Is(err, localplatform.ErrMediaNotFound) {
		return h.RespondWithData(c, nil)
	}
	if err != nil {
		return h.respondLibraryError(c, err)
	}
	return h.RespondWithData(c, details)
}

// HandleEditMyListEntry
//
//	@summary edits (or adds) the current profile's list entry for a media: status, score, progress, dates, rewatches, private, notes and custom lists.
//	@desc Only the fields sent change. A date with null parts clears it, "" clears the notes. A new entry is PLANNING unless a status is sent.
//	@route /api/v1/profiles/me/entries/{mediaId} [PATCH]
//	@param mediaId - int - true - "The AniList media ID"
//	@returns localplatform.ProfileEntryDetails
func (h *Handler) HandleEditMyListEntry(c echo.Context) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	mediaID, err := mediaIDParam(c, "mediaId")
	if err != nil {
		return h.respondLibraryError(c, err)
	}
	var edit localplatform.ProfileEntryEdit
	if err := c.Bind(&edit); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	ctx := c.Request().Context()
	if err := p.EditEntry(ctx, mediaID, edit); err != nil {
		return h.respondLibraryError(c, err)
	}
	h.afterListChange(c)
	details, err := p.EntryDetails(ctx, mediaID)
	if err != nil {
		return h.respondLibraryError(c, err)
	}
	return h.RespondWithData(c, details)
}

//----------------------------------------------------------------------------------------------------------------------
// Recently watched

// HandleGetMyRecentlyWatched
//
//	@summary returns the media the current profile watched most recently (latest position, list status and media).
//	@desc Optional query `limit` (default 20, max 200).
//	@route /api/v1/profiles/me/recently-watched [GET]
//	@returns []localplatform.ProfileRecentlyWatched
func (h *Handler) HandleGetMyRecentlyWatched(c echo.Context) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	ret, err := p.RecentlyWatched(c.Request().Context(), limit)
	if err != nil {
		return h.RespondWithError(c, err)
	}
	return h.RespondWithData(c, ret)
}
