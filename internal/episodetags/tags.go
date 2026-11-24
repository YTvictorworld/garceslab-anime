// Package episodetags tags anime episodes as canon, filler or recap, from
// MyAnimeList data served by Jikan-compatible APIs (Tenrai, jikan-edge, Jikan).
//
// Episode numbers are MyAnimeList's. They are mapped 1:1 to the episode list's
// episodeNumber (AniList numbering), which matches for almost every anime because
// AniList and MAL usually split seasons the same way. When they don't (a split
// cour that one site lists as two entries and the other as one, or an AniList
// entry numbered from where the previous season ended), the tags of that entry
// are shifted or missing. This package doesn't try to repair that.
package episodetags

import (
	"os"
	"strconv"
	"time"
)

// Kind is what an episode is.
type Kind string

const (
	KindCanon  Kind = "canon"
	KindFiller Kind = "filler"
	KindRecap  Kind = "recap"
	// KindMixed is a partly canon, partly filler episode. No current source
	// reports it; the type and the clients' rendering are ready for one.
	KindMixed Kind = "mixed"
)

// Status says whether a Response has data.
type Status string

const (
	// StatusReady: Episodes holds the tags (possibly stale while a refresh runs).
	StatusReady Status = "ready"
	// StatusPending: nothing cached yet; a background fetch is queued or running.
	StatusPending Status = "pending"
	// StatusUnavailable: no data for this anime (no MAL id, not on MAL, every
	// provider failed recently, the feature is disabled or the server is offline).
	StatusUnavailable Status = "unavailable"
)

// Response is what GET /api/v1/anime/episode-tags/{id} returns.
type Response struct {
	Status Status `json:"status"`
	// MalID is the MyAnimeList id the tags come from (0 when unknown).
	MalID int `json:"malId"`
	// Episodes maps an episode number (as a string, MAL numbering = the
	// episode list's episodeNumber) to its kind. Empty unless Status is "ready".
	Episodes map[string]Kind `json:"episodes"`
	// HasNonCanon is true when at least one episode is filler, recap or mixed.
	// Clients show tags only then, so an all-canon anime shows nothing.
	HasNonCanon bool `json:"hasNonCanon"`
	// FetchedAt is when the tags were fetched (RFC 3339), empty without data.
	FetchedAt string `json:"fetchedAt,omitempty"`
	// Source is the provider that served the data ("tenrai", "jikan-edge", "jikan").
	Source string `json:"source,omitempty"`
}

const (
	// DisableEnvVar turns the feature off (every response is "unavailable").
	DisableEnvVar = "SEANIME_EPISODE_TAGS_DISABLED"
	// ProvidersEnvVar sets the providers and their order, see ParseProviders.
	ProvidersEnvVar = "SEANIME_EPISODE_TAGS_PROVIDERS"
)

// Disabled reports whether the feature was turned off with DisableEnvVar.
func Disabled() bool {
	v, _ := strconv.ParseBool(os.Getenv(DisableEnvVar))
	return v
}

// kindOf classifies a source episode. A recap that is also flagged filler is
// shown as a recap.
func kindOf(filler, recap bool) Kind {
	switch {
	case recap:
		return KindRecap
	case filler:
		return KindFiller
	default:
		return KindCanon
	}
}

// Entry is the cached data for one MAL anime.
type Entry struct {
	MalID     int       `json:"malId"`
	FetchedAt time.Time `json:"fetchedAt"`
	Source    string    `json:"source,omitempty"`
	// NotFound is true when the providers answered 404 (the anime isn't on MAL).
	NotFound bool `json:"notFound,omitempty"`
	// Episodes maps the MAL episode number to its kind.
	Episodes map[int]Kind `json:"episodes"`
}

func (e *Entry) response() Response {
	if e == nil || e.NotFound {
		ret := unavailable()
		if e != nil {
			ret.MalID = e.MalID
		}
		return ret
	}
	ret := Response{
		Status:    StatusReady,
		MalID:     e.MalID,
		Episodes:  make(map[string]Kind, len(e.Episodes)),
		FetchedAt: e.FetchedAt.UTC().Format(time.RFC3339),
		Source:    e.Source,
	}
	for n, k := range e.Episodes {
		ret.Episodes[strconv.Itoa(n)] = k
		if k != KindCanon {
			ret.HasNonCanon = true
		}
	}
	return ret
}

func unavailable() Response {
	return Response{Status: StatusUnavailable, Episodes: map[string]Kind{}}
}

func pending(malID int) Response {
	return Response{Status: StatusPending, MalID: malID, Episodes: map[string]Kind{}}
}

// Unavailable is the response for an anime without data (exported for the handler).
func Unavailable() Response { return unavailable() }
