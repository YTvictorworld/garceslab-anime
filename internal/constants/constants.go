package constants

import (
	"seanime/internal/util"
	"time"
)

// GarceslabVersion is Garceslab Anime's own version (semver). Releases are tagged
// v<GarceslabVersion>, and the server and the TV app are released together with the
// same version; the release workflow refuses a tag that doesn't match it
// (docs/releasing.md).
//
// Version below stays Seanime's: extensions check it against their semver
// constraints, Nakama, config migrations and the TV app (MIN_SERVER_VERSION 3.8.0)
// compare it, so it must not become 1.x.
const GarceslabVersion = "1.0.0"

const (
	Version              = "3.10.5"
	VersionName          = "Saisei"
	GcTime               = time.Minute * 30
	ConfigFileName       = "config.toml"
	MalClientId          = "51cb4294feb400f3ddc66a30f9b9a00f"
	DiscordApplicationId = "1224777421941899285"
	AnilistApiUrl        = "https://graphql.anilist.co"
)

const (
	SeanimeRoomsApiUrl   = "https://seanime.app/api/rooms"
	SeanimeRoomsApiWsUrl = "wss://seanime.app/api/rooms"
	SeanimeRoomsVersion  = "1.0.0"
)

var DefaultExtensionMarketplaceURL = util.Decode("aHR0cHM6Ly9yYXcuZ2l0aHVidXNlcmNvbnRlbnQuY29tLzVyYWhpbS9zZWFuaW1lLWV4dGVuc2lvbnMvcmVmcy9oZWFkcy9tYWluL21hcmtldHBsYWNlLmpzb24=")

// AnnouncementURL is empty in Garceslab Anime: upstream's announcement feed is
// written for Seanime users (its releases, its servers), so the fork doesn't
// fetch it. An empty URL turns announcements off (updater.FetchAnnouncements).
var AnnouncementURL = ""
var InternalMetadataURL = util.Decode("aHR0cHM6Ly9hbmltZS5jbGFwLmluZw==")
