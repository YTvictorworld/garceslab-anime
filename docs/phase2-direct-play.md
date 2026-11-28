# Phase 2: up to three people watching at once (direct play)

Phase 2 lets up to three devices watch different debrid (TorBox) episodes at the same
time, each with its own profile and progress. It uses the **direct play** design that
passed the phase 1.5 test on an LG C3: the server only resolves the debrid link, and the
device plays it itself. The server never relays the video of a direct stream, so there is
no per-viewer directstream, videocore or mediacore "lane" on the server.

## How a direct stream works

1. The TV (Tenji on webOS) starts a debrid stream with the playback type
   `externalPlayerLink`: `POST /api/v1/debrid/stream/start`.
2. The server takes a **viewer session** (a viewing slot) for the requesting client. When
   every slot is in use it answers `409` with
   `LANES_FULL: All 3 viewing slots are in use (Ana, Bob and Carl)`.
3. The debrid stream manager selects the torrent (auto-select with the requesting profile's
   auto-select settings, or the release the user chose), adds it to TorBox and waits for
   the link. Its status events (`debrid-stream-state`, loaders, auto-select status) go to
   that client only.
4. The link goes to that client only (`external-player-open-url`), and is never logged.
5. The TV's `<video>` plays the link; it reads the MKV's subtitles itself with paced,
   coalesced HTTP Range reads (see the TV repo's `docs/direct-play.md`).
6. The player starts manual tracking (`POST /playback-manager/manual-tracking/start`): the
   session becomes *playing* and the anime is marked as watching for the profile (native
   auto-watching). Progress is saved per profile by the TV's own requests (continuity
   every 15 s, the episode marked watched at 85 %).
7. Leaving the player cancels the tracking for that episode
   (`manual-tracking/cancel` with `{mediaId, episodeNumber}`), which frees the slot.

If the TV can't play the link (or read the file's header), it asks the server for a fresh
link to the same release and file and plays it directly again, up to **3 times** (2 s, 4 s and
8 s apart, as the same device, so no extra slot is used). Only after the third failed retry does
it start the same release through the server's direct stream (relayed) and say "Playing through
the server instead." Decode errors go to the server at once. Details: the TV repository's
[docs/direct-play.md](https://github.com/YTvictorworld/garceslab-anime-tv/blob/main/docs/direct-play.md).

## Viewer sessions (`internal/profiles/sessions`)

- One session per **client id** (device). Starting another episode on the same device
  replaces its session without using another slot.
- One session per **profile**: the same profile starting to watch on another device ends
  its older session (that device is told and closes its player).
- At most `SEANIME_MAX_VIEWERS` sessions (default **3**).
- A session ends:
  - when its device stops (debrid stream cancel, manual tracking cancel for its episode,
    or sync of the progress),
  - when its device's websocket stays closed for `SEANIME_VIEWER_DISCONNECT_GRACE`
    (default 45 s; a reconnect within it keeps the slot),
  - after `SEANIME_VIEWER_IDLE_TIMEOUT` (default 15 min) without any request from the
    device (a playing TV sends one every 15 s; a long pause can lapse, and the TV takes
    the slot again when the server tells it),
  - from Settings → Profiles → **Now watching** → Stop,
  - when its debrid link can't be resolved, or its profile is deleted.
- When the server ends a session it sends `viewer-session-ended`
  (`{sessionId, reason, mediaId, episodeNumber}`) to that device only, and cancels a
  debrid link still being resolved for it.
- Manual progress tracking lives in the session: two devices no longer cancel each
  other's tracking (upstream's playback manager has one global tracking).
- `GET /api/v1/profiles/viewers` lists the sessions (profile, device, anime, episode,
  state) and the relayed playback; client ids are not exposed.

## What changed in the debrid stream manager

Upstream keeps one global stream (`currentTorrentItemId`, one download context, one
stream URL): starting a stream for one device cancelled another's, and every status
event was broadcast. Now (`internal/debrid/client/stream.go`):

- A stream is a `streamSlot`. **Direct** playback types (`externalPlayerLink`, `none`,
  `noneAndAwait`) get one slot per client id; **relayed** ones (`default`, the server's
  media player, and `nativeplayer`, the server's direct stream) keep upstream's single
  slot.
- A direct start replaces only its client's previous stream. A relayed start replaces
  the previous relayed stream and its own client's direct stream, never another
  client's direct stream.
- `CancelStream` keeps upstream's meaning (the relayed stream). `CancelStreamFor(client)`
  cancels that client's direct stream, or the relayed one if it has none (what the cancel
  route does). `CancelDirectStream(client)` never touches the relayed stream.
- Status events of a direct stream go to its client (`SendEventTo`); a relayed stream
  still broadcasts, as upstream.
- An observer hears *resolving* (with the title), *ready* and *failed* (never the link);
  the viewer sessions use it.
- Hooks (`OnDebridSkipStreamCheck`, `OnDebridSendStreamToMediaPlayer`) run per stream as
  before. Plugins that listen to `ctx.videoCore` events don't see direct play (nothing
  plays on the server); the native auto-watching does (step 6 above). A plugin's list
  writes about the session's anime act for the session's profile
  ([profiles-extensions.md](profiles-extensions.md)).

## Events routing

