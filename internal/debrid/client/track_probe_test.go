package debrid_client

import (
	"context"
	"seanime/internal/library/anime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTrackPreference_Sources(t *testing.T) {
	prev := TrackPreferenceCtxHook
	t.Cleanup(func() { TrackPreferenceCtxHook = prev })

	// The profile's TV player settings win.
	TrackPreferenceCtxHook = func(ctx context.Context) (string, string, string, bool) {
		return "spa, es, spanish", "jpn, ja", "", true
	}
	p := trackPreference(context.Background(), &anime.AutoSelectProfile{PreferredLanguages: []string{"French"}})
	assert.Equal(t, []string{"es"}, p.Subtitles)
	assert.Empty(t, p.Dub)

	// No TV settings: the auto-select profile's languages, Japanese left out.
	TrackPreferenceCtxHook = func(ctx context.Context) (string, string, string, bool) { return "", "", "", false }
	p = trackPreference(context.Background(), &anime.AutoSelectProfile{PreferredLanguages: []string{"jp", "Spanish, spa"}})
	assert.Equal(t, []string{"es"}, p.Subtitles)

	// Nothing at all: empty (no probing).
	assert.True(t, trackPreference(context.Background(), &anime.AutoSelectProfile{}).IsEmpty())
	assert.True(t, trackPreference(context.Background(), nil).IsEmpty())
}

func TestProbeTopN(t *testing.T) {
	t.Setenv(ProbeTopEnvVar, "")
	assert.Equal(t, 4, probeTopN())
	t.Setenv(ProbeTopEnvVar, "0")
	assert.Equal(t, 0, probeTopN())
	t.Setenv(ProbeTopEnvVar, "2")
	assert.Equal(t, 2, probeTopN())
	t.Setenv(ProbeTopEnvVar, "nope")
	assert.Equal(t, 4, probeTopN())
	t.Setenv(ProbeTopEnvVar, "99")
	assert.Equal(t, 10, probeTopN())
}
