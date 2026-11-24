package debrid_client

// Garceslab Anime: subtitle-aware auto-select for debrid streams (see
// internal/torrents/autoselect/track_select.go for the selection rule).

import (
	"context"
	"os"
	"seanime/internal/debrid/debrid"
	"seanime/internal/library/anime"
	"seanime/internal/torrents/autoselect"
	"seanime/internal/torrents/trackprobe"
	"strconv"
	"strings"
)

// ProbeTopEnvVar sets how many cached candidates may have their MKV header
// read (default 4; 0 turns the subtitle-aware step off).
const ProbeTopEnvVar = "SEANIME_AUTOSELECT_PROBE_TOP"

// TrackPreferenceCtxHook (profiles-hook) returns the TV player's language
// settings of the profile in ctx: preferred subtitle and audio languages and
// the subtitle labels to ignore (comma-separated, as the TV stores them). ok is
// false when the profile has none of its own.
var TrackPreferenceCtxHook func(ctx context.Context) (subtitles, audio, ignored string, ok bool)

// probeTopN reads ProbeTopEnvVar.
func probeTopN() int {
	v := strings.TrimSpace(os.Getenv(ProbeTopEnvVar))
	if v == "" {
		return autoselect.DefaultProbeTopN
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return autoselect.DefaultProbeTopN
	}
	if n > 10 {
		n = 10
	}
	return n
}

// trackPreference is the requesting profile's preference: its TV player
// settings, else the auto-select profile's preferred languages (as subtitle
// languages, Japanese left out).
func trackPreference(ctx context.Context, profile *anime.AutoSelectProfile) autoselect.TrackPreference {
	if TrackPreferenceCtxHook != nil {
		if subs, audio, ignored, ok := TrackPreferenceCtxHook(ctx); ok {
			p := autoselect.NewTrackPreference(subs, audio, ignored)
			if !p.IsEmpty() {
				return p
			}
		}
	}
	if profile == nil {
		return autoselect.TrackPreference{}
	}
	var subs []string
	for _, l := range trackprobe.ParseList(strings.Join(profile.PreferredLanguages, ",")) {
		if l != "ja" {
			subs = append(subs, l)
		}
	}
	return autoselect.TrackPreference{
		Subtitles:     subs,
		IgnoredLabels: []string{"signs", "songs"},
	}
}

// trackProbeOptions returns the subtitle-aware options for this provider, or
// nil when it's off, there is no preference or the provider can't hand out
// links to cached files.
func (r *Repository) trackProbeOptions(ctx context.Context, provider debrid.Provider, profile *anime.AutoSelectProfile, onProgress func(string)) *autoselect.TrackProbeOptions {
	topN := probeTopN()
	if topN == 0 {
		return nil
	}
	linker, ok := provider.(debrid.CachedFileLinker)
	if !ok {
		return nil
	}
	pref := trackPreference(ctx, profile)
	if pref.IsEmpty() {
		r.logger.Debug().Msg("debridstream: No subtitle or dub language preference, not checking tracks")
		return nil
	}
	return &autoselect.TrackProbeOptions{
		Preference: pref,
		Linker:     linker,
		ProviderID: provider.GetSettings().ID,
		Cache:      r.trackProbeCache,
		TopN:       topN,
		OnProgress: onProgress,
	}
}
