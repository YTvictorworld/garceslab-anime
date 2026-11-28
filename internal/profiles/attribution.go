package profiles

import "sort"

// Playback attribution: code without a request context (plugins, hooks) acts on
// behalf of the profile that owns the playback it reacts to. A playback is owned
// by a viewer session (phase 2 direct play) or by the relayed playback lane
// (phase 1). The rule is strict: a call is attributed only when exactly one
// profile can own it, so nothing is ever written to the wrong profile.

// PlaybackClaim is one playback in progress.
type PlaybackClaim struct {
	// ProfileID owns the playback; 0 is a client without a profile (guest).
	ProfileID int64
	// MediaID is the anime being played; 0 when unknown (e.g. a relayed playback
	// still being prepared), which then matches any anime.
	MediaID int
	// Source is "viewer-session" or "relayed-lane" (logs only).
	Source string
}

// AttributionReason says how a call was attributed, or why it was not.
type AttributionReason string

const (
	// AttributedPlayback: exactly one profile owns the matching playback.
	AttributedPlayback AttributionReason = "playback"
	// AttributedNoPlayback: no playback matches; the caller falls back to the
	// relayed lane owner (late progress updates of a finished relayed playback).
	AttributedNoPlayback AttributionReason = "no-playback"
	// AttributedAmbiguous: several profiles (or a profile and a guest) own a
	// matching playback, so the call gets no profile.
	AttributedAmbiguous AttributionReason = "ambiguous"
	// AttributedGuest: the only matching playback has no profile.
	AttributedGuest AttributionReason = "guest-playback"
)

// Attribution is the result of AttributePlayback.
type Attribution struct {
	ProfileID int64
	Reason    AttributionReason
	// Candidates are the profile ids that matched (0 = guest), sorted.
	Candidates []int64
}

// AttributePlayback picks the profile a call acts for. With mediaID > 0 (a call
// about one anime, like a list write) only the playbacks of that anime (or of an
// unknown anime) count; with mediaID <= 0 (a collection read) every playback
// counts.
func AttributePlayback(claims []PlaybackClaim, mediaID int) Attribution {
	seen := map[int64]bool{}
	for _, c := range claims {
		if mediaID > 0 && c.MediaID > 0 && c.MediaID != mediaID {
			continue
		}
		if c.ProfileID < 0 {
			continue
		}
		seen[c.ProfileID] = true
	}
	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	switch {
	case len(ids) == 0:
		return Attribution{Reason: AttributedNoPlayback, Candidates: ids}
	case len(ids) > 1:
		return Attribution{Reason: AttributedAmbiguous, Candidates: ids}
	case ids[0] == 0:
		return Attribution{Reason: AttributedGuest, Candidates: ids}
	}
	return Attribution{ProfileID: ids[0], Reason: AttributedPlayback, Candidates: ids}
}
