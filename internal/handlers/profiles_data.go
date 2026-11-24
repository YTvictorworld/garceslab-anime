package handlers

// Garceslab Anime: per-profile data endpoints and the helpers used by the
// `profiles-hook` lines in upstream handlers (plan §3.5, §3.7.3, §3.11).
// See internal/profiles/HOOKS.md.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"seanime/internal/database/db_bridge"
	"seanime/internal/database/models"
	"seanime/internal/library/anime"
	"seanime/internal/profiles"
	"seanime/internal/profiles/importer"
	"seanime/internal/profiles/localplatform"
	"seanime/internal/profiles/settings"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// MaxImportBodyBytes caps JSON imports.
const MaxImportBodyBytes = 32 << 20

func (h *Handler) registerProfileDataRoutes(v1 *echo.Group) {
	v1.GET("/profiles/me/settings", h.HandleGetMyProfileSettings)
	v1.PATCH("/profiles/me/settings", h.HandlePatchMyProfileSettings)
	v1.GET("/profiles/me/lists", h.HandleGetMyCustomLists)
	v1.POST("/profiles/me/lists", h.HandleCreateMyCustomList)
	v1.DELETE("/profiles/me/lists/:listId", h.HandleDeleteMyCustomList)
	v1.POST("/profiles/me/lists/:listId/items/:mediaId", h.HandleAddToMyCustomList)
	v1.DELETE("/profiles/me/lists/:listId/items/:mediaId", h.HandleRemoveFromMyCustomList)
	v1.GET("/profiles/me/activity", h.HandleGetMyActivity)
	v1.POST("/profiles/:id/import/anilist", h.HandleImportProfileAnilist)
	v1.POST("/profiles/:id/import/server-account", h.HandleImportProfileServerAccount)
	v1.POST("/profiles/:id/import/json", h.HandleImportProfileJSON)
	v1.GET("/profiles/:id/export", h.HandleExportProfile)
	h.registerProfileLibraryRoutes(v1) // phase 3 (profiles_library.go)
}

// tagWebsocketProfile tags a new /events connection with the profile of its
// ?profile= token (plan §3.7.1).
func (h *Handler) tagWebsocketProfile(c echo.Context, connID string) {
	if !h.profilesRuntimeOn() {
		return
	}
	if p := profiles.FromEcho(c); p != nil {
		h.App.ProfilesRuntime.Tags.Tag(connID, p.ID)
	}
}

// requestProfileID is the profile of the request (0 without one). Used as the key of
// the per-profile package caches.
func requestProfileID(c echo.Context) int64 {
	return profiles.IDFromContext(c.Request().Context())
}

func (h *Handler) profilesRuntimeOn() bool {
	return h.App != nil && h.App.ProfilesRuntime != nil
}

// requireProfile returns the request's profile or writes 401 PROFILE_REQUIRED.
func (h *Handler) requireProfile(c echo.Context) (*profiles.Profile, error) {
	if !h.profilesRuntimeOn() {
		return nil, h.profilesUnavailable(c)
	}
	p := profiles.FromEcho(c)
	if p == nil {
		return nil, h.RespondWithStatusError(c, http.StatusUnauthorized, profiles.ErrProfileRequired)
	}
	return p, nil
}

// targetProfile resolves :id for the import/export routes (any profile may manage
// any profile, plan §3.3).
func (h *Handler) targetProfile(c echo.Context) (*profiles.Profile, error) {
	if !h.profilesRuntimeOn() {
		return nil, h.profilesUnavailable(c)
	}
	id, err := profileIDParam(c)
	if err != nil {
		return nil, h.respondProfilesError(c, err)
	}
	p, err := h.App.Profiles.Get(c.Request().Context(), id)
	if err != nil {
		return nil, h.respondProfilesError(c, err)
	}
	if p.IsDeleted() {
		return nil, h.respondProfilesError(c, profiles.ErrDeleted)
	}
	return p, nil
}

