package handlers

// Garceslab Anime (phase 4): player preferences per profile (plan §3.7.3).
// The TV app and the web player load them when a profile is selected and save
// their changes back, so the preferences follow the profile between devices.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"seanime/internal/profiles/settings"

	"github.com/labstack/echo/v4"
)

func (h *Handler) registerProfilePlayerRoutes(v1 *echo.Group) {
	v1.GET("/profiles/me/player-prefs", h.HandleGetMyPlayerPrefs)
	v1.PATCH("/profiles/me/player-prefs", h.HandlePatchMyPlayerPrefs)
}

// HandleGetMyPlayerPrefs
//
//	@summary returns the current profile's player preferences (one section per client: tv, web).
//	@desc Empty sections mean the profile has not saved any yet: the client keeps its local values.
//	@route /api/v1/profiles/me/player-prefs [GET]
//	@returns settings.PlayerPrefs
func (h *Handler) HandleGetMyPlayerPrefs(c echo.Context) error {
	p, err := h.requireProfile(c)
	if p == nil {
		return err
	}
	prefs, err := h.App.ProfilesRuntime.Settings.PlayerPrefs(c.Request().Context(), p.ID)
	if err != nil {
		return h.RespondWithError(c, err)
	}
	return h.RespondWithData(c, prefs)
}

// HandlePatchMyPlayerPrefs
//
//	@summary updates the current profile's player preferences.
//	@desc Only the sections and fields sent change. "tv": the fields sent replace the saved ones. "web": videoCore and mediaCore are replaced as a whole. A section set to null is cleared.
//	@route /api/v1/profiles/me/player-prefs [PATCH]
//	@returns settings.PlayerPrefs
func (h *Handler) HandlePatchMyPlayerPrefs(c echo.Context) error {
	// The request body (for the generated client types); the handler reads it
	// as a patch so that null can clear a section.
	type body struct {
		TV  *settings.TVPlayerPrefs  `json:"tv,omitempty"`
		Web *settings.WebPlayerPrefs `json:"web,omitempty"`
	}

	p, err := h.requireProfile(c)
	if p == nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request().Body, settings.MaxPlayerPrefsBytes+1))
	if err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	if len(raw) > settings.MaxPlayerPrefsBytes {
		return h.RespondWithStatusError(c, http.StatusRequestEntityTooLarge, fmt.Errorf("%w: player preferences are too large", settings.ErrInvalidSetting))
	}
	var patch settings.PlayerPrefsPatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, fmt.Errorf("%w: %v", settings.ErrInvalidSetting, err))
	}
	prefs, err := h.App.ProfilesRuntime.Settings.PatchPlayerPrefs(c.Request().Context(), p.ID, patch)
	if err != nil {
		return h.respondProfileSettingsError(c, err)
	}
	return h.RespondWithData(c, prefs)
}
