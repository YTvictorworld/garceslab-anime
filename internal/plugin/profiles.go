package plugin

// Garceslab Anime (profiles-hook): plugins are global and act on behalf of the
// profile that owns the playback they react to (see internal/profiles/HOOKS.md
// and docs/profiles-extensions.md). They have no request context, so core
// resolves the profile from the playbacks in progress: the viewer sessions
// (direct play, e.g. the TV) and the relayed playback lane. Without exactly one
// owner, reads are anonymous/empty and list writes fail with PROFILE_REQUIRED.

import (
	"context"
	"seanime/internal/profiles"

	"github.com/rs/zerolog"
)

// ProfileContext returns the context for the plugins' platform calls that are
// not about one anime (collection reads, refresh). Set by core.
var ProfileContext = func() context.Context { return context.Background() }

// ProfileContextFor returns the context for a plugin call about one anime (list
// writes, entry reads): the profile that owns the playback of mediaID. reason
// says how it was resolved (logs). Set by core.
var ProfileContextFor = func(mediaID int) (ctx context.Context, reason string) {
	return ProfileContext(), ""
}

// listWriteContext is ProfileContextFor for a list write: when no profile can
// own the write, it is logged here (with the extension and the reason) and the
// platform refuses it with PROFILE_REQUIRED.
func listWriteContext(logger *zerolog.Logger, op string, mediaID int) context.Context {
	ctx, reason := ProfileContextFor(mediaID)
	if profiles.IDFromContext(ctx) <= 0 && logger != nil {
		logger.Warn().Str("op", op).Int("mediaId", mediaID).Str("reason", reason).
			Msg("plugin: List write has no profile to act for (no playback of this anime owned by exactly one profile), refused")
	}
	return ctx
}