func (h *Handler) respondProfileSettingsError(c echo.Context, err error) error {
	if errors.Is(err, settings.ErrInvalidSetting) {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	return h.RespondWithError(c, err)
}

//----------------------------------------------------------------------------------------------------------------------
// Settings (hooks in settings.go)

// saveProfileSettingsFromBody moves the per-profile fields of a full settings body
// to the request's profile and restores their global values in the body.
func (h *Handler) saveProfileSettingsFromBody(c echo.Context, prev *models.Settings, lib *models.LibrarySettings, al *models.AnilistSettings) error {
	if !h.profilesRuntimeOn() {
		return nil
	}
	pid := requestProfileID(c)
	if pid <= 0 {
		return nil
	}
	ctx := c.Request().Context()
	store := h.App.ProfilesRuntime.Settings
	current, err := store.Get(ctx, pid)
	if err != nil {
		return err
	}
	next := settings.Split(prev, &models.Settings{Library: lib, Anilist: al}, current)
	return store.Save(ctx, pid, next)
}

// patchProfileSetting handles PATCH /settings/path for per-profile fields.
func (h *Handler) patchProfileSetting(c echo.Context, path string, value any) (bool, error) {
	if !h.profilesRuntimeOn() || !settings.IsProfileField(path) {
		return false, nil
	}
	pid := requestProfileID(c)
	if pid <= 0 {
		return false, nil
	}
	ctx := c.Request().Context()
	store := h.App.ProfilesRuntime.Settings
	current, err := store.Get(ctx, pid)
	if err != nil {
		return true, h.RespondWithError(c, err)
	}
	if err := current.SetPath(path, value); err != nil {
		return true, h.respondProfileSettingsError(c, err)
	}
	if err := store.Save(ctx, pid, current); err != nil {
		return true, h.respondProfileSettingsError(c, err)
	}
	// No module refresh: the per-profile values are read when they are used.
	return true, h.RespondWithData(c, h.NewStatus(c))
}

// HandleGetMyProfileSettings
//
//	@summary returns the current profile's settings (its overrides and the effective settings).
//	@route /api/v1/profiles/me/settings [GET]
//	@returns handlers.ProfileSettingsResponse
func (h *Handler) HandleGetMyProfileSettings(c echo.Context) error {
	p, err := h.requireProfile(c)
	if p == nil {
		return err
	}
	return h.respondMyProfileSettings(c, p)
}

// ProfileSettingsResponse is the current profile's settings.
type ProfileSettingsResponse struct {
	Profile             *settings.ProfileSettings `json:"profile"`
	Effective           *models.Settings          `json:"effective"`
	MarkWatchingOnStart bool                      `json:"markWatchingOnStart"`
	CompletedAction     string                    `json:"completedAction"`
}

func (h *Handler) respondMyProfileSettings(c echo.Context, p *profiles.Profile) error {
	ctx := c.Request().Context()
	st, err := h.App.ProfilesRuntime.Settings.Get(ctx, p.ID)
	if err != nil {
		return h.RespondWithError(c, err)
	}
	global, _ := h.App.Database.GetSettings()
	enabled, action := st.MarkWatching()
	return h.RespondWithData(c, &ProfileSettingsResponse{
		Profile:             st,
		Effective:           settings.Effective(global, st),
		MarkWatchingOnStart: enabled,
		CompletedAction:     action,
	})
}

// HandlePatchMyProfileSettings
//
//	@summary updates the current profile's settings. Only the fields sent change; null resets a field to the global value.
//	@route /api/v1/profiles/me/settings [PATCH]
//	@returns handlers.ProfileSettingsResponse
func (h *Handler) HandlePatchMyProfileSettings(c echo.Context) error {
	p, err := h.requireProfile(c)
	if p == nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, settings.MaxPlayerPrefsBytes*2))
	if err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(body, &patch); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, fmt.Errorf("%w: %v", settings.ErrInvalidSetting, err))
	}
	ctx := c.Request().Context()
	store := h.App.ProfilesRuntime.Settings
	current, err := store.Get(ctx, p.ID)
	if err != nil {
		return h.RespondWithError(c, err)
	}
	// Merge the top-level keys of the patch into the current JSON.
	curJSON, _ := json.Marshal(current)
	var merged map[string]json.RawMessage
	_ = json.Unmarshal(curJSON, &merged)
	if merged == nil {
		merged = map[string]json.RawMessage{}
	}
	for k, v := range patch {
		switch k {
		case "library", "anilist", "markWatchingOnStart", "completedAction", "player":
		default:
			return h.RespondWithStatusError(c, http.StatusBadRequest, fmt.Errorf("%w: unknown field %q", settings.ErrInvalidSetting, k))
		}
		if string(v) == "null" {
			delete(merged, k)
		} else {
			merged[k] = v
		}
	}
	data, _ := json.Marshal(merged)
	var next settings.ProfileSettings
	if err := json.Unmarshal(data, &next); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, fmt.Errorf("%w: %v", settings.ErrInvalidSetting, err))
	}
	if err := store.Save(ctx, p.ID, &next); err != nil {
		return h.respondProfileSettingsError(c, err)
	}
	return h.respondMyProfileSettings(c, p)
}

