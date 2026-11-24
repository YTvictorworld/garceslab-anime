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
