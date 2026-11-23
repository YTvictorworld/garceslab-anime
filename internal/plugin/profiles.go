package plugin

// Garceslab Anime (profiles-hook): plugins are global and act on behalf of the
// profile of the context (see internal/profiles/HOOKS.md). They have no request
// context, so their platform calls use the profile that owns the playback lane;
// without one, reads are anonymous/empty and list writes fail.

import "context"

// ProfileContext returns the context for the plugins' platform calls. Set by core.
var ProfileContext = func() context.Context { return context.Background() }
