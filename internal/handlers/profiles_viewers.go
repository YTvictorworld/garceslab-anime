package handlers

// Garceslab Anime phase 2: viewer sessions (internal/profiles/sessions,
// docs/phase2-direct-play.md). Up to SEANIME_MAX_VIEWERS (3) devices play a
// debrid link directly at the same time, each in its own session; the relayed
// playbacks (web browser, torrent streams, library files, transcoding) keep the
// single playback lane of phase 1, which takes one of the slots too.

import (
	"errors"
	"net/http"
	"seanime/internal/profiles"
	"seanime/internal/profiles/sessions"
	"strings"

	"github.com/labstack/echo/v4"
)

// registerProfileViewerRoutes adds the "Now watching" routes (Settings → Profiles).
func (h *Handler) registerProfileViewerRoutes(v1 *echo.Group) {
	v1.GET("/profiles/viewers", h.HandleGetViewerSessions)
	v1.POST("/profiles/viewers/:sessionId/stop", h.HandleStopViewerSession)
}

// ViewerSessionsResponse is the "Now watching" list.
type ViewerSessionsResponse struct {
	Max      int                      `json:"max"`
	Sessions []sessions.ViewerSession `json:"sessions"`
	// Relayed is the profile playing through the server (single lane), if any.
	Relayed *ViewerRelayedPlayback `json:"relayed,omitempty"`
}

// ViewerRelayedPlayback is the playback that holds the relayed lane.
type ViewerRelayedPlayback struct {
	ProfileID   int64  `json:"profileId"`
	ProfileName string `json:"profileName"`
}

// HandleGetViewerSessions
//
//	@summary returns who is watching right now (viewer sessions and the relayed playback).
//	@route /api/v1/profiles/viewers [GET]
//	@returns handlers.ViewerSessionsResponse
func (h *Handler) HandleGetViewerSessions(c echo.Context) error {
	viewers := h.App.ViewerSessions()
	if viewers == nil {
		return h.profilesUnavailable(c)
	}
	ret := ViewerSessionsResponse{Max: viewers.Max(), Sessions: viewers.List()}
	if rt := h.App.ProfilesRuntime; rt != nil {
		if p, busy := rt.Lane.ActiveOwner(); busy {
			r := &ViewerRelayedPlayback{ProfileName: "Guest"}
			if p != nil {
				r.ProfileID, r.ProfileName = p.ID, p.Name
			}
			ret.Relayed = r
		}
	}
	return h.RespondWithData(c, ret)
}

// HandleStopViewerSession
//
//	@summary ends a viewer session and frees its slot.
//	@desc The device is told its session ended; a debrid link still being resolved for it is cancelled.
//	@route /api/v1/profiles/viewers/{sessionId}/stop [POST]
//	@param sessionId - string - true - "The session ID"
//	@returns bool
func (h *Handler) HandleStopViewerSession(c echo.Context) error {
	viewers := h.App.ViewerSessions()
	if viewers == nil {
		return h.profilesUnavailable(c)
	}
	if _, err := viewers.ReleaseByID(strings.TrimSpace(c.Param("sessionId")), sessions.ReasonAdmin); err != nil {
		return h.RespondWithStatusError(c, http.StatusNotFound, err)
	}
	return h.RespondWithData(c, true)
}

// acquireDirectSession takes a viewing slot for a direct debrid stream of the
// requesting client. Without profiles (or viewer sessions) it does nothing.
func (h *Handler) acquireDirectSession(c echo.Context, clientID string, mediaID, episode int) error {
	viewers := h.App.ViewerSessions()
	if viewers == nil {
		return nil
	}
	req := sessions.AcquireRequest{
		ClientID: clientID,
		MediaID:  mediaID,
		Episode:  episode,
		Device:   sessions.DeviceLabel(requestPlatform(c), c.Request().Header.Get("User-Agent")),
	}
	if p := profiles.FromEcho(c); p != nil {
		req.ProfileID, req.ProfileName = p.ID, p.Name
	}
	_, err := viewers.Acquire(req)
	return err
}

// acquireRelayedLane takes the single relayed playback lane (phase 1) for the
// requesting client, and ends its direct session: the device now plays through
// the server instead.
func (h *Handler) acquireRelayedLane(c echo.Context, clientID string) error {
	rt := h.App.ProfilesRuntime
	if rt == nil {
		return nil
	}
	if err := rt.Lane.AcquireFor(profiles.FromEcho(c), clientID); err != nil {
		return err
	}
	if rt.Viewers != nil && clientID != "" {
		rt.Viewers.Release(clientID, 0, 0, sessions.ReasonNewSession)
	}
	return nil
}

