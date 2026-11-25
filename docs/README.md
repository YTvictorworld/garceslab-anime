# Garceslab Anime documentation

All documentation of the Garceslab Anime **server and web interface** lives in this folder.
The TV app has its own docs in
[garceslab-anime-tv/docs](https://github.com/YTvictorworld/garceslab-anime-tv/tree/main/docs).

Garceslab Anime is derived from Seanime (© 5rahim and contributors) and Seanime Tenji (© AlkindiX
and contributors), under the GPL-3.0; see [Credits & License](../README.md#credits--license).
It is published only for self-hosting with LG webOS TVs: there is no hosted service.

## Setting it up

| Doc | What it covers |
|---|---|
| [self-hosting-webos.md](self-hosting-webos.md) | Docker Compose, server password, installing the TV app, first run, profiles, PINs, optional AniList import, troubleshooting |
| [configuration.md](configuration.md) | Every environment variable Garceslab Anime adds, with defaults |

## Features

| Doc | What it covers |
|---|---|
| [profiles.md](profiles.md) | "Who's watching?", avatars, PINs, per-profile data and player preferences, AniList import and JSON export, Recent changes |
| [profiles-phase3.md](profiles-phase3.md) | Favourites, entry editor, custom lists, stats, private import: endpoints and export format |
| [phase2-direct-play.md](phase2-direct-play.md) | Up to three viewers at once: direct play of debrid streams, viewer sessions, "Now watching", limits, tests |
| [subtitle-autoselect.md](subtitle-autoselect.md) | Auto-select that prefers cached releases with the profile's subtitle language |
| [title-search.md](title-search.md) | Search as you type with the local title index (anime-offline-database, ODbL) |
| [episode-tags.md](episode-tags.md) | CANON / FILLER / RECAP / MIX episode tags from MyAnimeList data, providers and caching |

## Developing

| Doc | What it covers |
|---|---|
| [../CONTRIBUTING.md](../CONTRIBUTING.md) | Issue first, the pull request checklist and the "(WIP)" rule, running the checks |
| [../AGENTS.md](../AGENTS.md) | Guide for AI coding agents: layout, commands, checks, screenshots, privacy |
| [../DEVELOPMENT_AND_BUILD.md](../DEVELOPMENT_AND_BUILD.md) | Building from source (inherited from Seanime) |
| [../internal/profiles/HOOKS.md](../internal/profiles/HOOKS.md) | Every place where the profiles feature touches Seanime code |
| [dependencies.md](dependencies.md) | Dependabot updates, and the chromedp v0.24 migration of the extensions' `ChromeDP` API with its tests |
| [../MODIFICATIONS.md](../MODIFICATIONS.md) | Every change compared with Seanime v3.10.5, with dates (GPL-3.0 §5a) |
| [../NOTICE](../NOTICE) | Copyright, licences and third-party data |

Screenshots used by the docs are in [images/](images/). They come from test setups with made-up
profiles; anime titles and art are AniList's public data.

## Roadmap

- **1.0 release:** prebuilt server images on GHCR and the `.ipk` on the TV repository's
  GitHub Releases, each built from a git tag.
- **Self-hosted subtitle translation (planned):** an optional service that runs on your own
  server and translates a release's subtitles into the profile's language when no release has
  them, so nothing is sent to a third-party translation service. It is not designed in detail
  yet and will get its own doc here when work starts.
- **Own app id for the TV app** (it still installs as `app.seanime.tenji`).
