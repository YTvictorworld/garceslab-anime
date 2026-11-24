# Self-hosting Garceslab Anime with an LG webOS TV

This guide sets up the Garceslab Anime server with Docker Compose, installs the TV app on an LG
TV with **webOS 23** (other webOS versions from 6.0 should work but are not tested), and walks
through the first run: profiles, PINs and the optional AniList import.

> **Not original work.** Garceslab Anime is derived from Seanime (server and web interface,
> © 5rahim and contributors) and Seanime Tenji (TV app, © AlkindiX and contributors, based on
> Seanime Tenji by 5rahim). This code is not our property: their authors keep their copyright,
> and this project only adds modifications under the same GPL-3.0 license. See
> [Credits & License](../README.md#credits--license).

> [!IMPORTANT]
> Garceslab Anime does not provide, host or distribute any media content. You run everything on
> your own hardware and are responsible for obtaining media legally.

<!-- Screenshot placeholder: overview of the web interface ("Who's watching?") -->

## What you need

- A computer that stays on (a mini PC, a NAS, a home server) with **Docker** and the
  **Docker Compose** plugin. Linux on x86-64 is what is tested.
- An **LG TV with webOS** on the same network, and an LG developer account (free) to turn on
  Developer Mode.
- A computer to install the TV app from (Windows, macOS or Linux).
- Optional: a TorBox (debrid) account, extensions for torrent or online providers, a folder with
  your own anime files.

## 1. Install the server with Docker Compose

Prebuilt images will be published on GHCR with the first release. Until then, build the image
from this repository.

```sh
git clone https://github.com/YTvictorworld/garceslab-anime-server.git
cd garceslab-anime-server
```

Create a `compose.yaml` next to the `Dockerfile`:

```yaml
services:
  garceslab-anime:
    build: .
    # After the first release you can use the published image instead of `build: .`:
    # image: ghcr.io/ytvictorworld/garceslab-anime-server:latest
    container_name: garceslab-anime
    restart: unless-stopped
    ports:
      - "43211:43211"
    environment:
      SEANIME_SERVER_HOST: "0.0.0.0"
      SEANIME_SERVER_PORT: "43211"
      # Ask every client to choose a profile before it can read or change user data.
      SEANIME_PROFILES_ENFORCE: "true"
    volumes:
      - garceslab_anime_data:/home/seanime
      # Optional: your own anime files, read-only.
      # - /path/to/your/anime:/anime:ro
    read_only: true
    tmpfs:
      # Streams are buffered here: give it room.
      - /tmp:size=8g,mode=1777
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true

volumes:
  garceslab_anime_data:
```

Start it:

```sh
docker compose up -d --build
docker compose logs -f garceslab-anime   # Ctrl+C to stop following the logs
```

The container runs as an unprivileged user and keeps everything (settings, `profiles.db`,
caches) in the `garceslab_anime_data` volume, under `/home/seanime/.config/Seanime`. Back up
that volume to keep your profiles and lists.

To update later: `git pull && docker compose up -d --build` (or `docker compose pull && docker
compose up -d` once you use the published image). The server never updates itself.

### Set a server password

Open `http://<server-address>:43211` in a browser on your network, go to **Settings → Server**
and set a **server password**. The TV app needs it: the server only accepts the TV's live
connection when a password is set, and the password is what keeps other devices on your
network out.

<!-- Screenshot placeholder: Settings → Server password -->

## 2. Install the TV app

The TV app is a `.ipk` package, published on the
[releases page of garceslab-anime-tv](https://github.com/YTvictorworld/garceslab-anime-tv/releases)
together with a `SHA256SUMS.txt` file. Download both and check the package:

```sh
sha256sum -c SHA256SUMS.txt
```

Then:

1. On the TV, install LG's **Developer Mode** app from the LG Content Store, sign in with your LG
   developer account and turn Developer Mode on. The TV restarts. Developer Mode expires after a
   while unless you extend it from the same app.
2. On your computer, install the package with either tool:
   - [webOS Dev Manager](https://github.com/webosbrew/dev-manager-desktop): add the TV (with the
     passphrase shown in the Developer Mode app), then **Apps → Install** and pick the `.ipk`.
   - The [webOS CLI](https://webostv.developer.lge.com/develop/tools/cli-installation):
     ```sh
     npm install -g @webos-tools/cli
     ares-setup-device                                 # add the TV once
     ares-install --device tv garceslab-anime-tv.ipk   # the file you downloaded
     ```
3. Open **Garceslab Anime** from the TV's app list.

If the upload fails even though the TV is reachable, see "If the upload fails" in the
[TV app's README](https://github.com/YTvictorworld/garceslab-anime-tv#install-on-an-lg-tv).

<!-- Screenshot placeholder: the app in the TV's launcher -->

## 3. First run

### Connect the TV to the server

The first screen asks for the server address and password. Enter the address with the port,
for example `http://192.0.2.10:43211` (use your server's address), and the password you set.

<!-- Screenshot placeholder: TV server setup screen -->

### Create the first profile

When the server has no profiles yet, both the web interface and the TV start with
**Create your profile**:

1. Enter a name (on the TV, with the on-screen keyboard and the remote).
2. Choose an avatar. All avatars are bundled with the server and are free-licensed artwork
   (CC0 or CC BY with credit); there are no uploads.
3. Optionally add a **PIN** and import an AniList list (see below).

<!-- Screenshot placeholder: "Create your profile" on the TV -->

### Who's watching?

Every time the app starts (a cold launch on the TV, every page load on the web) it asks
**Who's watching?**. Nothing is remembered between launches, so each person picks their own
profile. Each profile has its own list, "Continue watching", watch history, settings and
**player preferences**: subtitle and audio languages, subtitle size, autoplay and "next
episode", skipping openings and endings, and so on. Player preferences follow the profile:
change them on the TV and the next time that profile is chosen on the TV they are the same.
The web interface keeps its own set per profile (volume, subtitle styles and languages).

Manage profiles from the web interface in **Settings → Profiles**: add, rename, change the
avatar or colour, set or remove a PIN, export and import, and restore a deleted profile from
the trash within 30 days. **Recent changes** on that page lists who created, renamed, deleted,
restored, imported into or exported which profile, and who changed a PIN, and when.

<!-- Screenshot placeholder: Settings → Profiles with "Recent changes" -->

### PINs

A PIN (4 to 6 digits) is optional. It stops someone from entering your profile by mistake;
after 5 wrong tries the profile is locked for 5 minutes. It is **not** a security
barrier: there are no admin roles, so anyone who can use the server can remove a PIN or delete
a profile from **Settings → Profiles** (and that action shows in **Recent changes**). The
server password is what protects the server.

### AniList is optional

No profile needs an AniList account. AniList is used anonymously for public data (titles,
covers, search, airing schedules), and each profile's list is stored locally on your server.
If you already have an AniList list, you can copy it into a profile once:

- **By user name**: the public list of any AniList user, from "Create your profile" (web or TV)
  or later from **Settings → Profiles → Import**.
- **From a file**: a profile export (JSON) made by Garceslab Anime.

After an import the list lives on your server; nothing is sent back to AniList.

## 4. Watching

Browse on the TV with the remote (D-pad, OK, Back, media keys or the Magic Remote pointer). With
a TorBox account set in **Settings → Debrid** on the web interface, streams are played by the
TV's own player. Local files from your library play too; files the TV cannot decode need
transcoding turned on in **Settings → Media streaming**. See the TV app's
[webOS notes](https://github.com/YTvictorworld/garceslab-anime-tv/blob/main/webos/README.md)
for supported formats and remote shortcuts.

<!-- Screenshot placeholder: playing an episode on the TV -->

## Troubleshooting

- **The TV says the server turns it away**: set a server password (step 1) and enter it on the
  TV.
- **"Choose a profile" errors after an update**: open the app again and pick your profile;
  sessions last at most 24 hours.
- **The TV can't reach the server**: check that both are on the same network, that port 43211
  is open on the server's firewall, and use the server's IP address rather than a name the TV
  cannot resolve.
- **Logs**: `docker compose logs garceslab-anime`.
