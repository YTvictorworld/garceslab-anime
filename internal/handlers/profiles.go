package handlers

// Garceslab Anime: local profiles endpoints (plan §3.11). See internal/profiles/HOOKS.md.

import (
	"errors"
	"net/http"
	"seanime/internal/profiles"
	"seanime/internal/profiles/avatars"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

// registerProfileRoutes adds the profile and avatar routes.
func (h *Handler) registerProfileRoutes(e *echo.Echo, v1 *echo.Group) {
	e.GET(avatars.URLPrefix+":file", h.HandleGetProfileAvatarFile)
	e.HEAD(avatars.URLPrefix+":file", h.HandleGetProfileAvatarFile)

	v1.GET("/avatars", h.HandleGetAvatarCatalog)

	v1.GET("/profiles", h.HandleGetProfiles)
	v1.POST("/profiles", h.HandleCreateProfile)
	v1.GET("/profiles/audit", h.HandleGetProfileAudit)
	v1.POST("/profiles/logout", h.HandleProfileLogout)
	v1.PATCH("/profiles/:id", h.HandleUpdateProfile)
	v1.DELETE("/profiles/:id", h.HandleDeleteProfile)
	v1.POST("/profiles/:id/restore", h.HandleRestoreProfile)
	v1.PUT("/profiles/:id/pin", h.HandleSetProfilePin)
	v1.POST("/profiles/:id/select", h.HandleSelectProfile)
}

// profilesMiddleware resolves the request's profile. Registered right after the
// server password middleware.
func (h *Handler) profilesMiddleware() echo.MiddlewareFunc {
	if h.App == nil || h.App.Profiles == nil {
		return func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	}
	return h.App.Profiles.Middleware(profiles.MiddlewareConfig{
		ClientID: requestClientID,
		Platform: func(c echo.Context) string {
			p, _ := c.Get(clientPlatformHeader).(string)
			return p
		},
		Reject: func(c echo.Context, err error) error {
			return h.RespondWithStatusError(c, http.StatusUnauthorized, err)
		},
	})
}

// requestClientID returns the client id resolved by the client identity middleware
// (signed header, trusted claimed header, or the HttpOnly cookie).
func requestClientID(c echo.Context) string {
	id, _ := c.Get(clientIdCookieName).(string)
	return strings.TrimSpace(id)
}

func (h *Handler) respondProfilesError(c echo.Context, err error) error {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, profiles.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, profiles.ErrNameTaken), errors.Is(err, profiles.ErrDeleted), errors.Is(err, profiles.ErrNotDeleted):
		code = http.StatusConflict
	case errors.Is(err, profiles.ErrInvalidName), errors.Is(err, profiles.ErrInvalidAvatar),
		errors.Is(err, profiles.ErrInvalidColor), errors.Is(err, profiles.ErrInvalidField),
		errors.Is(err, profiles.ErrInvalidPin), errors.Is(err, profiles.ErrNoClientID):
		code = http.StatusBadRequest
	case errors.Is(err, profiles.ErrPinRequired), errors.Is(err, profiles.ErrWrongPin):
		// 403, not 401: the web client treats 401 as "server password needed".
		code = http.StatusForbidden
	case errors.Is(err, profiles.ErrPinLocked):
		code = http.StatusTooManyRequests
		var locked *profiles.PinLockedError
		if errors.As(err, &locked) {
			if secs := locked.Until - time.Now().Unix(); secs > 0 {
				c.Response().Header().Set("Retry-After", strconv.FormatInt(secs, 10))
			}
		}
	}
	return h.RespondWithStatusError(c, code, err)
}

func profileIDParam(c echo.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, profiles.ErrNotFound
	}
	return id, nil
}

func (h *Handler) profilesUnavailable(c echo.Context) error {
	return h.RespondWithStatusError(c, http.StatusServiceUnavailable, errors.New("profiles are not available"))
}

// HandleGetAvatarCatalog
//
//	@summary returns the bundled profile avatar catalog.
//	@route /api/v1/avatars [GET]
//	@returns []avatars.Avatar
func (h *Handler) HandleGetAvatarCatalog(c echo.Context) error {
	return h.RespondWithData(c, avatars.Catalog())
}

