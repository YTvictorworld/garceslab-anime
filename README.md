<p align="center">
<img src="branding/logo.svg" alt="Garceslab Anime" width="80px"/>
</p>

<h1 align="center">Garceslab Anime</h1>

<p align="center">
A self-hosted anime media server and web interface, made to work well with LG webOS TVs.<br/>
Based on Seanime and Seanime Tenji (GPL-3.0).
</p>

---

**Status: early development.** The current version is Seanime v3.10.5 with a few fixes and the
update checker turned off. The features below that are marked *planned* don't exist yet.

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
- *Planned:* local Netflix-style profiles without an AniList account, and up to three playbacks
  at the same time.

## Run it with Docker

```sh
docker build -t garceslab-anime .
docker volume create garceslab_anime_data
docker run -d --name garceslab-anime \
  -p 43211:43211 \
  -e SEANIME_SERVER_HOST=0.0.0.0 -e SEANIME_SERVER_PORT=43211 \
  -v garceslab_anime_data:/home/seanime \
  --read-only --tmpfs /tmp:size=8g,mode=1777 \
  --cap-drop ALL --security-opt no-new-privileges:true \
  garceslab-anime
```

Then open `http://<server>:43211`. The container runs as the unprivileged user `seanime`
(uid 1000) and keeps its data in `/home/seanime/.config/Seanime` (same layout as the community
rootless Seanime images, so an existing volume can be reused). Streams are buffered in `/tmp`,
hence the large tmpfs. Set a server password in the settings (or in `config.toml`) before
exposing it to your network. Prebuilt images will be published at
`ghcr.io/ytvictorworld/garceslab-anime` once the first release is made.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md) and [DEVELOPMENT_AND_BUILD.md](DEVELOPMENT_AND_BUILD.md).
Changes compared with Seanime are listed in [MODIFICATIONS.md](MODIFICATIONS.md).

## Credits

Garceslab Anime is based on:

- **[Seanime](https://github.com/5rahim/seanime)** by **5rahim** and its contributors: the
  server and web interface in this repository are derived from Seanime v3.10.5.
- **[Seanime Tenji](https://github.com/AlkindiX/seanime-tv)** by **AlkindiX** (from Seanime
  Tenji by 5rahim): the TV app in
  [garceslab-anime-tv](https://github.com/YTvictorworld/garceslab-anime-tv) is derived from it.

The original git history is kept, so every upstream commit keeps its author. The original
copyright and license notices are preserved (see [NOTICE](NOTICE)). Garceslab Anime is an
independent, unofficial project: it is not affiliated with or endorsed by Seanime or Seanime
Tenji, so please don't report Garceslab Anime issues to them.

## License

[GNU General Public License v3.0](LICENSE), like the projects it is based on.
