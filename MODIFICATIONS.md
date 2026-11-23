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
