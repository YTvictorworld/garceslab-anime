# Profiles and extensions (plugins)

Extensions are global: one installed plugin serves every profile. Their code runs on the
server without a request, so it has no profile of its own. This page explains which
profile a plugin's `$anilist` and `ctx.anime` calls act for, and when they are refused.

## The rule

A plugin call acts for **the profile that owns the playback it reacts to**. A playback is
owned by:

- a **viewer session**: a device that plays a debrid link itself (direct play, e.g. the
  LG TV; see [phase2-direct-play.md](phase2-direct-play.md)). The session knows its profile
  and its anime from the start request, before the device's player opens;
- the **relayed playback lane**: a playback through the server (web player, torrent
  streams, library files). Its anime comes from the media core once the player loads;
  while the playback is being prepared it is unknown and matches any anime.

| Call | Acts for |
|---|---|
| About one anime: `$anilist.updateEntry`, `updateEntryProgress`, `updateEntryRepeat`, `deleteEntry`, `addMediaToCollection` (first id), `ctx.anime.getAnimeEntry` | The one profile playing **that anime** |
| Not about one anime: `$anilist.getAnimeCollection`, `getRawAnimeCollection`, manga collections, `refreshAnimeCollection`, … | The one profile playing **anything** |

When nothing is playing that matches, the call falls back to the relayed lane owner, as in
phase 1 (late progress updates of a relayed playback that just ended), or gets no profile.

## When a call has no profile

A call gets **no profile** when:

- nobody plays that anime and the relayed lane has no owner;
- the only playback of that anime is a guest's (a client without a profile, possible only
  with `SEANIME_PROFILES_ENFORCE` off);
- two playbacks of different profiles (or a profile and a guest) match. For a call about
  one anime: both play it. For a collection read: both play something;
- the profile of the playback was deleted.

Then reads are anonymous (empty lists) and **list writes are refused** with
`PROFILE_REQUIRED: no profile selected, the change was not saved`. Nothing is ever written
to another profile, a default profile or an admin profile. The refusal is logged twice:

```
|WRN| plugin > List write has no profile to act for (no playback of this anime owned by exactly one profile), refused id=<extension> mediaId=… op=UpdateEntry reason="several playbacks match (profiles 1, 3)"
|WRN| profiles > List write without a profile refused mediaId=… op=UpdateEntry
```

`SEANIME_PROFILES_ENFORCE` doesn't change any of this: it only decides whether HTTP requests
without a profile token are refused.

## Writing a plugin that works with profiles

- React to a playback event (`$app.onDebridSendStreamToMediaPlayer`,
  `onTorrentStreamSendStreamToMediaPlayer`, `ctx.videoCore` events) and write right away,
  while the playback runs.
- Read the entry with `ctx.anime.getAnimeEntry(mediaId)` (its `listData`), not by searching
  `$anilist.getAnimeCollection()`: the collection read is not about one anime, so it is
  empty as soon as two people watch at the same time.
- Send `undefined` for the fields you don't change (`scoreRaw`, `startedAt`,
  `completedAt`): the local list keeps them.
- Direct play (the TV) doesn't go through the server's player, so `ctx.videoCore` events
  don't fire for it; the debrid hook does.
- Expect `PROFILE_REQUIRED` and don't retry it in a loop.

The native "mark as watching on start" (per profile, in the profile's settings) already does
what the `auto-watching` example plugin does, for direct and relayed playback.

## Where it lives in the code

- `internal/profiles/attribution.go`: `AttributePlayback`, the rule above (pure function).
- `internal/core/profiles_attribution.go`: `ProfilesRuntime.PlaybackProfileContext`, which
  builds the claims from the viewer sessions and the relayed lane.
- `internal/plugin/profiles.go`: `ProfileContext()` and `ProfileContextFor(mediaID)`, set by
  core; the bindings in `internal/plugin/anilist.go` and `anime.go` use them.
- Tests: `internal/profiles/attribution_test.go` and
  `internal/handlers/profiles_plugin_attribution_test.go` (direct play through the routes,
  two profiles, no playback, guest, relayed lane, with enforcement on and off).
