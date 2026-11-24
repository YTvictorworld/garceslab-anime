<p align="center">
<img src="branding/logo.svg" alt="Garceslab Anime" width="80px"/>
</p>

<h1 align="center">Garceslab Anime</h1>

<p align="center">
A self-hosted anime media server and web interface, made to work well with LG webOS TVs.<br/>
Based on Seanime and Seanime Tenji (GPL-3.0).
</p>

---

> **Not original work.** Garceslab Anime is derived from
> [Seanime](https://github.com/5rahim/seanime) (© 5rahim and contributors) and
> [Seanime Tenji](https://github.com/AlkindiX/seanime-tv) (© AlkindiX and contributors). Their
> authors keep their copyright; this project only adds modifications, under the same GPL-3.0
> license. See [Credits & License](#credits--license).

**Status: early development.** Based on Seanime v3.10.5, with local profiles, a few fixes
and the update checker turned off. The features below that are marked *planned* don't exist yet.

**Setting it up with an LG TV:** see [Self-hosting with LG webOS](docs/self-hosting-webos.md)
(Docker Compose, installing the TV app, first run, profiles, PINs, optional AniList import).

## What it is

Garceslab Anime is a server you run yourself (Docker) plus a web interface, and a companion TV
app for LG webOS in [garceslab-anime-tv](https://github.com/YTvictorworld/garceslab-anime-tv).
It is published only so that people can **self-host a version that works with LG webOS TVs**
(tested on webOS 23).

There is **no hosted service**: no website to watch anything on, no accounts, nothing run by
this project. You install and run it on your own hardware.

> [!IMPORTANT]
> Garceslab Anime does not provide, host or distribute any media content. Users are responsible
> for obtaining media through legal means and for complying with the laws of their country.
> Extensions are third-party software, unaffiliated with this project.

## Features

- Everything Seanime v3.10.5 does: library management and scanning, AniList integration,
  debrid and torrent streaming, online streaming and torrent providers through extensions,
  plugins, schedule, and the web interface.
- **LG webOS fixes:** debrid streams requested without an HTTP `Range` header (as the webOS
  native player does) are served correctly, so the TV no longer needs a proxy in front of the
  server.
- **No auto-update to Seanime:** the update check and self-update are turned off, so the server
  is never replaced by a Seanime release.
- **Local profiles** ("Who's watching?") without an AniList account: each profile has its own
  list, progress, history, settings and player preferences, an optional PIN, and avatars from a
  bundled free-licensed catalog. AniList is only used anonymously for public data; a public
  AniList list can be imported once. Profile changes are listed in **Settings → Profiles →
  Recent changes**.
- **No upstream announcements:** the server doesn't fetch Seanime's announcement feed.
- *Planned:* up to three playbacks at the same time.

## Run it with Docker

```sh
docker build -t garceslab-anime-server .
docker volume create garceslab_anime_server_data
docker run -d --name garceslab-anime-server \
  -p 43211:43211 \
  -e SEANIME_SERVER_HOST=0.0.0.0 -e SEANIME_SERVER_PORT=43211 \
  -v garceslab_anime_server_data:/home/seanime \
  --read-only --tmpfs /tmp:size=8g,mode=1777 \
  --cap-drop ALL --security-opt no-new-privileges:true \
  garceslab-anime-server
```

Or with Docker Compose, as in the [self-hosting guide](docs/self-hosting-webos.md).
Then open `http://<server>:43211`. The container runs as the unprivileged user `seanime`
(uid 1000) and keeps its data in `/home/seanime/.config/Seanime` (same layout as the community
rootless Seanime images, so an existing volume can be reused). Streams are buffered in `/tmp`,
hence the large tmpfs. Set a server password in the settings (or in `config.toml`) before
exposing it to your network. Prebuilt images will be published at
`ghcr.io/ytvictorworld/garceslab-anime-server` once the first release is made.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) and [DEVELOPMENT_AND_BUILD.md](DEVELOPMENT_AND_BUILD.md).
Changes compared with Seanime are listed in [MODIFICATIONS.md](MODIFICATIONS.md).

## Credits & License

Garceslab Anime is **not originally our code**. It is derived from:

- **[Seanime](https://github.com/5rahim/seanime)**, © 5rahim and the Seanime contributors: the
  server and web interface in this repository are derived from Seanime v3.10.5.
- **[Seanime Tenji](https://github.com/AlkindiX/seanime-tv)**, © AlkindiX and contributors
  (based on [Seanime Tenji](https://github.com/5rahim/seanime-tenji) by 5rahim): the TV app in
  [garceslab-anime-tv](https://github.com/YTvictorworld/garceslab-anime-tv) is derived from it.

Search suggestions (prefix "search as you type") come from the
[anime-offline-database](https://github.com/manami-project/anime-offline-database) by
manami-project, available under the [Open Database License (ODbL) v1.0](https://opendatacommons.org/licenses/odbl/1-0/)
(contents under the Database Contents License v1.0). It is not included in this repository: the
server downloads it at runtime into its data directory (`title-index/`, about 6 MB, refreshed
weekly) and serves `GET /api/v1/search/suggest` from it. Set `SEANIME_TITLE_INDEX_DISABLED=true`
to turn it off; clients then use the AniList search only.

Profile avatars are generated with [DiceBear](https://www.dicebear.com) (code under MIT) from
styles licensed CC0 1.0 (Lorelei, Notionists, Critters, Thumbs, Open Peeps by Pablo Stanley,
Pixel Art, Voxel Bot, Clay, Sprouts, Planets) and CC BY 4.0 (**Adventurer** by Lisa Wischofsky,
[source](https://www.figma.com/community/file/1184595184137881796),
[licence](https://creativecommons.org/licenses/by/4.0/)). Every image's licence and source is in
[`internal/profiles/avatars/catalog.json`](internal/profiles/avatars/catalog.json) and
[`LICENSES.md`](internal/profiles/avatars/LICENSES.md); CI rejects avatars without one.

Episode tags (CANON / FILLER / RECAP on episode cards) use [MyAnimeList](https://myanimelist.net)
episode data, read through the Jikan-compatible APIs [Tenrai](https://tenrai.org),
[jikan-edge](https://jikan.lucashdo.com) and [Jikan](https://jikan.moe). The data belongs to
MyAnimeList; this project is not affiliated with MyAnimeList or with these APIs. The server
fetches the episode list of the anime being viewed in the background (at most one request per
second per API, rotating to the next API when one is rate limited or down) and caches it in its
data directory (`episode-tags/`, one small JSON file per anime, refreshed daily while airing and
every 30 days once finished). `GET /api/v1/anime/episode-tags/{id}` serves it.
`SEANIME_EPISODE_TAGS_PROVIDERS` sets the APIs and their order (default
`tenrai,jikan-edge,jikan`; `jikan4=<url>` or `edge=<url>` adds another instance), and
`SEANIME_EPISODE_TAGS_DISABLED=true` turns the feature off.

The original authors keep the copyright on their work, and this project does not claim
ownership of it. All original copyright and license notices are preserved unchanged, and the
full git history is kept, so every upstream commit keeps its author.

The modifications made by the Garceslab Anime contributors are distributed under the same
license, the [GNU General Public License v3.0](LICENSE), with no additional restrictions. The
modifications and their dates are listed in [MODIFICATIONS.md](MODIFICATIONS.md); see also
[NOTICE](NOTICE).

"Seanime" and "Seanime Tenji", and their logos, belong to their respective owners and are used
only to say where this project comes from. Garceslab Anime is an independent, unofficial
project, not affiliated with or endorsed by Seanime or Seanime Tenji: please don't report
Garceslab Anime issues to them.
