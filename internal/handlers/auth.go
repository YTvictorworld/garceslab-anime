package handlers

import (
	"net/http"
	"seanime/internal/core"

	"github.com/labstack/echo/v4"
)

// HandleLogin
//
//	@summary logs in the user by saving the JWT token in the database.
//	@desc This is called when the JWT token is obtained from AniList after logging in with redirection on the client.
//	@desc It also fetches the Viewer data from AniList and saves it in the database.
//	@desc It creates a new handlers.Status and refreshes App modules.
//	@route /api/v1/auth/login [POST]
//	@returns handlers.Status
func (h *Handler) HandleLogin(c echo.Context) error {

	type body struct {
		Token string `json:"token"`
	}

	var b body

	if err := c.Bind(&b); err != nil {
		return h.RespondWithError(c, err)
	}

	if h.App.ProfilesRuntime != nil { // profiles-hook: profiles never log in to AniList
		return h.RespondWithStatusError(c, http.StatusGone, core.ErrAnilistLoginDisabled)
	}

	if err := h.App.LoginToAnilist(b.Token); err != nil {
		return h.RespondWithError(c, err)
	}

	// Create a new status
	status := h.NewStatus(c)

	// Return new status
	return h.RespondWithData(c, status)

}

// HandleLogout
//
//	@summary logs out the user by removing JWT token from the database.
//	@desc It removes JWT token and Viewer data from the database.
//	@desc It creates a new handlers.Status and refreshes App modules.
//	@route /api/v1/auth/logout [POST]
//	@returns handlers.Status
func (h *Handler) HandleLogout(c echo.Context) error {
	h.App.LogoutFromAnilist()

	status := h.NewStatus(c)
	return h.RespondWithData(c, status)
}