//----------------------------------------------------------------------------------------------------------------------
// Theme (hooks in theme.go and status.go)

func (h *Handler) profileTheme(c echo.Context) (*models.Theme, bool) {
	if !h.profilesRuntimeOn() {
		return nil, false
	}
	pid := requestProfileID(c)
	if pid <= 0 {
		return nil, false
	}
	t, found, err := h.App.ProfilesRuntime.Settings.Theme(c.Request().Context(), pid)
	if err != nil || !found {
		return nil, false
	}
	return t, true
}

func (h *Handler) saveProfileTheme(c echo.Context, t *models.Theme) (bool, error) {
	if !h.profilesRuntimeOn() {
		return false, nil
	}
	pid := requestProfileID(c)
	if pid <= 0 {
		return false, nil
	}
	if cur, ok := h.profileTheme(c); ok && t.HomeItems == nil {
		t.HomeItems = cur.HomeItems
	}
	t.ID = 1
	if err := h.App.ProfilesRuntime.Settings.SaveTheme(c.Request().Context(), pid, t); err != nil {
		return true, h.RespondWithError(c, err)
	}
	return true, h.RespondWithData(c, t)
}

//----------------------------------------------------------------------------------------------------------------------
// Torrent auto-select (hooks in torrent_search.go)

func (h *Handler) profileAutoSelect(c echo.Context) (bool, *anime.AutoSelectProfile, error) {
	if !h.profilesRuntimeOn() {
		return false, nil, nil
	}
	if pid := requestProfileID(c); pid > 0 {
		p, found, err := h.App.ProfilesRuntime.Settings.AutoSelect(c.Request().Context(), pid)
		if err != nil {
			return true, nil, err
		}
		if found {
			return true, p, nil
		}
	}
	// No profile auto-select: the global one (never the playing profile's).
	p, err := db_bridge.GetGlobalAutoSelectProfile(h.App.Database)
	return true, p, err
}

func (h *Handler) saveProfileAutoSelect(c echo.Context, p *anime.AutoSelectProfile) (bool, *anime.AutoSelectProfile, error) {
	pid := requestProfileID(c)
	if !h.profilesRuntimeOn() || pid <= 0 {
		return false, nil, nil
	}
	if err := h.App.ProfilesRuntime.Settings.SaveAutoSelect(c.Request().Context(), pid, p); err != nil {
		return true, nil, err
	}
	saved, _, err := h.App.ProfilesRuntime.Settings.AutoSelect(c.Request().Context(), pid)
	return true, saved, err
}

func (h *Handler) deleteProfileAutoSelect(c echo.Context) (bool, error) {
	pid := requestProfileID(c)
	if !h.profilesRuntimeOn() || pid <= 0 {
		return false, nil
	}
	return true, h.App.ProfilesRuntime.Settings.DeleteAutoSelect(c.Request().Context(), pid)
}

