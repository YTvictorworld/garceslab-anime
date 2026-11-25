# Releasing

How Garceslab Anime is versioned and how a release is cut. The TV app's side is in the TV
repository's [docs/releasing.md](https://github.com/YTvictorworld/garceslab-anime-tv/blob/main/docs/releasing.md);
both are released together.

## Versions

- Garceslab Anime has its **own version**, starting at **1.0.0**, following
  [semantic versioning](https://semver.org/): `MAJOR.MINOR.PATCH`, no pre-release suffix.
  - PATCH: fixes only; MINOR: new features that keep working with the previous TV app and
    server; MAJOR: a change that needs the server and the TV app to be updated together.
- Releases are git tags **`vX.Y.Z`**. A tag is never moved or reused: a broken release is
  fixed by a new version.
- **The server and the TV app are released together, with the same version**: server `v1.0.0`
  goes with TV `v1.0.0`. If only one of them changed, both still get the new version, so the
  pair to install is always obvious.
- In the server, the version is `GarceslabVersion` in
  [`internal/constants/constants.go`](../internal/constants/constants.go). It is shown as
  `garceslabVersion` in the status response (`GET /api/v1/status`), logged at startup
  ("Garceslab Anime 1.0.0 (based on Seanime 3.10.5-…)") and set as the image's
  `org.opencontainers.image.version` label.
- `Version` in the same file stays **Seanime's version (3.10.5)** and must not be changed to
  1.x: extensions declare semver constraints against it, Nakama and the config migrations
  compare it, and the TV app refuses servers whose `version` is below 3.8.0
  ("Server update required"). A test (`internal/constants/version_test.go`) locks both.
- `CHANGELOG.md` is Seanime's changelog: the web interface shows its entries by Seanime
  version, so Garceslab Anime's release notes live on
  [GitHub Releases](https://github.com/YTvictorworld/garceslab-anime-server/releases)
  instead.

## Who and when

- Only the **maintainer** cuts releases (the workflow needs write access to the repository).
- Only **after the full TV test**: the exact `main` commits of both repositories have been
  built and tested together on a real LG TV (install the `.ipk` built from TV `main`, run the
  image built from server `main`, go through the features in [README.md](README.md)).
- Both repositories, same version, one after the other: **server first**, then the TV app.

## Cutting a release

1. **Bump the version** (skip for the very first release, 1.0.0, which is already set):
   open an issue and a pull request in each repository that changes the version (server:
   `GarceslabVersion`; TV: `package.json`, `package-lock.json` and `app.config.ts`, see the TV
   doc). Merge both.
2. **Server:** GitHub → *Actions* → **Release image (manual)** → *Run workflow*:
   - *Use workflow from*: **`main`**
   - *version*: the version without `v`, e.g. **`1.0.0`**
   - *confirm*: **`publish`**
3. **TV app:** in the TV repository, *Actions* → **Release (webOS .ipk)** → *Run workflow* with
   the same version (see the TV doc).
4. **Verify** both (below), then announce the pair.

From a terminal with the GitHub CLI, the same as step 2:

```sh
gh workflow run release.yml -R YTvictorworld/garceslab-anime-server --ref main \
  -f version=1.0.0 -f confirm=publish
gh run watch -R YTvictorworld/garceslab-anime-server
```

## What the workflow does

[`.github/workflows/release.yml`](../.github/workflows/release.yml), job by job:

1. **Check the request** (read-only): runs the tests of the release rules, then
   [`.github/scripts/release-check.sh`](../.github/scripts/release-check.sh). It **fails
   with a message** (shown as an error annotation on the run) when:
   - *confirm* is not exactly `publish`;
   - it runs on a branch other than `main`, or on a tag that isn't `vX.Y.Z`;
   - from `main`, *version* is empty, isn't `MAJOR.MINOR.PATCH`, or the tag `vX.Y.Z` already
     exists;
   - the version doesn't equal `GarceslabVersion` in the code ("bump it in a pull request
     first").

   Nothing is published when this job fails. (Before 1.0 the workflow *skipped* silently and
   showed a green run in these cases.)
2. **Image, tag and GitHub Release** (needs `contents: write` and `packages: write`):
   - builds the image for `linux/amd64` and `linux/arm64` from the exact commit the run
     started on and pushes `ghcr.io/ytvictorworld/garceslab-anime-server:vX.Y.Z` and
     `:latest`, with the OCI labels `version` (`vX.Y.Z`), `revision` (the commit) and `source`;
   - creates the tag `vX.Y.Z` at that commit (only when run from `main`), after the image is
     pushed, so a failed build leaves no tag behind;
   - creates the GitHub Release `vX.Y.Z` whose notes link the image, the exact source tree and
     commit (GPL-3.0 §6), the matching TV release, `NOTICE` and `MODIFICATIONS.md`.

Only one release runs at a time (`concurrency: release`); a second run waits.

**Run on an existing tag.** If a run failed after the tag was created (e.g. at the GitHub
Release step), start the workflow again with *Use workflow from* = the tag `vX.Y.Z` and
*confirm* = `publish` (*version* empty or the same). It rebuilds and pushes the same image tag
and creates the release.

## Verifying a release

```sh
# The image exists for both architectures and carries the version and the commit
docker buildx imagetools inspect ghcr.io/ytvictorworld/garceslab-anime-server:v1.0.0
docker pull ghcr.io/ytvictorworld/garceslab-anime-server:v1.0.0
docker inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }} {{ index .Config.Labels "org.opencontainers.image.revision" }}' \
  ghcr.io/ytvictorworld/garceslab-anime-server:v1.0.0
git ls-remote --tags https://github.com/YTvictorworld/garceslab-anime-server v1.0.0   # same commit

# A container started from it logs both versions
docker logs garceslab-anime 2>&1 | grep "app: Garceslab Anime"
```

- The release page shows the image, the source link and the commit.
- The GHCR package is public (the first time: *Packages* → *garceslab-anime-server* →
  *Package settings* → *Change visibility* → Public, and link it to this repository).
- The TV app of the same version connects (no "Server update required").

## Rolling back

Tags and releases are never moved or deleted once published; roll back by running the
previous version and fixing forward.

- **Users:** pin the previous image in `compose.yaml`
  (`image: ghcr.io/ytvictorworld/garceslab-anime-server:v1.0.0`), then
  `docker compose pull && docker compose up -d`. Back up the data volume before upgrading
  (`/home/seanime/.config/Seanime`); a newer version may have migrated it.
- **`latest`:** point it back at the previous version (needs a GHCR login with
  `write:packages`):
  `docker buildx imagetools create -t ghcr.io/ytvictorworld/garceslab-anime-server:latest ghcr.io/ytvictorworld/garceslab-anime-server:v1.0.0`
- **The broken release:** edit its GitHub Release notes to say "Don't use: see vX.Y.Z+1"
  (or mark it as a pre-release), then fix it in a pull request, bump the PATCH version in both
  repositories and release again.
- **A tag created by mistake before anything was published** (the image step failed and no
  release exists): delete it with `git push --delete origin vX.Y.Z` and run the workflow from
  `main` again.

## Tests

`bash .github/scripts/release-check_test.sh` covers the rules above (pass and fail cases,
including the regression "run from main without *confirm* fails instead of skipping"). CI runs
it in the *Go* job and the release workflow runs it before every release.