// HandleGetProfileAvatarFile serves a bundled avatar image. Public (no server
// password), like the embedded web assets, so <img> tags work on every client.
func (h *Handler) HandleGetProfileAvatarFile(c echo.Context) error {
	data, contentType, err := avatars.ReadFile(c.Param("file"))
	if err != nil {
		return c.NoContent(http.StatusNotFound)
	}
	hdr := c.Response().Header()
	hdr.Set("Cache-Control", "public, max-age=86400")
	hdr.Set("Cross-Origin-Resource-Policy", "cross-origin")
	hdr.Set("X-Content-Type-Options", "nosniff")
	// SVGs are images only: no scripts even if opened directly.
	hdr.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	return c.Blob(http.StatusOK, contentType, data)
}

// HandleGetProfiles
//
//	@summary returns the profiles for the "Who's watching?" screen.
//	@desc Pass ?deleted=true to include the profiles in the trash (restorable for 30 days).
//	@route /api/v1/profiles [GET]
//	@returns []profiles.Profile
func (h *Handler) HandleGetProfiles(c echo.Context) error {
	if h.App.Profiles == nil {
		return h.profilesUnavailable(c)
	}
	includeDeleted, _ := strconv.ParseBool(c.QueryParam("deleted"))
	list, err := h.App.Profiles.List(c.Request().Context(), includeDeleted)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	return h.RespondWithData(c, list)
}

// HandleCreateProfile
//
//	@summary creates a profile.
//	@desc The avatar must be an id from /api/v1/avatars. The PIN is optional (4-6 digits).
//	@route /api/v1/profiles [POST]
//	@returns profiles.Profile
func (h *Handler) HandleCreateProfile(c echo.Context) error {
	if h.App.Profiles == nil {
		return h.profilesUnavailable(c)
	}
	var b profiles.CreateInput
	if err := c.Bind(&b); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	p, err := h.App.Profiles.Create(c.Request().Context(), b)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	return h.RespondWithData(c, p)
}

// HandleUpdateProfile
//
//	@summary updates a profile (name, avatar, color, order, score format, title language).
//	@desc Any profile can edit any profile (no roles).
//	@route /api/v1/profiles/{id} [PATCH]
//	@param id - int - true - "The profile ID"
//	@returns profiles.Profile
func (h *Handler) HandleUpdateProfile(c echo.Context) error {
	if h.App.Profiles == nil {
		return h.profilesUnavailable(c)
	}
	id, err := profileIDParam(c)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	var b profiles.UpdateInput
	if err := c.Bind(&b); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	p, err := h.App.Profiles.Update(c.Request().Context(), id, b)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	return h.RespondWithData(c, p)
}

// HandleDeleteProfile
//
//	@summary moves a profile to the trash.
//	@desc It can be restored for 30 days; then it is purged with all its data.
//	@route /api/v1/profiles/{id} [DELETE]
//	@param id - int - true - "The profile ID"
//	@returns bool
func (h *Handler) HandleDeleteProfile(c echo.Context) error {
	if h.App.Profiles == nil {
		return h.profilesUnavailable(c)
	}
	id, err := profileIDParam(c)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	if err := h.App.Profiles.Delete(c.Request().Context(), id); err != nil {
		return h.respondProfilesError(c, err)
	}
	if cur := profiles.FromEcho(c); cur != nil && cur.ID == id {
		clearProfileCookie(c)
	}
	return h.RespondWithData(c, true)
}

// HandleRestoreProfile
//
//	@summary restores a profile from the trash.
//	@route /api/v1/profiles/{id}/restore [POST]
//	@param id - int - true - "The profile ID"
//	@returns profiles.Profile
func (h *Handler) HandleRestoreProfile(c echo.Context) error {
	if h.App.Profiles == nil {
		return h.profilesUnavailable(c)
	}
	id, err := profileIDParam(c)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	p, err := h.App.Profiles.Restore(c.Request().Context(), id)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	return h.RespondWithData(c, p)
}