//----------------------------------------------------------------------------------------------------------------------
// Custom lists and activity

func (h *Handler) myPlatform(c echo.Context) (*localplatform.Platform, error) {
	p, err := h.requireProfile(c)
	if p == nil {
		return nil, err
	}
	return h.App.ProfilesRuntime.Router.For(p.ID), nil
}

func (h *Handler) afterListChange(c echo.Context) {
	_, _ = h.App.RefreshAnimeCollectionCtx(context.WithoutCancel(c.Request().Context()))
}

func (h *Handler) respondListError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, localplatform.ErrCustomListNotFound), errors.Is(err, localplatform.ErrMediaNotFound):
		return h.RespondWithStatusError(c, http.StatusNotFound, err)
	case errors.Is(err, localplatform.ErrInvalidListName):
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	return h.RespondWithError(c, err)
}

// HandleGetMyCustomLists
//
//	@summary returns the current profile's anime custom lists.
//	@route /api/v1/profiles/me/lists [GET]
//	@returns []localplatform.ProfileCustomList
func (h *Handler) HandleGetMyCustomLists(c echo.Context) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	lists, err := p.CustomLists(c.Request().Context())
	if err != nil {
		return h.RespondWithError(c, err)
	}
	return h.RespondWithData(c, lists)
}

// HandleCreateMyCustomList
//
//	@summary creates a custom list in the current profile.
//	@route /api/v1/profiles/me/lists [POST]
//	@returns []localplatform.ProfileCustomList
func (h *Handler) HandleCreateMyCustomList(c echo.Context) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	var b struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&b); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	if _, err := p.CreateCustomList(c.Request().Context(), b.Name); err != nil {
		return h.respondListError(c, err)
	}
	h.afterListChange(c)
	return h.HandleGetMyCustomLists(c)
}

func listParams(c echo.Context) (int64, int, error) {
	listID, err := strconv.ParseInt(c.Param("listId"), 10, 64)
	if err != nil || listID <= 0 {
		return 0, 0, localplatform.ErrCustomListNotFound
	}
	mediaID := 0
	if c.Param("mediaId") != "" {
		mediaID, err = strconv.Atoi(c.Param("mediaId"))
		if err != nil || mediaID <= 0 {
			return 0, 0, localplatform.ErrMediaNotFound
		}
	}
	return listID, mediaID, nil
}

// HandleDeleteMyCustomList
//
//	@summary deletes a custom list of the current profile (the entries stay in the list).
//	@route /api/v1/profiles/me/lists/{listId} [DELETE]
//	@param listId - int - true - "The custom list ID"
//	@returns bool
func (h *Handler) HandleDeleteMyCustomList(c echo.Context) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	listID, _, err := listParams(c)
	if err != nil {
		return h.respondListError(c, err)
	}
	if err := p.DeleteCustomList(c.Request().Context(), listID); err != nil {
		return h.respondListError(c, err)
	}
	h.afterListChange(c)
	return h.RespondWithData(c, true)
}

// HandleAddToMyCustomList
//
//	@summary adds a media of the current profile's list to one of its custom lists.
//	@route /api/v1/profiles/me/lists/{listId}/items/{mediaId} [POST]
//	@param listId - int - true - "The custom list ID"
//	@param mediaId - int - true - "The AniList media ID"
//	@returns bool
func (h *Handler) HandleAddToMyCustomList(c echo.Context) error {
	return h.setInMyCustomList(c, true)
}

// HandleRemoveFromMyCustomList
//
//	@summary removes a media from one of the current profile's custom lists.
//	@route /api/v1/profiles/me/lists/{listId}/items/{mediaId} [DELETE]
//	@param listId - int - true - "The custom list ID"
//	@param mediaId - int - true - "The AniList media ID"
//	@returns bool
func (h *Handler) HandleRemoveFromMyCustomList(c echo.Context) error {
	return h.setInMyCustomList(c, false)
}

