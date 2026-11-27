package handlers

// Garceslab Anime: proxy to an optional, self-hosted TV app updater
// (internal/tvupdates, docs/tv-updates.md). The TV app and the web interface
// ask this server; only the server knows the updater's URL and token.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"seanime/internal/profiles/sessions"
	"seanime/internal/tvupdates"

	"github.com/labstack/echo/v4"
)

// ErrPlaybackActive is returned by POST /tv-updates/install while the TV may be
// playing something: installing closes the TV app.
var ErrPlaybackActive = errors.New("PLAYBACK_ACTIVE")

// tvDeviceLabel is the device label of viewer sessions started from an LG
// webOS TV (sessions.DeviceLabel).
const tvDeviceLabel = "LG TV"

func (h *Handler) registerTVUpdatesRoutes(v1 *echo.Group) {
	v1.GET("/tv-updates/status", h.HandleGetTvUpdatesStatus)
	v1.POST("/tv-updates/install", h.HandleInstallTvUpdate)
	v1.POST("/tv-updates/later", h.HandleDismissTvUpdate)
	v1.GET("/tv-updates/settings", h.HandleGetTvUpdatesSettings)
	v1.PATCH("/tv-updates/settings", h.HandleSaveTvUpdatesSettings)
}

func (h *Handler) tvUpdates() *tvupdates.Service {
	if h.App == nil {
		return nil
	}
	return h.App.TVUpdates
}

// HandleGetTvUpdatesStatus
//
//	@summary returns the TV app update status from the optional TV updater.
//	@desc Not configured: {"configured": false} (clients hide all update UI).
//	@desc Configured but the updater doesn't answer within 3 s: {"configured": true, "reachable": false, "error": "..."}.
//	@desc Otherwise: {"configured": true, "reachable": true, "status": <the updater's GET /status, unchanged>}.
//	@route /api/v1/tv-updates/status [GET]
//	@returns tvupdates.StatusResponse
func (h *Handler) HandleGetTvUpdatesStatus(c echo.Context) error {
	svc := h.tvUpdates()
	if svc == nil {
		return h.RespondWithData(c, tvupdates.StatusResponse{Configured: false})
	}
	return h.RespondWithData(c, svc.Status(c.Request().Context()))
}

// HandleInstallTvUpdate
//
//	@summary confirms the installation of the pending TV app build.
//	@desc Forwards to the updater's POST /install with the server-side token and returns {"ok", "result": <the updater's JSON>}.
//	@desc The updater queues the install (it waits for the TV if it is off) and the TV app closes while it installs.
//	@desc 409 PLAYBACK_ACTIVE while a viewer session runs on an LG TV or a playback goes through the server.
//	@desc 404 TV_UPDATER_NOT_CONFIGURED without an updater URL; 502 TV_UPDATER_UNREACHABLE when the updater doesn't answer.
//	@route /api/v1/tv-updates/install [POST]
//	@returns tvupdates.ActionResponse
func (h *Handler) HandleInstallTvUpdate(c echo.Context) error {
	if !h.tvUpdates().Configured() {
		return h.RespondWithStatusError(c, http.StatusNotFound, tvupdates.ErrNotConfigured)
	}
	if err := h.tvPlaybackActive(); err != nil {
		return h.RespondWithStatusError(c, http.StatusConflict, err)
	}
	return h.tvUpdateAction(c, h.tvUpdates().Install)
}

// HandleDismissTvUpdate
//
//	@summary dismisses the pending TV app build ("Later").
//	@desc Forwards to the updater's POST /later with the server-side token and returns {"ok", "result": <the updater's JSON>}.
//	@desc The updater offers the next build when one comes out. It also cancels an install waiting for the TV.
//	@route /api/v1/tv-updates/later [POST]
//	@returns tvupdates.ActionResponse
func (h *Handler) HandleDismissTvUpdate(c echo.Context) error {
	return h.tvUpdateAction(c, h.tvUpdates().Later)
}