| Event | Direct stream | Relayed stream |
|---|---|---|
| `debrid-stream-state`, `show/hide-indefinite-loader` | requesting client | broadcast (upstream) |
| `stream-auto-select-status` | requesting client | broadcast (upstream) |
| `external-player-open-url` | requesting client (not logged) | requesting client |
| manual tracking state / stopped | session's client | lane owner's profile (upstream tracking) |
| `viewer-session-ended` | session's client | — |

## Playback matrix

| Playback | Client | Path | Concurrency |
|---|---|---|---|
| Debrid stream on the TV (webOS Tenji) | TV `<video>` | **direct** (link from the server) | 1 viewer session each, up to 3 |
| Debrid stream, external player (Tenji Android/iOS, mpv/VLC links) | the device | **direct** | 1 viewer session each |
| Manual tracking of any client-played media (online streams on the TV) | the device | direct (no video through the server) | takes a viewer session |
| Debrid stream in the web browser | web player | **relayed** (`nativeplayer` direct stream: the browser can't play MKV/HEVC) | single relayed lane |
| Debrid stream, "Retry via server" on the TV | TV | relayed | single relayed lane |
| Torrent streaming (P2P) | any | relayed | single relayed lane (upstream) |
| Library files (direct stream or transcoding) | any | relayed | single relayed lane |
| Server-side players (mpv, VLC…) | server | relayed | single relayed lane |

- The **relayed lane** is phase 1's single playback lane: a second relayed playback of
  another profile gets `409 PLAYBACK_BUSY` (upstream behaviour otherwise). It counts as
  one viewer: two direct sessions + one relayed playback = full.
- Direct sessions are never cancelled by a relayed start and vice versa (except on the
  same device, which can only watch one thing).

## Limits

- **TorBox:** on the Essential plan, cached downloads don't use the active download slots,
  so three viewers of cached releases are fine; an uncached release waits for TorBox to
  download it. Each device downloads from TorBox's CDN itself.
- **Same home IP:** all three TVs stream from the CDN from the same public IP. The TV
  paces its subtitle reads (one at a time, at least 1 s apart, backing off on 429/503);
  watch the 429 count in the TV's Playback stats during the three-viewer test.
- The server no longer carries direct-play video, so its Wi-Fi, CPU and `/tmp` are not
  affected by direct viewers. A relayed playback still is.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `SEANIME_MAX_VIEWERS` | `3` | viewing slots (direct sessions + the relayed lane) |
| `SEANIME_VIEWER_IDLE_TIMEOUT` | `15m` | a session without requests from its device ends |
| `SEANIME_VIEWER_DISCONNECT_GRACE` | `45s` | how long a closed websocket keeps its slot |

On the TV, a build with `EXPO_PUBLIC_DIRECT_PLAY=0` sends every debrid stream through the
server instead (one viewer at a time again).

## Tests

```bash
# Unit and handler tests (fake debrid provider, no TorBox):
go test ./internal/profiles/sessions/ ./internal/debrid/client/ ./internal/handlers/ -run 'Viewer|Direct|Relayed|Lanes|Tracking|Disconnect|Idle'

# End to end against a throwaway server with the Dummy Debrid provider:
scripts/e2e-phase2.sh            # E2E_PORT (default 43391), E2E_DIR (default a temp dir)
```

`scripts/e2e-phase2.sh` builds the server, starts it on `127.0.0.1:$E2E_PORT` with its own
data directory, turns on `[experimental] dummydebrid`, and runs `internal/e2e/phase2`
(build tag `e2e`): four profiles on four websocket + HTTP clients start streams at once;
three get their own link and only their own events, the fourth gets `LANES_FULL` naming
who is watching; closing a player frees a slot for the fourth; Stop in Settings tells
only that device; a closed websocket frees its slot after the grace. Anime metadata comes
from AniList anonymously; no debrid service is called. With Docker only:

```bash
docker run --rm --dns 1.1.1.1 -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/tmp/gocache \
  -e GOMODCACHE=/tmp/gomod -e E2E_DIR=/e2e -v "$PWD:/src" -v "$(mktemp -d):/e2e" -w /src \
  golang:1.27.1 scripts/e2e-phase2.sh
```

## Three-viewer test on real devices

Needs three devices with the TV app (or two TVs and an Android phone with Tenji) and three
profiles.

1. Deploy the server from this branch and install the normal TV app
   (`app.seanime.tenji`) built from the TV repo's same branch.
2. On each device pick a **different profile** and start a **different cached** debrid
   episode at about the same time.
   - Each episode card shows only its own progress ("Selecting…", "Adding torrent…").
   - All three play; subtitles appear; audio switching and a far seek work.
3. Settings → Profiles → **Now watching** (web): three sessions, each with its profile,
   device ("LG TV"), anime and episode, "Watching".
4. On a fourth device or profile, start an episode: the card says "All 3 viewing slots
   are in use (…). Try again when someone stops watching." Nothing changes on the three.
5. Leave the player on one TV: within seconds Now watching shows two; the fourth can now
   start.
6. Press **Stop** on one session in Now watching: that TV closes its player with
   "Playback was stopped from Settings on another device."; the others keep playing.
7. Turn one TV off (or pull its network): its slot frees after about a minute.
8. Afterwards, check each profile's progress and "Continue watching": every episode is
   in the right profile, none in another's.
9. During the test, turn on Playback stats on one TV: the 429 count should stay near 0;
   the server's CPU and network should stay low (no video through it).