func (h *Handler) setInMyCustomList(c echo.Context, in bool) error {
	p, err := h.myPlatform(c)
	if p == nil {
		return err
	}
	listID, mediaID, err := listParams(c)
	if err != nil {
		return h.respondListError(c, err)
	}
	if err := p.SetInCustomList(c.Request().Context(), listID, mediaID, in); err != nil {
		return h.respondListError(c, err)
	}
	h.afterListChange(c)
	return h.RespondWithData(c, true)
}

// ProfileActivity is a row of the current profile's activity log.
type ProfileActivity struct {
	ID        int64   `json:"id"`
	MediaType string  `json:"mediaType"`
	MediaID   int     `json:"mediaId"`
	Kind      string  `json:"kind"`
	Episode   *int    `json:"episode,omitempty"`
	OldValue  *string `json:"oldValue,omitempty"`
	NewValue  *string `json:"newValue,omitempty"`
	CreatedAt int64   `json:"createdAt"`
}

// HandleGetMyActivity
//
//	@summary returns the current profile's latest list activity ("recently watched", changes).
//	@route /api/v1/profiles/me/activity [GET]
//	@returns []handlers.ProfileActivity
func (h *Handler) HandleGetMyActivity(c echo.Context) error {
	p, err := h.requireProfile(c)
	if p == nil {
		return err
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	ret := make([]*ProfileActivity, 0)
	if err := h.App.Profiles.DB().WithContext(c.Request().Context()).Table("activity_log").
		Select("id, media_type, media_id, kind, episode, old_value, new_value, created_at").
		Where("profile_id = ?", p.ID).Order("created_at DESC, id DESC").Limit(limit).Scan(&ret).Error; err != nil {
		return h.RespondWithError(c, err)
	}
	return h.RespondWithData(c, ret)
}

//----------------------------------------------------------------------------------------------------------------------
// Import / export (plan §3.5)

func importMode(s string) localplatform.ImportMode {
	if strings.EqualFold(strings.TrimSpace(s), string(localplatform.ImportReplace)) {
		return localplatform.ImportReplace
	}
	return localplatform.ImportMerge
}

func (h *Handler) respondImportError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, importer.ErrInvalidUsername), errors.Is(err, importer.ErrInvalidFile), errors.Is(err, importer.ErrInvalidToken):
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	case errors.Is(err, importer.ErrListUnavailable), errors.Is(err, importer.ErrNothingToImport):
		return h.RespondWithStatusError(c, http.StatusUnprocessableEntity, err)
	}
	return h.RespondWithError(c, err)
}

func (h *Handler) afterImport(c echo.Context, p *profiles.Profile) {
	ctx := profiles.WithProfile(context.WithoutCancel(c.Request().Context()), p)
	_, _ = h.App.RefreshAnimeCollectionCtx(ctx)
}

// HandleImportProfileAnilist
//
//	@summary imports an AniList anime list (and optionally the favourites) into a profile.
//	@desc Body: {"username": string} for a public list (anonymous), or {"token": string} for a private list: the token is used for this import only and is never stored or logged. Also "mode" (merge|replace) and "favourites" (default true).
//	@route /api/v1/profiles/{id}/import/anilist [POST]
//	@param id - int - true - "The profile ID"
//	@returns localplatform.ProfileImportResult
func (h *Handler) HandleImportProfileAnilist(c echo.Context) error {
	target, err := h.targetProfile(c)
	if target == nil {
		return err
	}
	var b struct {
		Username   string `json:"username"`
		Token      string `json:"token"`
		Mode       string `json:"mode"`
		Favourites *bool  `json:"favourites"`
	}
	if err := c.Bind(&b); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	rt := h.App.ProfilesRuntime
	src := importer.AnilistSources{
		Public:           importer.ClientFetcher{Client: rt.DirectAnilist},
		PublicFavourites: importer.PublicFavourites{Query: importer.ClientQuery(rt.DirectAnilist), Logger: h.App.Logger},
		NewAccount: func(token string) (importer.AccountFetcher, error) {
			return importer.NewTokenClient(token, h.App.Logger)
		},
	}
	if profileImportSourcesForTest != nil {
		src = profileImportSourcesForTest()
	}
	res, err := importer.ImportAnilist(c.Request().Context(), rt.Router.For(target.ID), src, importer.AnilistImportOptions{
		Username:   b.Username,
		Token:      b.Token,
		Mode:       importMode(b.Mode),
		Favourites: b.Favourites == nil || *b.Favourites,
	})
	b.Token = ""
	if err != nil {
		return h.respondImportError(c, err)
	}
	h.afterImport(c, target)
	return h.RespondWithData(c, res)
}