// HandleSetProfilePin
//
//	@summary sets, changes or removes (pin: null) a profile's PIN.
//	@desc Any change closes the profile's open sessions.
//	@route /api/v1/profiles/{id}/pin [PUT]
//	@param id - int - true - "The profile ID"
//	@returns bool
func (h *Handler) HandleSetProfilePin(c echo.Context) error {
	if h.App.Profiles == nil {
		return h.profilesUnavailable(c)
	}
	id, err := profileIDParam(c)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	type body struct {
		Pin *string `json:"pin"`
	}
	var b body
	if err := c.Bind(&b); err != nil {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	if err := h.App.Profiles.SetPin(c.Request().Context(), id, b.Pin); err != nil {
		return h.respondProfilesError(c, err)
	}
	return h.RespondWithData(c, true)
}

// HandleSelectProfile
//
//	@summary enters a profile on this device and returns its session token.
//	@desc Send the token in X-Seanime-Profile (or rely on the HttpOnly Seanime-Profile cookie on the web).
//	@desc It lasts 24 h at most, is bound to this client id and must not be persisted by clients.
//	@route /api/v1/profiles/{id}/select [POST]
//	@param id - int - true - "The profile ID"
//	@returns profiles.ProfileSession
func (h *Handler) HandleSelectProfile(c echo.Context) error {
	if h.App.Profiles == nil {
		return h.profilesUnavailable(c)
	}
	id, err := profileIDParam(c)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	type body struct {
		Pin string `json:"pin"`
	}
	var b body
	if c.Request().ContentLength != 0 {
		if err := c.Bind(&b); err != nil {
			return h.RespondWithStatusError(c, http.StatusBadRequest, err)
		}
	}
	platform, _ := c.Get(clientPlatformHeader).(string)
	sess, err := h.App.Profiles.Select(c.Request().Context(), id, b.Pin, requestClientID(c), platform)
	if err != nil {
		return h.respondProfilesError(c, err)
	}

	// Session cookie (no Expires): closing the browser forgets the profile, so the
	// selector is shown again on the next launch. The token itself expires in <= 24 h.
	c.SetCookie(&http.Cookie{
		Name:     profiles.CookieName,
		Value:    sess.ProfileToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestUsesTrustedHTTPS(c.Request()),
	})
	return h.RespondWithData(c, sess)
}

// HandleProfileLogout
//
//	@summary leaves the current profile on this device.
//	@route /api/v1/profiles/logout [POST]
//	@returns bool
func (h *Handler) HandleProfileLogout(c echo.Context) error {
	if h.App.Profiles == nil {
		return h.profilesUnavailable(c)
	}
	if clientID := requestClientID(c); clientID != "" {
		if err := h.App.Profiles.Logout(c.Request().Context(), clientID); err != nil {
			return h.respondProfilesError(c, err)
		}
	}
	clearProfileCookie(c)
	return h.RespondWithData(c, true)
}

// HandleGetProfileAudit
//
//	@summary returns the latest profile management actions (who did what, when).
//	@route /api/v1/profiles/audit [GET]
//	@returns []profiles.ProfileAuditEntry
func (h *Handler) HandleGetProfileAudit(c echo.Context) error {
	if h.App.Profiles == nil {
		return h.profilesUnavailable(c)
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	list, err := h.App.Profiles.AuditLog(c.Request().Context(), limit)
	if err != nil {
		return h.respondProfilesError(c, err)
	}
	return h.RespondWithData(c, list)
}

func clearProfileCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     profiles.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestUsesTrustedHTTPS(c.Request()),
	})
}

// fillProfilesStatus adds the profiles block and the current profile to /status.
func (h *Handler) fillProfilesStatus(c echo.Context, status *Status) {
	if h.App == nil || h.App.Profiles == nil || status == nil {
		return
	}
	st := h.App.Profiles.Status(c.Request().Context())
	status.Profiles = &st
	status.CurrentProfile = profiles.FromEcho(c)
}
