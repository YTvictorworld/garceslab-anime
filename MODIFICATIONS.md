# Modifications

Garceslab Anime is a modified version of [Seanime](https://github.com/5rahim/seanime)
(GPL-3.0), derived from tag **v3.10.5** (commit `d4d58b22`). This file is the prominent
notice of modifications required by section 5a of the GPL-3.0. Every change is also in the
git history.

Maintained independently since 2026-11 by YTvictorworld.

## 2026-11 (base, phase 0)

- **Direct stream without a `Range` header** (`internal/directstream/httpstream.go`, with a
  test in `httpstream_test.go`): a request for a debrid/HTTP stream that doesn't send a
  `Range` header (as LG webOS's native player does) used to get the headers but no body.
  It is now proxied as a full-file request, and an empty `Range` header is no longer sent to
  the CDN.
- **Updater disabled** (`internal/updater`, `internal/handlers/releases.go`): Garceslab Anime never
  checks for or installs Seanime releases, which would replace it. "Check for updates"
  always answers that there is none, and self-update returns an error.
- **Branding:** a placeholder Garceslab Anime logo (`branding/logo.svg`, exported by
  `branding/export.sh`) replaces Seanime's logo, favicons, PWA icons, the embedded server logo
  and the Windows icon; the web UI's window/page titles and PWA manifest say "Garceslab Anime";
  the settings page's donate link points to this project's source code; the getting-started
  page credits Seanime by 5rahim.
- **Links:** the changelog and API docs the server downloads, the Discord Rich Presence button
  and the updater's GitHub fallback point to this repository instead of Seanime's.
- **Project files:** this file, `NOTICE`, a new `README.md` and `CONTRIBUTING.md`, issue and
  pull request templates (Seanime's `FUNDING.yml` and issue templates removed), a
  `Dockerfile` and `.dockerignore` (rootless image), GitHub Actions CI and Dependabot
  (`.github/`). Seanime's own release and Electron workflows were removed from
  `.github/workflows` because they publish Seanime-named builds; they remain in Seanime's
  repository.

Nothing else differs from Seanime v3.10.5 yet. Planned changes (local profiles without an
AniList account, several playbacks at the same time) will be listed here as they land.

## 2026-11 (search suggestions)

- **Prefix search suggestions** (`internal/titleindex`, `internal/core/title_index.go`,
  `internal/handlers/search_suggest.go`, route in `internal/handlers/routes.go`, public route in
  `internal/profiles/public_routes.go`): `GET /api/v1/search/suggest?q=&limit=` suggests anime
  from the first letters of a title or synonym ("ble" -> Bleach), which AniList's search can't
  do. The index is built from manami-project's anime-offline-database (ODbL), downloaded at
  runtime into the data directory and refreshed weekly; only entries with an AniList id are kept.
  Without the database the endpoint returns an empty list. The web search page's typeahead uses
  it first and falls back to AniList. `SEANIME_TITLE_INDEX_DISABLED=true` turns it off.
  Codegen gives the `titleindex` package the `TitleIndex_` prefix.

## 2026-11 (episode tags)

- **Canon / filler / recap episode tags** (`internal/episodetags`, `internal/core/episode_tags.go`,
  `internal/handlers/anime_episode_tags.go`, route in `internal/handlers/routes.go`, public route
  in `internal/profiles/public_routes.go`): `GET /api/v1/anime/episode-tags/{id}` returns
  `{status: "ready"|"pending"|"unavailable", malId, episodes: {"<n>": "canon"|"filler"|"recap"|"mixed"},
  hasNonCanon, fetchedAt, source}` for an AniList anime, from MyAnimeList data (looked up by the
  media's `idMal`) served by the Jikan-compatible APIs Tenrai, jikan-edge and Jikan. It never
  waits for the network: a cache miss queues a background fetch (all pages) and answers
  "pending". Requests go out one at a time, at least 1 s apart per API (1.5 s for jikan-edge and
  Jikan), and rotate to the next API on 429 (honouring Retry-After), 5xx or a connection failure;
  a failing API rests on an exponential cooldown (30 s doubling, capped at 30 min). The cache is
  global (one JSON file per MAL anime in `<data dir>/episode-tags`, not in the profiles
  database): daily refresh while airing, 30 days once finished. MAL episode numbers map 1:1 to
  the episode list's `episodeNumber`; anime that MAL and AniList split differently (split cours,
  absolute numbering) get shifted or missing tags. `SEANIME_EPISODE_TAGS_PROVIDERS` and
  `SEANIME_EPISODE_TAGS_DISABLED` configure it. Codegen gives the `episodetags` package the
  `EpisodeTags_` prefix. The web anime page shows a CANON / FILLER / RECAP / MIX pill on episode
  cards when the anime has at least one filler or recap episode.

## 2026-11 (profiles, phase 3: AniList-like features)

- **Favourites, full entry editor, custom list management, recently watched**
  (`internal/profiles/localplatform/{favourites,editor,customlists}.go`,
  `internal/handlers/profiles_library.go`), stored per profile on the existing tables. The web
  adds a favourite button, notes/private/custom lists in the entry editor and "Manage lists";
  stats are computed locally. See `docs/profiles-phase3.md`.
- **Private AniList import with a one-time token** and favourites import
  (`internal/profiles/importer/anilist_account.go`); the token is never stored.

## 2026-11 (profiles, phase 4: polish)

- **Player preferences per profile** (`internal/profiles/settings/player.go`,
  `internal/handlers/profiles_player.go`): `GET/PATCH /api/v1/profiles/me/player-prefs` keep
  each profile's player preferences in `profile_settings.value.player`, with a typed, range
  checked `tv` section and a `web` section (video core and media core objects). Device-only
  settings are refused. The web player (`profile-player-prefs-sync.tsx`) loads and saves them
  per profile.
- **More avatars** (`internal/profiles/avatars`): 30 more, in the CC0 styles Open Peeps,
  Pixel Art, Voxel Bot, Clay, Sprouts and Planets and the CC BY 4.0 style Adventurer (with its
  attribution in `catalog.json`, `LICENSES.md`, the README and the avatar pickers). The
  runtime check and `.github/scripts/check-avatars.go` only accept CC0 1.0 and CC BY 4.0, and
  CC BY entries need a credit.
- **`profile_audit`** (`internal/profiles/store/migrations.go` step 2, `service.go`,
  `internal/handlers/profiles_audit.go`): a `detail` column, renames ("Old → New"), changed
  fields, imports (with the source) and exports are recorded; the web's
  Settings → Profiles → Recent changes shows them.
- **Announcements off** (`internal/constants`, `internal/updater/announcement.go`, web
  `main-layout.tsx`): the upstream announcement feed is no longer fetched or shown.
- **Fan art removed:** `seanime-web/public/luffy-01.png` is replaced by the Garceslab Anime
  logo in the error and "no results" illustrations.
- **Branding:** user-facing web texts say "Garceslab Anime" instead of "Seanime" (credits,
  identifiers, storage keys and references to upstream services unchanged).
- **Docs:** `docs/self-hosting-webos.md` (Docker Compose, TV app, first run, profiles, PINs,
  optional AniList).

## 2026-11 (profiles, phase 2: up to three people watching at once)

- **Per-client debrid streams** (`internal/debrid/client/stream.go`, `repository.go`):
  direct streams (the client plays the debrid link itself: `externalPlayerLink`, `none`,
  `noneAndAwait`) get one slot per client id, with their status events sent to that client
  only; relayed streams keep upstream's single slot. A relayed start never cancels another
  client's direct stream and vice versa. The link sent to an external player is no longer
  logged. The torrent auto-select status of a direct stream goes to its client only
  (`internal/torrents/autoselect`), and its auto-select profile is the requesting profile's
  (`db_bridge.FindAutoSelectProfileCtx`).
- **Viewer sessions** (`internal/profiles/sessions`, `internal/handlers/profiles_viewers.go`,
  `internal/core/profiles.go`): at most `SEANIME_MAX_VIEWERS` (default 3) devices watch at
  once; a fourth gets `409 LANES_FULL` naming who is watching. Sessions end on stop, after
  the websocket stays closed for `SEANIME_VIEWER_DISCONNECT_GRACE` (45 s), after
  `SEANIME_VIEWER_IDLE_TIMEOUT` (15 min), from Settings → Profiles → Now watching
  (`GET /api/v1/profiles/viewers`, `POST /api/v1/profiles/viewers/:sessionId/stop`), or when
  the profile is deleted. Manual progress tracking is per session. The phase 1 playback lane
  stays for relayed playback and takes one of the slots.
- **Web:** the LANES_FULL message names who is watching; Settings → Profiles shows "Now
  watching" with Stop.
- **Tests and docs:** `internal/profiles/sessions/sessions_test.go`,
  `internal/debrid/client/stream_sessions_test.go`, `internal/handlers/profiles_viewers_test.go`,
  an end-to-end check against a throwaway server (`scripts/e2e-phase2.sh`,
  `internal/e2e/phase2`), and `docs/phase2-direct-play.md`.

## 2026-11 (subtitle-aware debrid auto-select)

- **Auto-select reads subtitle languages** (`internal/torrents/trackprobe` (new),
  `internal/torrents/autoselect/track_select.go` (new), `autoselect.go`): release names rarely
  say which subtitle languages a file has, so after the name-based choice a debrid auto-select
  reads the MKV header (EBML header, SeekHead, Tracks; attachments and clusters are skipped)
  of up to `SEANIME_AUTOSELECT_PROBE_TOP` (default 4, `0` = off) cached candidates over HTTP
  Range: at most 2 MiB and 5 s per file, 10 s in all, link requests at least 500 ms apart,
  stopped by a 429. It then prefers, among candidates with the same resolution, the
  highest-ranked one that has a full (not forced, not "Signs & Songs") subtitle track in the
  profile's preferred language (or a preferred dub language), else keeps the name-based
  choice. Results are cached per (provider, info hash, file path) in
  `<data dir>/track-probe` for 180 days. Language codes, IETF tags and names are normalised
  (`spa`, `es-419`, "Español", "Latino", "Castellano" are all Spanish). The preference comes from
  the requesting profile's TV player settings, else from its auto-select profile's languages;
  without one nothing is probed. Progress ("Checking subtitle languages… (2/4)") goes to the
  requesting client only (auto-select status, direct-stream open step, debrid stream state);
  the decision is logged at INFO without links. Torrent streams are not probed (that would
  need data from peers).
- **TorBox** (`internal/debrid/torbox/cached_link.go` (new), `torbox.go`): `CachedFileLink`
  (`debrid.CachedFileLinker`) returns a link to one file of a cached torrent, adding it with
  `add_only_if_cached` (never a download) and removing it afterwards unless it is the pick;
  429 answers are reported as `debrid.ErrRateLimited`; the download link is no longer logged.
- **Matroska** (`internal/matroska`): `FlagForced` is parsed (was always false).
- **Tests:** `internal/torrents/trackprobe/probe_test.go` (fixtures in `testdata/`,
  regenerated by `testdata/make-fixtures.sh`), `internal/torrents/autoselect/track_select_test.go`,
  `internal/debrid/torbox/cached_link_test.go`, `internal/debrid/client/track_probe_test.go`.

## 2026-11 (documentation and contribution rules)

- **Docs:** a feature overview with screenshots in `README.md`; `docs/` is the home of all
  documentation (`docs/README.md` index, new `profiles.md`, `configuration.md`,
  `subtitle-autoselect.md`, `title-search.md`, `episode-tags.md`; `self-hosting-webos.md` and
  `phase2-direct-play.md` updated); screenshots in `docs/images/` (WebP, made-up profiles).
- **Contribution rules:** `CONTRIBUTING.md` (issue first, tests with regression tests and docs
  in every PR, the "(WIP)" rule), a new `AGENTS.md`, the pull request checklist and issue
  templates (`.github/`), `scripts/check.sh` (the Go checks CI runs) and the
  `PR ready (WIP rule)` workflow (`.github/workflows/pr-ready.yml`,
  `.github/scripts/pr-ready.sh` with its tests). The existing CI workflows are unchanged.

## 2026-11 (Go dependency updates, chromedp v0.24)

- **Go dependencies:** the 28 minor/patch updates of Dependabot's `go-minor-patch` group
  (`go.mod`, `go.sum`), including `github.com/chromedp/chromedp` v0.16.0 -> v0.24.0 and
  `github.com/chromedp/cdproto` v0.160.8, whose Go API changed.
- **Extension `ChromeDP` bindings** (`internal/goja/goja_bindings/chromedp.go`): moved to
  chromedp v0.24's API (generic actions that return their value, `Do`, `Listen`, raw CDP calls
  through the action's target, events marshalled with cdproto's JSON options). The JS API that
  extensions use is unchanged. Tests: `chromedp_surface_test.go` (no browser),
  `chromedp_values_test.go` (real browser); the existing browser tests can run with
  `GARCESLAB_CHROMEDP_TESTS=1`, and three of them that had bugs in the test code (and an
  unconditional skip of the screenshot test) were fixed. See `docs/dependencies.md`.

## 2026-11 (playback manager without a media player)

- **No panic without a desktop media player** (`internal/library/playbackmanager/playback_manager.go`,
  `progress_tracking.go`, tests in `playback_manager_test.go` and `internal/playlist/manager_test.go`):
  `SetMediaPlayerRepository` stored the repository from a goroutine, and the playback manager
  dereferenced it without a nil check, so a playlist episode change (which always calls `Cancel`)
  right after start-up, or on a server whose settings have no media player section, panicked. The
  repository is now stored before the call returns, guarded by its own lock, and every use returns
  `ErrNoMediaPlayer` instead of panicking. See [docs/testing.md](docs/testing.md).
- **Playlist manager state behind a lock** (`internal/playlist/manager.go`): the current playlist,
  episode, client id and cancel function are read and written through locked getters/setters, the
  playlist goroutine keeps its own context, and `markCurrentAsCompleted` no longer unlocks a mutex it
  doesn't own. Fixes data races found with `go test -race`. The test `FakePlatform`
  (`internal/testmocks`) records update calls under a mutex.

## 2026-11 (versioning and releases)

- **Own version** (`internal/constants/constants.go`, `internal/handlers/status.go`,
  `internal/core/app.go`): Garceslab Anime has its own version, `GarceslabVersion` (starting
  at 1.0.0), returned as `garceslabVersion` in the status response and logged at startup.
  Seanime's `Version` (3.10.5) is unchanged, because extensions, Nakama, config migrations
  and the TV app compare it.
- **Release workflow** (`.github/workflows/release.yml`, `.github/scripts/release-check.sh`
  with its tests): it can be run from `main` with a version (it then creates the tag), and it
  fails with a message instead of skipping when it is run without `confirm = publish`, from
  another branch or with a version that doesn't match. See `docs/releasing.md`.

## GPL-3.0 compliance checklist

- [x] **Prominent modification notices with dates (§5a):** this file, `NOTICE`, the README's
      "Credits & License", and the git history (every change is a dated commit).
- [x] **License files untouched:** `LICENSE` is Seanime's GPL-3.0 text, unchanged. Copyright
      and license notices in the sources are kept.
- [x] **Same license, no additional restrictions:** modifications are GPL-3.0; nothing in this
      repository adds terms to it.
- [ ] **Source for every binary we distribute (§6):** every published image must be built
      from a git tag in this repository. The image carries
      `org.opencontainers.image.source` and `org.opencontainers.image.revision` labels
      (see `Dockerfile` and `.github/workflows/release.yml`), and the release notes link to
      the tag. Ticked when the first release is published.

## 2026-11 (TV app updates)

- **Proxy to an optional TV app updater** (new `internal/tvupdates`, `internal/core/tv_updates.go`,
  `internal/handlers/tv_updates.go`; `TVUpdates` field and its initialisation in
  `internal/core/app.go`, routes in `internal/handlers/routes.go`): `GET /api/v1/tv-updates/status`,
  `POST /api/v1/tv-updates/install` and `/later`, and `GET`/`PATCH /api/v1/tv-updates/settings`
  forward to a self-hosted updater with a shared token that never leaves the server
  (`SEANIME_TV_UPDATER_URL`, `SEANIME_TV_UPDATER_TOKEN`, `SEANIME_TV_UPDATER_TOKEN_FILE`, or the
  web settings). The install is refused while a TV viewer session or a relayed playback runs.
- **Web:** a "TV updates" tab in Settings (`seanime-web/src/app/(main)/settings/page.tsx`, new
  `_containers/tv-updates-settings.tsx`, `src/api/hooks/tv_updates.hooks.ts`,
  `src/lib/tv-updates/`); generated API types updated (`codegen/`, with the `tvupdates` package
  prefix in `codegen/internal/generate_structs.go`). See `docs/tv-updates.md`.

## 2026-11 (plugin list writes during direct play, #26)

- **Plugins act for the profile of the playback** (`internal/plugin/profiles.go`,
  `internal/plugin/anilist.go`, `internal/plugin/anime.go`, `internal/core/profiles.go`,
  `internal/core/modules.go`, new `internal/core/profiles_attribution.go` and
  `internal/profiles/attribution.go`): Seanime's plugin `$anilist` list writes
  (`updateEntry`, `updateEntryProgress`, `updateEntryRepeat`, `deleteEntry`,
  `addMediaToCollection`) and `ctx.anime.getAnimeEntry` now act for the one profile that plays
  that anime (a direct-play viewer session or the relayed lane); collection reads and the
  plugins' collection refresh for the one profile that plays anything. Before, they only used
  the relayed lane owner, so a plugin's writes during direct play on the TV were all refused
  with `PROFILE_REQUIRED`, and during a relayed playback of another profile they went to that
  profile. Without exactly one owner (no playback, a guest, two profiles) writes are still
  refused, with a log line naming the extension and the reason. See
  `docs/profiles-extensions.md`.