func (h *Handler) tvUpdateAction(c echo.Context, f func(ctx context.Context) (tvupdates.ActionResponse, error)) error {
	res, err := f(c.Request().Context())
	switch {
	case errors.Is(err, tvupdates.ErrNotConfigured):
		return h.RespondWithStatusError(c, http.StatusNotFound, err)
	case errors.Is(err, tvupdates.ErrUnreachable):
		return h.RespondWithStatusError(c, http.StatusBadGateway, err)
	case err != nil:
		return h.RespondWithError(c, err)
	}
	return h.RespondWithData(c, res)
}

// HandleGetTvUpdatesSettings
//
//	@summary returns the TV updater settings, without the token.
//	@desc "tokenConfigured" says whether a token is set; the token itself is never returned.
//	@desc "urlFromEnv" / "tokenFromEnv": set by SEANIME_TV_UPDATER_URL / SEANIME_TV_UPDATER_TOKEN, read-only in the web.
//	@route /api/v1/tv-updates/settings [GET]
//	@returns tvupdates.SettingsView
func (h *Handler) HandleGetTvUpdatesSettings(c echo.Context) error {
	return h.RespondWithData(c, h.tvUpdates().View())
}

// HandleSaveTvUpdatesSettings
//
//	@summary saves the TV updater URL and/or token.
//	@desc An omitted field keeps its value; an empty string clears it. The token is stored on the server only.
//	@desc 400 TV_UPDATER_INVALID_SETTINGS for a bad URL; 409 TV_UPDATER_SET_BY_ENV when the value comes from an environment variable.
//	@route /api/v1/tv-updates/settings [PATCH]
//	@returns tvupdates.SettingsView
func (h *Handler) HandleSaveTvUpdatesSettings(c echo.Context) error {
	type body struct {
		URL   *string `json:"url"`
		Token *string `json:"token"`
	}
	var b body
	if err := c.Bind(&b); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, errors.New("invalid request body"))
	}
	svc := h.tvUpdates()
	if svc == nil {
		return h.RespondWithStatusError(c, http.StatusServiceUnavailable, errors.New("TV updates are not available on this server"))
	}
	view, err := svc.Update(tvupdates.SettingsPatch{URL: b.URL, Token: b.Token})
	switch {
	case errors.Is(err, tvupdates.ErrInvalidSettings):
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	case errors.Is(err, tvupdates.ErrSetByEnv):
		return h.RespondWithStatusError(c, http.StatusConflict, err)
	case err != nil:
		return h.RespondWithError(c, err)
	}
	return h.RespondWithData(c, view)
}

// tvPlaybackActive fails with PLAYBACK_ACTIVE when the TV may be playing:
//   - a viewer session (direct play, phase 2) started from an LG TV, or
//   - the relayed playback lane is in use: the server doesn't record which
//     device holds it, so any playback through the server counts.
//
// Without profiles there are no viewer sessions or lane, so the server has no
// signal and doesn't refuse (docs/tv-updates.md).
func (h *Handler) tvPlaybackActive() error {
	if h.App == nil {
		return nil
	}
	if viewers := h.App.ViewerSessions(); viewers != nil {
		for _, s := range viewers.List() {
			if s.Device == tvDeviceLabel {
				return fmt.Errorf("%w: %s is watching on the TV", ErrPlaybackActive, viewerName(s))
			}
		}
	}
	if rt := h.App.ProfilesRuntime; rt != nil {
		if p, busy := rt.Lane.ActiveOwner(); busy {
			name := "Guest"
			if p != nil && p.Name != "" {
				name = p.Name
			}
			return fmt.Errorf("%w: %s is watching through the server", ErrPlaybackActive, name)
		}
	}
	return nil
}

func viewerName(s sessions.ViewerSession) string {
	if s.ProfileName == "" {
		return "Guest"
	}
	return s.ProfileName
}
