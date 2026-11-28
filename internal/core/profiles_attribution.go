package core

// Garceslab Anime: playback attribution for code without a request context
// (plugins and their hooks). See docs/profiles-extensions.md.

import (
	"context"
	"fmt"
	"seanime/internal/profiles"
	"strings"
)

// Claim sources (logs).
const (
	claimViewerSession = "viewer-session"
	claimRelayedLane   = "relayed-lane"
)

// playbackClaims lists the playbacks in progress: every viewer session (direct
// play: the device plays the debrid link itself, e.g. the TV) and the relayed
// lane while it is busy.
func (rt *ProfilesRuntime) playbackClaims() []profiles.PlaybackClaim {
	claims := make([]profiles.PlaybackClaim, 0, 4)
	if rt.Viewers != nil {
		for _, s := range rt.Viewers.List() {
			claims = append(claims, profiles.PlaybackClaim{ProfileID: s.ProfileID, MediaID: s.MediaID, Source: claimViewerSession})
		}
	}
	if owner, busy := rt.Lane.ActiveOwner(); busy {
		c := profiles.PlaybackClaim{Source: claimRelayedLane}
		if owner != nil {
			c.ProfileID = owner.ID
		}
		if rt.relayedMedia != nil {
			c.MediaID = rt.relayedMedia()
		}
		claims = append(claims, c)
	}
	return claims
}

// PlaybackProfileContext returns the context a call without a request context
// acts in: the profile that owns the playback of mediaID (mediaID <= 0: of any
// playback). Only one owner may match; with none it falls back to the relayed
// lane owner, as before (late updates of a finished relayed playback). A guest
// playback, two owners or a deleted profile give no profile, so list writes are
// refused with PROFILE_REQUIRED. The reason is for logs.
func (rt *ProfilesRuntime) PlaybackProfileContext(mediaID int) (context.Context, string) {
	bg := context.Background()
	att := profiles.AttributePlayback(rt.playbackClaims(), mediaID)
	switch att.Reason {
	case profiles.AttributedPlayback:
		p, err := rt.Service.Get(bg, att.ProfileID)
		if err != nil || p == nil || p.IsDeleted() {
			return bg, fmt.Sprintf("profile %d of the playback is not available", att.ProfileID)
		}
		return profiles.WithProfile(bg, p), fmt.Sprintf("playback of profile %d", p.ID)
	case profiles.AttributedNoPlayback:
		ctx := rt.Lane.Context(bg)
		if pid := profiles.IDFromContext(ctx); pid > 0 {
			return ctx, fmt.Sprintf("relayed lane owner %d", pid)
		}
		if mediaID > 0 {
			return ctx, "no playback of this anime"
		}
		return ctx, "no playback"
	case profiles.AttributedGuest:
		return bg, "the playback has no profile"
	default:
		ids := make([]string, 0, len(att.Candidates))
		for _, id := range att.Candidates {
			if id == 0 {
				ids = append(ids, "guest")
			} else {
				ids = append(ids, fmt.Sprint(id))
			}
		}
		return bg, "several playbacks match (profiles " + strings.Join(ids, ", ") + ")"
	}
}
