package profiles

import "strings"

// Routes reachable without a profile when enforcement is on (§3.2): the selector and
// first-run setup, status, avatars, the public (anonymous AniList) catalog and stream
// URLs. Everything else touches user data and requires a profile.
//
// Keys are "METHOD route-template". Stream routes are matched by prefix below.
var publicRoutes = map[string]bool{
	"GET /api/v1/status":                       true,
	"GET /api/v1/profiles":                     true,
	"POST /api/v1/profiles":                    true, // first-run "Create your profile"
	"POST /api/v1/profiles/:id/select":         true,
	"POST /api/v1/profiles/logout":             true,
	"GET /api/v1/avatars":                      true,
	"GET /api/v1/image-proxy":                  true,
	"GET /api/v1/anilist/media-details/:id":    true,
	"GET /api/v1/anilist/studio-details/:id":   true,
	"POST /api/v1/anilist/list-anime":          true,
	"POST /api/v1/anilist/list-recent-anime":   true,
	"GET /api/v1/anilist/cache-layer/status":   true,
	"GET /api/v1/search/suggest":               true, // title suggestions from the local index (no user data)
	"GET /api/v1/anime/episode-collection/:id": true, // public episode metadata
	"GET /events":                              true, // the websocket handler authenticates itself
}

// Stream URLs are fetched by players that can't send headers; they keep using the
// server password HMAC (?token=) and, from phase 2, ?lane=.
var publicPrefixes = []string{
	"/api/v1/proxy",
	"/api/v1/directstream/stream",
	"/api/v1/directstream/att/",
	"/api/v1/mediastream/transcode/",
	"/api/v1/mediastream/subs/",
	"/api/v1/mediastream/att/",
	"/api/v1/mediastream/direct",
	"/api/v1/mediastream/file",
	"/api/v1/torrentstream/stream/",
	"/api/v1/nakama/ws", // Nakama peers authenticate with their own password
	"/api/v1/nakama/host/",
	"/api/v1/nakama/stream",
}

// IsPublicRoute reports whether a route can be used without a profile.
func IsPublicRoute(method, route string) bool {
	if method == "HEAD" {
		method = "GET"
	}
	if publicRoutes[method+" "+route] {
		return true
	}
	for _, p := range publicPrefixes {
		if strings.HasPrefix(route, p) {
			return true
		}
	}
	return false
}
