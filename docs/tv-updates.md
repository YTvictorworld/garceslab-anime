# TV app updates (optional)

Garceslab Anime can tell you when a new version of the TV app is ready and install it on the TV
**only after you confirm**. It doesn't build or install anything itself: it talks to a small
**TV updater** service that you run next to the server (for example one that watches the TV
app's GitHub releases). The TV app and the web interface never talk to the
updater directly; they ask this server, which forwards the request with a shared token.

Without an updater this feature is off and every client hides its update UI.

- [Setting it up](#setting-it-up)
- [Using it](#using-it)
- [Server API (what clients call)](#server-api-what-clients-call)
- [Updater API (what the server calls)](#updater-api-what-the-server-calls)
- [Playback check](#playback-check)
- [Security](#security)
- [Tests](#tests)

## Setting it up

You need a TV updater that implements the [updater API](#updater-api-what-the-server-calls)
and that this server can reach, for example on a shared Docker network.

Either in the web interface, **Settings → TV updates → TV updater**:

| Field | Example | Notes |
|---|---|---|
| Updater URL | `http://tv-updater:8095` | `http://` or `https://`, no user name, password, query or fragment. Empty turns the feature off. |
| Token | the updater's `UPDATER_TOKEN` | Optional, but recommended. Stored on the server only and never shown again; leave the field empty to keep the saved token. |

Or with environment variables (handy in Docker Compose):

| Variable | What it does |
|---|---|
| `SEANIME_TV_UPDATER_URL` | The updater URL. |
| `SEANIME_TV_UPDATER_TOKEN` | The shared token. |
| `SEANIME_TV_UPDATER_TOKEN_FILE` | A file with the token (Docker secrets). Ignored when `SEANIME_TV_UPDATER_TOKEN` is set. |

**Precedence:** each environment variable wins over the value saved in the web settings, field
by field. A value set by the environment is shown read-only in the web settings, and a change
to it is refused (`409 TV_UPDATER_SET_BY_ENV`). You can set the URL in the environment and the
token in the web, or the other way round.

The web settings are saved in `tv-updates.json` in the data directory (mode `0600`).

### Docker Compose example

The updater doesn't need a published port. Put both containers on the same network (here the
updater's own network, `tv-updater`, created by its compose project):

```yaml
services:
  garceslab-anime:
    # ...
    environment:
      SEANIME_TV_UPDATER_URL: http://tv-updater:8095
      SEANIME_TV_UPDATER_TOKEN_FILE: /run/secrets/tv_updater_token
    secrets:
      - tv_updater_token
    networks:
      - default
      - tv-updater

networks:
  tv-updater:
    external: true

secrets:
  tv_updater_token:
    file: ./secrets/tv_updater_token   # the same value as the updater's UPDATER_TOKEN
```

## Using it

**Settings → TV updates** shows the **TV app update** card when an updater is configured:

- the channel (`nightly` or `stable`), whether the TV is on, and the installed version;
- the pending version, with its commit, build date and changes grouped as **New**, **Fixes**
  and **Other** (with a note when the list may be incomplete);
- **Install on TV** and **Later** buttons; while an install waits for the TV to turn on,
  **Cancel**;
- the last install result.

When the updater doesn't answer, the card says so, with a **Try again** button.

Installing closes the TV app (the updater reopens it if it was open). If the TV is off, the
updater keeps the request and installs when the TV turns on. **Later** dismisses the pending
version; you are told about the next one.

The TV app shows the same information and buttons (see the TV repository's docs).

## Server API (what clients call)

All routes use the server's usual envelope: `{"data": ...}` on success, `{"error": "CODE: details"}`
otherwise. They **require a profile** when profiles are enforced (`401 PROFILE_REQUIRED`), like
every route that isn't public.

### `GET /api/v1/tv-updates/status`

Always `200`. Three shapes of `data`:

```jsonc
// No updater URL: clients hide all update UI.
{ "configured": false }

// Configured, but the updater didn't answer within 3 s, answered with a non-2xx status,
// or didn't answer with a JSON object.
{ "configured": true, "reachable": false, "error": "the updater did not answer within 3s" }

// The updater's GET /status, passed through unchanged (see below).
{ "configured": true, "reachable": true, "status": { "channel": "nightly", "installed": { ... }, "pending": { ... }, ... } }
```

`error` is a short message without the URL or the token.

### `POST /api/v1/tv-updates/install`

Confirms the pending version. No body.

| Status | Body | When |
|---|---|---|
| `200` | `{"data": {"ok": true, "result": {"ok": true, "queued": true, "commit": "…", "version": "…"}}}` | The updater accepted it (`result` is the updater's JSON, unchanged) |
| `200` | `{"data": {"ok": false, "result": {"ok": false, "error": "no pending build"}}}` | The updater answered but refused (any non-2xx, or `"ok": false`) |
| `409` | `{"error": "PLAYBACK_ACTIVE: Ana is watching on the TV"}` | Something may be playing on the TV ([playback check](#playback-check)); the updater isn't called |
| `404` | `{"error": "TV_UPDATER_NOT_CONFIGURED"}` | No updater URL |
| `502` | `{"error": "TV_UPDATER_UNREACHABLE: …"}` | The updater didn't answer within 10 s, or not with a JSON object |

`ok` is `true` when the updater answered `2xx` and its JSON doesn't contain `"ok": false`. The
install itself runs in the updater after it answers (`202`): follow it with `GET .../status`
(`status.installing`, `status.installPhase`, `status.lastResult`).

### `POST /api/v1/tv-updates/later`

Dismisses the pending version (and cancels an install waiting for the TV). Same responses as
`install`, without the playback check: dismissing never closes the app.

### `GET /api/v1/tv-updates/settings` and `PATCH /api/v1/tv-updates/settings`

```jsonc
// GET → data, and the answer to PATCH
{ "configured": true, "url": "http://tv-updater:8095", "tokenConfigured": true, "urlFromEnv": false, "tokenFromEnv": false }

// PATCH body: an omitted field keeps its value, "" clears it
{ "url": "http://tv-updater:8095", "token": "…" }
```

The token is **never** returned, only `tokenConfigured`. Errors: `400 TV_UPDATER_INVALID_SETTINGS`
(bad URL), `409 TV_UPDATER_SET_BY_ENV` (the field comes from an environment variable).

## Updater API (what the server calls)

Any service that implements this can be your updater. The reference implementation is the
maintainer's `tv-updater` (Node; not published yet). All answers are JSON.

| Request | Token | Answer |
|---|---|---|
| `GET {url}/status` | not sent | `200` and the status object below |
| `POST {url}/install` | `X-Updater-Token` | `202 {"ok": true, "queued": true, ...}` · `202 {"ok": true, "alreadyQueued": true}` · `409 {"ok": false, "error": "no pending build"}` · `401` with a wrong token |
| `POST {url}/later` | `X-Updater-Token` | `200 {"ok": true, "dismissed": "<commit>", "cancelledQueue": false}` · `409 {"ok": false, "error": "..."}` (nothing pending, or an install is running) · `401` |

The server sends `X-Updater-Token: <token>` on the `POST` requests when a token is configured,
with an empty body, and doesn't follow redirects. The updater should refuse a `POST` without
the right token when it has one.

`POST /install` must only **queue** the install and answer quickly (the server waits up to 10 s);
the install itself happens afterwards, and `GET /status` reports its progress. Nothing should be
installed without this confirmation.

### Status object

```json
{
  "channel": "nightly",
  "installed": { "version": "1.0.0", "commit": "<sha>", "shortCommit": "808b9ee", "channel": "nightly", "builtAt": "…" },
  "pending": {
    "version": "1.0.1", "commit": "<sha>", "shortCommit": "0eff776", "builtAt": "2026-11-26T19:03:40.416Z",
    "channel": "nightly",
    "changes": [ { "type": "feat", "title": "Seasons tabs", "pr": 21, "url": "https://github.com/…/pull/21" } ],
    "changesComplete": true
  },
  "tvOnline": true,
  "installing": false,
  "installPhase": null,
  "lastResult": { "at": "…", "ok": true, "version": "1.0.0", "commit": "<sha>", "shortCommit": "808b9ee", "error": null }
}
```

| Field | Meaning |
|---|---|
| `channel` | `nightly` or `stable` |
| `installed` | What the TV has (`commit` may be `null` for a build without a `build.json`), or `null` when unknown |
| `pending` | The version waiting for confirmation, or `null`. `changes[]`: `type` is `feat`, `fix` or `other`, oldest first, without internal changes; `changesComplete: false` when the list may be missing some |
| `tvOnline` | The TV answers |
| `installing` | `true` from the confirmation until the install ends (also while waiting for the TV) |
| `installPhase` | `waiting-tv`, `installing` or `null` (optional) |
| `lastResult` | The last install: `ok`, `error`, version and commit, or `null` |

Clients only rely on `channel`, `installed`, `pending`, `tvOnline`, `installing` and
`lastResult`; every other field (the reference updater also sends `queued`, `appWillClose`,
`lastCheckAt`, `lastCheckError`, `tvConfigured`, `devmode`) is passed through and optional.
The status must never contain paths, keys or tokens: the server passes it to every profile.

### Differences from the issue text (#18)

The issue sketched the contract before the updater existed. The implementation follows the real
updater:

- Not configured is `200 {"configured": false}` for the status (clients need no special error
  handling); `install` and `later` answer `404 TV_UPDATER_NOT_CONFIGURED`.
- The updater's status has more fields than the issue listed (`lastResult`, `installPhase`, …);
  they are passed through.
- `changes[].type` can also be `other`, shown as **Other**.
- `POST /install` queues the install and answers `202` right away; the result shows up later in
  `lastResult`.
- There is also `POST /later` (dismiss), which the issue didn't list.
- The issue says "refuses while that device has an active playback session": the server doesn't
  know which device the updater installs on, see the next section.

## Playback check

Installing closes the TV app, so `POST /install` is refused with `409 PLAYBACK_ACTIVE` while the
server knows that the TV may be playing:

- a **viewer session** ([phase 2](phase2-direct-play.md)) started from an LG webOS TV
  ("LG TV" in **Now watching**) is open; viewer sessions on other devices (browser, phone)
  don't block it;
- the **relayed playback** (web player, torrent stream, library file through the server) is in
  use: the server doesn't record which device holds it, so any relayed playback blocks the
  install.

The server can't tell which TV the updater installs on, so a session on **any** LG TV counts.
Without profiles there are no viewer sessions, so the server has no signal and doesn't refuse;
the updater's own warning that the app will close still applies. `later` is never blocked.

## Security

- **The token stays on the server.** It is stored in `tv-updates.json` (mode `0600`) or comes
  from the environment, is only sent to the updater in `X-Updater-Token`, and is never returned
  to a client or written to a log. A URL with a user name or password is refused so the token
  can't end up in it. Redirects are not followed.
- **LAN or internal network only.** The updater can install software on your TV. Don't publish
  it on the internet; the simplest setup is a Docker network shared with this server and no
  published port. Use a token anyway.
- Any profile can see the status and confirm an install, like every other setting (there are no
  roles). With `SEANIME_PROFILES_ENFORCE=true`, a client must choose a profile first.
- The updater's status is passed to clients as is: an updater must not put secrets in it.

## Tests

- `internal/tvupdates/tvupdates_test.go`: the proxy against a fake updater (`httptest`): not
  configured, unreachable, timeout, not JSON, pending, installing, install, already queued,
  later, wrong token, settings saved with mode `0600`, URL validation, environment precedence,
  token file, and the token never in responses or logs.
- `internal/handlers/tv_updates_test.go`: the HTTP routes: the three status shapes, install and
  later through the routes, a profile required when enforced, `PLAYBACK_ACTIVE` for a TV viewer
  session and the relayed lane (not for a browser session), settings errors, and the token
  absent from every response and from the logs (request logs included).
- `seanime-web/src/lib/tv-updates/tv-updates.test.ts`: grouping of changes and the texts of the
  Updates card.

```sh
scripts/check.sh ./internal/tvupdates/ ./internal/handlers/
cd seanime-web && npm test
```