// HandleImportProfileServerAccount
//
//	@summary imports the server's existing AniList account list (its cached collection or its public list) into a profile.
//	@desc The account's token is never used. After the import the token can be removed (plan: one-time migration).
//	@route /api/v1/profiles/{id}/import/server-account [POST]
//	@param id - int - true - "The profile ID"
//	@returns localplatform.ProfileImportResult
func (h *Handler) HandleImportProfileServerAccount(c echo.Context) error {
	target, err := h.targetProfile(c)
	if target == nil {
		return err
	}
	var b struct {
		Mode string `json:"mode"`
	}
	_ = c.Bind(&b)
	col, source, err := h.App.ServerAccountAnimeCollection(c.Request().Context())
	if err != nil {
		return h.respondImportError(c, err)
	}
	res, err := importer.ImportCollection(c.Request().Context(), h.App.ProfilesRuntime.Router.For(target.ID), col, importMode(b.Mode), source)
	if err != nil {
		return h.respondImportError(c, err)
	}
	h.afterImport(c, target)
	return h.RespondWithData(c, res)
}

// HandleImportProfileJSON
//
//	@summary imports a profile export (or an AniList-shaped anime collection) into a profile.
//	@desc Body: {"mode": "merge"|"replace", "data": <export JSON>}.
//	@route /api/v1/profiles/{id}/import/json [POST]
//	@param id - int - true - "The profile ID"
//	@returns localplatform.ProfileImportResult
func (h *Handler) HandleImportProfileJSON(c echo.Context) error {
	target, err := h.targetProfile(c)
	if target == nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, MaxImportBodyBytes+1))
	if err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	if len(body) > MaxImportBodyBytes {
		return h.RespondWithStatusError(c, http.StatusRequestEntityTooLarge, fmt.Errorf("%w: file too large", importer.ErrInvalidFile))
	}
	var b struct {
		Mode string          `json:"mode"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &b); err != nil || len(b.Data) == 0 {
		return h.RespondWithStatusError(c, http.StatusBadRequest, fmt.Errorf("%w: expected {\"mode\", \"data\"}", importer.ErrInvalidFile))
	}
	res, err := importer.ImportJSON(c.Request().Context(), h.App.Profiles.DB(), h.App.ProfilesRuntime.Router.For(target.ID), b.Data, importMode(b.Mode))
	if err != nil {
		return h.respondImportError(c, err)
	}
	h.afterImport(c, target)
	return h.RespondWithData(c, res)
}

// HandleExportProfile
//
//	@summary exports everything a profile owns as JSON (lists, custom lists, favourites, history, settings, activity).
//	@route /api/v1/profiles/{id}/export [GET]
//	@param id - int - true - "The profile ID"
//	@returns importer.ProfileExport
func (h *Handler) HandleExportProfile(c echo.Context) error {
	target, err := h.targetProfile(c)
	if target == nil {
		return err
	}
	exp, err := importer.BuildExport(c.Request().Context(), h.App.Profiles.DB(), h.App.ProfilesRuntime.Router.For(target.ID), target, time.Now())
	if err != nil {
		return h.RespondWithError(c, err)
	}
	if c.QueryParam("download") == "true" {
		c.Response().Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="garceslab-anime-profile-%d.json"`, target.ID))
	}
	return h.RespondWithData(c, exp)
}
