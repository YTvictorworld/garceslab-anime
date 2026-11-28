package profiles

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttributePlayback(t *testing.T) {
	session := func(pid int64, media int) PlaybackClaim {
		return PlaybackClaim{ProfileID: pid, MediaID: media, Source: "viewer-session"}
	}
	lane := func(pid int64, media int) PlaybackClaim {
		return PlaybackClaim{ProfileID: pid, MediaID: media, Source: "relayed-lane"}
	}
	cases := []struct {
		name    string
		claims  []PlaybackClaim
		media   int
		want    int64
		reason  AttributionReason
		matched []int64
	}{
		{"no playback", nil, 10, 0, AttributedNoPlayback, []int64{}},
		{"no playback, collection read", nil, 0, 0, AttributedNoPlayback, []int64{}},
		{"one session of that anime", []PlaybackClaim{session(1, 10)}, 10, 1, AttributedPlayback, []int64{1}},
		{"one session of another anime", []PlaybackClaim{session(1, 10)}, 11, 0, AttributedNoPlayback, []int64{}},
		{"two profiles, two anime", []PlaybackClaim{session(1, 10), session(2, 20)}, 20, 2, AttributedPlayback, []int64{2}},
		{"two profiles, same anime", []PlaybackClaim{session(1, 10), session(2, 10)}, 10, 0, AttributedAmbiguous, []int64{1, 2}},
		{"same profile on two devices", []PlaybackClaim{session(1, 10), session(1, 10)}, 10, 1, AttributedPlayback, []int64{1}},
		{"guest session", []PlaybackClaim{session(0, 10)}, 10, 0, AttributedGuest, []int64{0}},
		{"guest and profile, same anime", []PlaybackClaim{session(0, 10), session(1, 10)}, 10, 0, AttributedAmbiguous, []int64{0, 1}},
		{"collection read, one profile", []PlaybackClaim{session(1, 10)}, 0, 1, AttributedPlayback, []int64{1}},
		{"collection read, two profiles", []PlaybackClaim{session(1, 10), session(2, 20)}, 0, 0, AttributedAmbiguous, []int64{1, 2}},
		{"relayed lane, known anime", []PlaybackClaim{lane(3, 30), session(1, 10)}, 30, 3, AttributedPlayback, []int64{3}},
		{"relayed lane, unknown anime, matches any", []PlaybackClaim{lane(3, 0)}, 30, 3, AttributedPlayback, []int64{3}},
		{"relayed lane, unknown anime, with a session", []PlaybackClaim{lane(3, 0), session(1, 10)}, 10, 0, AttributedAmbiguous, []int64{1, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AttributePlayback(tc.claims, tc.media)
			require.Equal(t, tc.want, got.ProfileID)
			require.Equal(t, tc.reason, got.Reason)
			require.Equal(t, tc.matched, got.Candidates)
		})
	}
}
