# Configuration

Garceslab Anime is configured like Seanime (the web settings and `config.toml` in the data
directory). The features it adds read these **environment variables**; set them in the
`environment:` block of your `compose.yaml` (see [self-hosting-webos.md](self-hosting-webos.md)).

| Variable | Default | What it does | More |
|---|---|---|---|
| `SEANIME_SERVER_HOST` / `SEANIME_SERVER_PORT` | from `config.toml` | Address and port to listen on (use `0.0.0.0` in Docker) | Seanime |
| `SEANIME_PROFILES_ENFORCE` | `false` | `true`: every client must choose a profile before reading or changing user data (`401 PROFILE_REQUIRED` otherwise). Recommended | [profiles.md](profiles.md) |
| `SEANIME_MAX_VIEWERS` | `3` | Viewing slots: direct-play sessions plus the relayed lane | [phase2-direct-play.md](phase2-direct-play.md) |
| `SEANIME_VIEWER_IDLE_TIMEOUT` | `15m` | A viewer session without requests from its device ends | [phase2-direct-play.md](phase2-direct-play.md) |
| `SEANIME_VIEWER_DISCONNECT_GRACE` | `45s` | How long a closed websocket keeps its slot | [phase2-direct-play.md](phase2-direct-play.md) |
| `SEANIME_AUTOSELECT_PROBE_TOP` | `4` | How many cached debrid candidates auto-select may probe for subtitle languages (`0` turns it off, at most 10) | [subtitle-autoselect.md](subtitle-autoselect.md) |
| `SEANIME_TITLE_INDEX_DISABLED` | `false` | `true`: don't download the title index; search suggestions use AniList only | [title-search.md](title-search.md) |
| `SEANIME_EPISODE_TAGS_DISABLED` | `false` | `true`: no CANON / FILLER / RECAP tags | [episode-tags.md](episode-tags.md) |
| `SEANIME_EPISODE_TAGS_PROVIDERS` | `tenrai,jikan-edge,jikan` | Episode tag APIs and their order; `jikan4=<url>` or `edge=<url>` adds another instance | [episode-tags.md](episode-tags.md) |
| `SEANIME_TV_UPDATER_URL` | — | URL of an optional TV app updater (e.g. `http://tv-updater:8095`); wins over the URL saved in Settings → TV updates | [tv-updates.md](tv-updates.md) |
| `SEANIME_TV_UPDATER_TOKEN` / `SEANIME_TV_UPDATER_TOKEN_FILE` | — | The updater's shared token, or a file with it; wins over the saved token. Never returned to clients | [tv-updates.md](tv-updates.md) |

The server password is set in the web settings or in `config.toml`:

```toml
[server]
password = "your password"
```

Data written by these features lives in the data directory
(`/home/seanime/.config/Seanime` in the container): `profiles.db`, `title-index/`,
`episode-tags/`, `track-probe/` and `tv-updates.json`. Back up the volume to keep the profiles.
