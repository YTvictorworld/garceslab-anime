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
