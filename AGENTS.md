# Garceslab Anime server: guide for AI coding agents

This file is for AI coding agents (and humans who like checklists). The human guide is
[CONTRIBUTING.md](CONTRIBUTING.md); the rules are the same.

## What this is

Garceslab Anime is a hard fork of **Seanime v3.10.5** (© 5rahim and contributors, GPL-3.0): a
self-hosted anime media server in Go with a React web interface (`seanime-web/`). It adds local
profiles, up to three viewers at once with direct play on LG webOS TVs, subtitle-aware
auto-select, a local title index, episode tags and more. The TV app is a separate repository
(`garceslab-anime-tv`, derived from Seanime Tenji). The code is **not our property**: keep every
copyright and license notice, and log meaningful changes to Seanime code in `MODIFICATIONS.md`.

Start with [docs/README.md](docs/README.md): it links every feature doc.

## Where things are

| Area | Path |
|---|---|
| Profiles (store, PINs, tokens, middleware, importer, local platform, avatars) | `internal/profiles/`, hooks listed in `internal/profiles/HOOKS.md` |
| Viewer sessions (three slots) | `internal/profiles/sessions/`, `internal/handlers/profiles_viewers.go` |
| Debrid streams per client | `internal/debrid/client/` |
| Subtitle-aware auto-select | `internal/torrents/trackprobe/`, `internal/torrents/autoselect/track_select.go` |
| Title index | `internal/titleindex/`, `internal/handlers/search_suggest.go` |
| Episode tags | `internal/episodetags/`, `internal/handlers/anime_episode_tags.go` |
| HTTP routes | `internal/handlers/routes.go` (+ `profiles*.go`) |
| Web interface | `seanime-web/src/` |
| Generated API types (web and TV) | `codegen/` (run it after changing handlers) |
| Docs | `docs/` (index: `docs/README.md`) |

## The workflow you must follow

1. **Issue first.** Every change has an issue; the PR description says `Closes #N`.

   If your change covers only part of an issue, don't write `Part of #N` (the PR ready check
   doesn't accept it, and `Closes #N` would close the whole issue too early). Use a
   **sub-issue**: create one under the parent issue for exactly your scope (GitHub → the parent
   issue → Sub-issues → Create sub-issue, or ask a maintainer), and write `Closes #<sub-issue>`
   in the PR. The parent issue closes when all its sub-issues are done.

2. **Tests in every PR, including regression tests.** A bug fix adds a test that fails before
   the fix and passes after it (run it both ways). A feature adds tests that lock its behaviour.
3. **Docs in every PR**, in `./docs` (new doc → add it to `docs/README.md`).
4. **The "(WIP)" rule.** Open the PR as a **draft** with a title starting with `(WIP)`. Only when
   every item of the checklist in `.github/pull_request_template.md` is ticked and every CI job
   is green may you remove `(WIP)` and mark it ready. The `PR ready (WIP rule)` workflow
   (`.github/workflows/pr-ready.yml`, logic in `.github/scripts/pr-ready.sh`) fails otherwise.
   Never tick a box you didn't verify; write "n/a" next to a box that doesn't apply.
5. **The PR title is a Conventional Commit** (`type(scope): description`, imperative, English,
   no `(WIP)` once ready). PRs are **squash-merged** only, so the title becomes the single
   commit on `main` (GitHub appends ` (#N)`); the `PR ready` check validates it. Allowed
   types: `feat`, `fix`, `docs`, `test`, `ci`, `build`, `chore`, `refactor`, `perf`, `style`,
   `revert`, `deps`. Commits inside the PR can be WIP; to resolve conflicts, merge `main` into
   the branch (no rebase or force-push needed). Details:
   [CONTRIBUTING.md](CONTRIBUTING.md#merging-squash-only).
6. Don't push, merge or open PRs unless the maintainer asked you to.

## Running the checks locally

```sh
# Go: build, vet, test (the regression suite, as CI runs it)
scripts/check.sh                                   # whole module
scripts/check.sh ./internal/profiles/...           # vet + test the touched packages (also run the whole suite before "ready")

# Without Go installed: the official image
mkdir -p ~/.cache/garceslab-go
docker run --rm --dns 1.1.1.1 -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -e GOCACHE=/tmp/go/cache -e GOMODCACHE=/tmp/go/mod \
  -v "$PWD:/src" -v "$HOME/.cache/garceslab-go:/tmp/go" -w /src \
  golang:1.27.1 scripts/check.sh

# Web (when seanime-web changed)
cd seanime-web && npm ci && npm test && npm run build

# The WIP rule's own tests (when .github/scripts/pr-ready.sh changed)
bash .github/scripts/pr-ready_test.sh

# The release rules (when .github/workflows/release.yml or release-check.sh changed)
bash .github/scripts/release-check_test.sh
```

Tests in `.github/scripts/known-failing-go-tests.txt` already fail on a clean Seanime v3.10.5
checkout and are skipped (e.g. `TestUsesPrivilegedCommandSettings`). Everything else must pass.
If you fix one of them, remove it from the list in the same PR.

## Screenshots for UI changes

Every UI change needs **BEFORE and AFTER** screenshots in the PR (and in `docs/images/` when they
document a feature).

1. Start a **throwaway local server**, never the maintainer's or any production server:
   ```sh
   docker build -t garceslab-anime-server:dev .
   docker run --rm -d --name ga-scratch -p 127.0.0.1:43299:43211 \
     -e SEANIME_SERVER_HOST=0.0.0.0 -e SEANIME_SERVER_PORT=43211 \
     -e SEANIME_PROFILES_ENFORCE=true garceslab-anime-server:dev
   ```
   Create made-up profiles (Alex, Sam, Kids, …). Don't log in to real accounts (AniList,
   TorBox) on it; anonymous AniList metadata is fine.
2. Capture with headless Chromium, e.g. a one-shot page:
   ```sh
   chromium --headless=new --hide-scrollbars --window-size=1920,1080 \
     --screenshot=after.png http://127.0.0.1:43299/
   ```
   For screens behind clicks ("Who's watching?", settings), drive the page with Playwright or
   the DevTools protocol at a 1920×1080 viewport.
3. Take BEFORE from the base branch and AFTER from yours, with the same data and viewport.
4. For the TV app's screens, follow the TV repository's AGENTS.md (`npm run serve:webos` +
   headless Chromium at 1920×1080).
5. **Privacy check every image** (open it): no real names, IP addresses, server URLs, emails,
   tokens, time zones or paths with user names. Strip metadata and compress:
   `magick in.png -strip -resize '1600x1600>' -quality 78 docs/images/name.webp`.
   `docker rm -f ga-scratch` when done.

## Privacy and safety

- Never commit secrets, `config.toml`, `*.db`, logs, real IPs, hostnames, emails or tokens.
  Use `192.0.2.x` (documentation range) or `<server>` in examples. CI runs gitleaks.
- Debrid links are secrets: never log or print them (the code already avoids it).
- Don't touch production servers, the maintainer's devices or TVs.

## Conventions

- Go: `gofmt`; comments explain why; tests next to the package (`*_test.go`).
- Web: match the surrounding style; user-facing text says "Garceslab Anime", not "Seanime"
  (credits and identifiers stay).
- Commits: [Conventional Commits](https://www.conventionalcommits.org/) (`feat(profiles): …`,
  `fix(debrid): …`, `docs: …`, `test: …`), one topic each. What reaches `main` is the PR
  title (squash merge), so that is the one that must be right.
- Upstream links (Seanime, Seanime Tenji) only in credits and attribution.
