# Contributing to Garceslab Anime

Thanks for helping! Garceslab Anime is a small, self-hosted project derived from Seanime
(GPL-3.0). Contributions of any size are welcome, and **AI-assisted contributions are welcome
too**: there are no special rules about using AI tools, the same checks apply to every change.
AI coding agents: read [AGENTS.md](AGENTS.md).

The TV app has its own repository and guide:
[garceslab-anime-tv](https://github.com/YTvictorworld/garceslab-anime-tv/blob/main/CONTRIBUTING.md).

## The rules in short

1. **Every change starts with an Issue.** Describe the bug or the idea first
   ([bug report](https://github.com/YTvictorworld/garceslab-anime-server/issues/new?template=bug_report.yml),
   [feature request](https://github.com/YTvictorworld/garceslab-anime-server/issues/new?template=feature_request.yml)).
2. **Then open a Pull Request that references it** with `Closes #N` (or `Fixes #N`,
   `Resolves #N`) in the description.

   If your change covers only part of an issue, don't write `Part of #N` (the PR ready check
   doesn't accept it, and `Closes #N` would close the whole issue too early). Use a
   **sub-issue**: create one under the parent issue for exactly your scope (GitHub → the parent
   issue → Sub-issues → Create sub-issue, or ask a maintainer), and write `Closes #<sub-issue>`
   in the PR. The parent issue closes when all its sub-issues are done.
3. **Every PR adds or updates tests**, including **regression tests**:
   - a **bug fix** includes a test that **fails before the fix and passes after** it;
   - a **feature** includes tests that **lock its behaviour**.
4. **Every PR documents what it changes in [`./docs`](docs/README.md)**: a new doc or an update
   to an existing one (and a line in the [docs index](docs/README.md) for a new doc).
5. **A PR is "ready" only when every check below passes.** If even one is failing or missing,
   the title starts with **`(WIP)`** and the PR stays a **draft**.

## When a PR is ready

The [pull request template](.github/pull_request_template.md) has this checklist. Tick every
box; for one that doesn't apply (e.g. screenshots for a change without UI), write "n/a" next to
it and tick it.

- [ ] **Linked issue:** `Closes #N` in the description.
- [ ] **Tests:** added or updated, with a regression test for a bug fix and tests that lock the
      behaviour of a feature.
- [ ] **Go checks pass** (see [Running the checks](#running-the-checks)): `go build ./...`,
      `go vet` on the touched packages, `go test` on the touched packages **and** the whole
      regression suite.
- [ ] **Web checks pass** if `seanime-web` changed: `npm run build` (includes the typecheck) and
      `npm test` in `seanime-web/`.
- [ ] **UI changes:** BEFORE and AFTER screenshots attached to the PR (1920×1080 for the TV,
      the usual desktop size for the web), and saved under `docs/images/` when they document a
      feature. Screenshots contain **no private data** (see below).
- [ ] **Docs** updated in `./docs`.
- [ ] **No secrets or private data** (IP addresses, server URLs, tokens, passwords, emails,
      real names) in code, commits or images.
- [ ] `MODIFICATIONS.md` updated if the change modifies code that comes from Seanime.

### The "(WIP)" check

The **PR ready (WIP rule)** workflow ([`.github/workflows/pr-ready.yml`](.github/workflows/pr-ready.yml))
runs on every PR and every edit of its title or description. If the title does **not** start
with `(WIP)` and any of these is true, it fails and labels the PR `wip`:

- no linked issue in the description (`Closes|Fixes|Resolves #N`);
- a checklist box is not ticked;
- a required CI job (Go, Web, Secrets, Docker image) failed or hasn't finished.

With a `(WIP)` title it passes and lists what is still missing. When everything is green,
remove `(WIP)` and mark the PR ready for review. The rule itself is in
[`.github/scripts/pr-ready.sh`](.github/scripts/pr-ready.sh), with tests in
`pr-ready_test.sh` (`bash .github/scripts/pr-ready_test.sh`).

## Running the checks

### Go (server)

[`scripts/check.sh`](scripts/check.sh) runs exactly what CI runs: `go build ./...`, `go vet` and
`go test`, skipping only the tests in
[`.github/scripts/known-failing-go-tests.txt`](.github/scripts/known-failing-go-tests.txt)
(they already fail on a clean Seanime v3.10.5 checkout, e.g. `TestUsesPrivilegedCommandSettings`,
and stay listed as known until fixed). With Go installed:

```sh
scripts/check.sh                                         # the whole regression suite
scripts/check.sh ./internal/profiles/... ./internal/handlers/   # vet + test the touched packages
```

Without Go, run it in the official image (Go 1.27.1, the version in `go.mod`):

```sh
mkdir -p ~/.cache/garceslab-go
docker run --rm --dns 1.1.1.1 -u "$(id -u):$(id -g)" -e HOME=/tmp \
  -e GOCACHE=/tmp/go/cache -e GOMODCACHE=/tmp/go/mod \
  -v "$PWD:/src" -v "$HOME/.cache/garceslab-go:/tmp/go" -w /src \
  golang:1.27.1 scripts/check.sh
```

Run the whole suite before marking a PR ready, even if you only touched one package.

### Web interface

```sh
cd seanime-web
npm ci
npm test          # vitest
npm run build     # typecheck + production build
```

### Image and end-to-end

- Docker image: `docker build -t garceslab-anime-server .`
- Viewer sessions end to end (throwaway server, no real debrid account): see
  [docs/phase2-direct-play.md](docs/phase2-direct-play.md#tests).
- From source with the web UI: [DEVELOPMENT_AND_BUILD.md](DEVELOPMENT_AND_BUILD.md) (build
  `seanime-web`, copy `seanime-web/out` to `web/`, then `go build`).
- API types for the web UI and the TV app are generated from the Go handlers (`codegen/`).

## Screenshots

- Use a **throwaway local server** with made-up profiles (Alex, Sam, Kids, …), never your own
  server or a production one.
- Check every image before you commit it: no real names, IP addresses, server URLs, emails,
  tokens, time zones or file paths with user names. Anime covers and banners from AniList are
  fine; don't add fan art.
- Save them as WebP (or compressed PNG), at most about 1600 px wide, without metadata, e.g.
  `magick shot.png -strip -resize '1600x1600>' -quality 78 docs/images/shot.webp`.

## Commits and branches

- Branch from `main`: `feature/<name>`, `fix/<name>` or `docs/<name>`.
- [Conventional Commits](https://www.conventionalcommits.org/) are suggested:
  `feat(profiles): …`, `fix(directstream): …`, `docs: …`, `test: …`. Explain the why in the
  body.
- Never commit secrets, `config.toml`, databases (`*.db`), logs or fixtures with real user data.
  CI runs gitleaks on the whole history.

## License and notices

Garceslab Anime is derived from Seanime (© 5rahim and contributors) and is not our property.
Keep every existing copyright and license notice. Code that comes from Seanime and is changed in
a meaningful way gets an entry in [MODIFICATIONS.md](MODIFICATIONS.md) (GPL-3.0 section 5a).
By contributing you agree that your contribution is licensed under the GPL-3.0, like the rest
of the project.

Please don't report Garceslab Anime issues to Seanime or Seanime Tenji.