// respondPlaybackSlotError answers 409 for LANES_FULL and PLAYBACK_BUSY.
func (h *Handler) respondPlaybackSlotError(c echo.Context, err error) error {
	if errors.Is(err, sessions.ErrLanesFull) || errors.Is(err, profiles.ErrPlaybackBusy) {
		return h.RespondWithStatusError(c, http.StatusConflict, err)
	}
	if errors.Is(err, sessions.ErrNoClientID) {
		return h.RespondWithStatusError(c, http.StatusBadRequest, err)
	}
	return h.RespondWithError(c, err)
}

func requestPlatform(c echo.Context) string {
	p, _ := c.Get(clientPlatformHeader).(string)
	return p
}

// viewerWebsocketConnected / viewerWebsocketDisconnected: the disconnect grace of
// a session starts when its client's last websocket closes.
func (h *Handler) viewerWebsocketConnected(clientID string) {
	if viewers := h.App.ViewerSessions(); viewers != nil {
		viewers.ClientConnected(clientID)
	}
}

func (h *Handler) viewerWebsocketDisconnected(clientID string) {
	viewers := h.App.ViewerSessions()
	if viewers == nil {
		return
	}
	for _, id := range h.App.WSEventManager.GetClientIds() {
		if id == clientID {
			return // another connection of the same client is still open
		}
	}
	viewers.ClientDisconnected(clientID)
}

// Manual progress tracking (phase 2). Upstream keeps one global tracking, so a
// second device's tracking cancelled the first one's. With profiles, a client
// that tracks manually plays the media itself (a direct player), so its tracking
// lives in its viewer session: starting it takes a slot if the client has none
// (LANES_FULL when full), events go to that client only, and cancel/sync only
// touch the requesting client's session.

// startSessionManualTracking reports handled=false without viewer sessions.
func (h *Handler) startSessionManualTracking(c echo.Context, clientID string, mediaID, episode int) (bool, error) {
	viewers := h.App.ViewerSessions()
	if viewers == nil {
		return false, nil
	}
	if clientID == "" {
		return true, sessions.ErrNoClientID
	}
	ctx := c.Request().Context()
	tracking := sessions.ViewerTracking{MediaID: mediaID, EpisodeNumber: episode}
	if col, err := h.App.GetAnimeCollectionCtx(ctx, false); err == nil && col != nil {
		if entry, found := col.GetListEntryFromAnimeId(mediaID); found && entry.GetMedia() != nil {
			tracking.MediaTitle = entry.GetMedia().GetPreferredTitle()
			tracking.TotalEpisodes = entry.GetMedia().GetTotalEpisodeCount()
			if entry.GetProgress() != nil {
				tracking.CurrentProgress = *entry.GetProgress()
			}
		}
	}
	if tracking.MediaTitle == "" {
		media, err := h.App.AnilistPlatformRef.Get().GetAnime(ctx, mediaID)
		if err != nil || media == nil {
			return true, errors.New("media not found")
		}
		tracking.MediaTitle = media.GetPreferredTitle()
		tracking.TotalEpisodes = media.GetTotalEpisodeCount()
	}

	if _, ok := viewers.Get(clientID); !ok {
		if err := h.acquireDirectSession(c, clientID, mediaID, episode); err != nil {
			return true, err
		}
	}
	if _, ok := viewers.StartTracking(clientID, tracking); !ok {
		return true, errors.New("the viewer session ended")
	}
	h.App.OnDirectSessionPlaying(ctx, mediaID, episode)
	return true, nil
}

// cancelSessionManualTracking ends the requesting client's session. A cancel
// without an episode only ends a session that is already playing: a session
// still being prepared belongs to the next episode.
func (h *Handler) cancelSessionManualTracking(c echo.Context, mediaID, episode int) bool {
	viewers := h.App.ViewerSessions()
	if viewers == nil {
		return false
	}
	clientID := getRequestClientId(c, "")
	sess, ok := viewers.Get(clientID)
	if !ok {
		return true
	}
	if mediaID <= 0 && sess.State != sessions.StatePlaying {
		return true
	}
	viewers.Release(clientID, mediaID, episode, sessions.ReasonStopped)
	return true
}

// syncSessionManualTracking saves the tracked episode as watched for the
// requesting client's profile, then ends the tracking (like upstream).
func (h *Handler) syncSessionManualTracking(c echo.Context) (int, bool, error) {
	viewers := h.App.ViewerSessions()
	if viewers == nil {
		return 0, false, nil
	}
	clientID := getRequestClientId(c, "")
	t, ok := viewers.Tracking(clientID)
	if !ok {
		return 0, false, nil // maybe a relayed playback: upstream
	}
	ctx := c.Request().Context()
	total := t.TotalEpisodes
	if err := h.App.AnilistPlatformRef.Get().UpdateEntryProgress(ctx, t.MediaID, t.EpisodeNumber, &total); err != nil {
		return 0, true, err
	}
	_, _ = h.App.RefreshAnimeCollectionCtx(ctx)
	viewers.Release(clientID, t.MediaID, t.EpisodeNumber, sessions.ReasonStopped)
	return t.MediaID, true, nil
}
