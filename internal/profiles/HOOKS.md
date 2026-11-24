# Profiles hooks in upstream files

Garceslab Anime is a hard fork, so these hooks don't have to stay minimal for rebases,
but they document every place where the profiles feature touches Seanime code.
Find them with `grep -rn "profiles-hook" --include=*.go .`

New files (not upstream): `internal/profiles/**`, `internal/core/profiles.go`,
`internal/handlers/profiles.go`, `internal/handlers/profiles_data.go`,
`internal/handlers/profiles_test.go`, `internal/handlers/profiles_m2_test.go`,
`internal/continuity/store.go`, `internal/plugin/profiles.go`.

## M1: profile manager

| File | Hook |
|---|---|
| `internal/core/app.go` | `App.Profiles` field; `initProfiles(cfg, logger)` right after `db.NewDatabase` (fatal if profiles.db can't be opened or foreign keys are off); `Profiles:` in the `App` literal; cleanup closes the store |
| `internal/core/echo.go` | Static web skipper ignores `/profile-avatars/` (otherwise the HTML5 fallback would answer with `index.html`) |
| `internal/handlers/routes.go` | CORS allows `X-Seanime-Profile`; `profilesMiddleware()` on `/events` (for `?profile=`) and on `/api/v1` right after `OptionalAuthMiddleware`; `registerProfileRoutes(e, v1)` after `/status` |
| `internal/handlers/status.go` | `Status.Profiles` and `Status.CurrentProfile`; `fillProfilesStatus` at the end of `NewStatus` (not on restricted status). From M2 it also replaces `status.user` with the profile (`IsSimulated: true`, never the server's AniList account) and applies the profile's settings and theme |

## M2: local platform, router, per-profile data

The router is installed unless the server runs in offline mode (`profilesPlatformEnabled`).

| File | Hook |
|---|---|
| `internal/core/app.go` | `App.ProfilesRuntime` field; the AniList client is created **without** the server token (it stays in `accounts` for the one-time import); after the platform selection `newProfilesRuntime(...)` builds the anonymous metadata service + `RouterPlatform` and puts the router in `AnilistPlatformRef` |
| `internal/core/anilist.go` | `UpdatePlatform` keeps the router (any other platform is closed); `LoginToAnilist` and `applyRuntimeAnilistClient` return `ErrAnilistLoginDisabled`; `LogoutFromAnilist` is a no-op; `GetUserAnilistToken` returns `""`; `RefreshAnimeCollection()` refreshes the playback lane owner's collection (`refreshAnimeCollectionProfiles`) |
| `internal/core/offline.go` | `SetOfflineMode` refuses to switch while profiles are on |
| `internal/core/modules.go` | `InitOrRefreshAnilistData` ignores the server account (simulated user); the playback modules (playback manager, video core, mpv core, media core, direct stream, torrent stream, debrid, Nakama, playlist) get `a.playbackPlatformRef()` (the `LanePlatform`: calls without a profile in ctx go to the lane owner); playback manager, video core, mpv core, native player and direct stream get `a.playbackWS()` (`ProfileWS` of the lane owner; broadcast when there is no owner); `a.InstallProfilesRuntime()` at the end of `initModulesOnce` (lane activity check, owner's collection pushed to the playback modules on acquire, per-profile watch history, native auto-watching on `PlaybackLoadedEvent`) |
| `internal/continuity/manager.go`, `history.go` | Storage goes through `HistoryStore` (`store.go`; default = the upstream file cache bucket). `*Ctx` variants (`GetWatchHistoryCtx`, `GetWatchHistoryItemCtx`, `UpdateWatchHistoryItemCtx`, `DeleteWatchHistoryItemCtx`, `GetSettingsCtx`); the old methods use the background context = lane owner. `GetSettings()` and the external player paths use the per-profile "watch continuity" switch |
| `internal/database/db_bridge/autoselect_profile.go` | `ProfileAutoSelectHook` (lane owner's auto-select profile during a stream start); `GetGlobalAutoSelectProfile` = the upstream query |
| `internal/plugin/anilist.go`, `anime.go`, `manga.go`, `scanner.go` | Platform calls use `ProfileContext()` (`plugin/profiles.go`, set by core to the lane owner's context) instead of `context.Background()` |
| `internal/library/anime/missing_episodes.go`, `schedule.go` | Caches keyed by profile (`Get/Set…CacheFor(profileID)`); `Clear…` clears every profile |
| `internal/handlers/*.go` (anilist, anime_collection, anime_entries, auto_downloader, manga, manga_download, metadata, nakama, playlist, scan, torrent_client) | `h.App.GetAnimeCollection(…)` and friends → `GetAnimeCollectionCtx(c.Request().Context(), …)`, `RefreshAnimeCollection()` → `RefreshAnimeCollectionCtx(…)` (`context.WithoutCancel` in goroutines). Server-wide features (scanner, auto-downloader rule cleanup, Nakama host) use `GlobalAnimeCollection` (union of all profiles) |
| `internal/handlers/anilist.go`, `anime_entries.go` | `anilistStatsCache`, `anilistMissedSequelsCache`, `upcomingEpisodesCache` keyed by profile; stats cleared on refresh |
| `internal/handlers/auth.go` | `HandleLogin` → `410 Gone` |
| `internal/handlers/continuity.go` | Uses the `*Ctx` variants with the request context |
| `internal/handlers/settings.go` | `GET /settings` returns the effective settings; `PATCH /settings` moves the per-profile fields to the profile (`saveProfileSettingsFromBody`); `PATCH /settings/path` routes per-profile paths (`patchProfileSetting`) |
| `internal/handlers/theme.go`, `status.go` (home items) | Theme and home items per profile |
| `internal/handlers/torrent_search.go` | Auto-select profile per profile (falls back to the global one, never to the playing profile's) |
| `internal/handlers/websocket.go` | `tagWebsocketProfile` tags the connection with its `?profile=` profile |
| `internal/handlers/profiles.go` (own file) | Middleware `OnProfile` tags the client id (HTTP requests too), `RejectBusy` = `409 PLAYBACK_BUSY`; delete/logout untag and drop caches; `registerProfileDataRoutes` |

## Phase 4: polish

New files: `internal/profiles/settings/player.go`, `internal/handlers/profiles_player.go`,
`internal/handlers/profiles_audit.go`, `internal/profiles/store/migrations_phase4_test.go`.

| File | Hook |
|---|---|
| `internal/handlers/profiles.go` | `registerProfilePlayerRoutes(v1)` after `registerProfileDataRoutes` |
| `internal/handlers/profiles_data.go` | `afterImport` writes an `import` audit entry; `HandleExportProfile` an `export` entry |
| `internal/profiles/store/migrations.go` | Step 2 rebuilds `profile_audit` (`detail`, no action CHECK) |
| `internal/constants/constants.go`, `internal/updater/announcement.go` | Empty `AnnouncementURL` turns the upstream announcement feed off |

## Phase 3: AniList-like features

New files: `internal/handlers/profiles_library.go`, `internal/handlers/profiles_p3_test.go`,
`internal/profiles/localplatform/{favourites,editor}.go`, `internal/profiles/importer/anilist_account.go`.
No schema change: favourites, custom lists and activity use the v1 tables.

| File | Hook |
|---|---|
| `internal/handlers/profiles_data.go` | `registerProfileLibraryRoutes(v1)` at the end of `registerProfileDataRoutes`; `HandleImportProfileAnilist` takes `{token}` / `{favourites}` and names token imports in the audit |
| `internal/handlers/profiles_audit.go` | `auditProfileImport` uses the handler's `importAuditSourceKey` when set |
| `seanime-web/.../media-page-header-components.tsx` | `ProfileFavouriteButton` next to the list editor |
| `seanime-web/.../anilist-media-entry-modal.tsx` | With a profile, anime entries save through `PATCH /profiles/me/entries/:mediaId` with `ProfileEntryExtras` (notes, private, custom lists) |
| `seanime-web/.../lists/_containers/anilist-collection-lists.tsx` | Favourites section, "Manage lists", recently watched on the Stats tab |

## Phase 2: viewer sessions (up to three people watching)

New files: `internal/profiles/sessions/**`, `internal/handlers/profiles_viewers.go`,
`internal/handlers/profiles_viewers_test.go`, `internal/debrid/client/stream_sessions_test.go`,
`internal/e2e/phase2/**`, `scripts/e2e-phase2.sh`. Design: `docs/phase2-direct-play.md`.

| File | Hook |
|---|---|
| `internal/debrid/client/stream.go` | `StreamManager` holds a relayed `streamSlot` and one direct slot per client (`takeSlot`, `cancelClientStream`, `eventsFor` = `SendEventTo(client)` for direct streams); `notifyDirect` → `DirectStreamObserver` (resolving/ready/failed); `StreamPlaybackType.IsDirect`; the external player link is sent with `noLog` |
| `internal/debrid/client/repository.go` | `GetStreamURLFor`, `CancelStreamFor`, `CancelDirectStream`, `DirectStreamClientIds`, `SetDirectStreamObserver`, `SetProviderForTesting`; `GetPreviousStreamOptions` locks; auto-select status via `OnStatusCtx` (to the stream's client) |
| `internal/debrid/client/finder.go` | `db_bridge.FindAutoSelectProfileCtx(ctx, …)` (requesting profile) |
| `internal/torrents/autoselect/autoselect.go` | `OnStatusCtx`, `WithStatusClient`/`StatusClient`; `updateStatus` takes ctx |
| `internal/database/db_bridge/autoselect_profile.go` | `ProfileAutoSelectCtxHook`, `FindAutoSelectProfileCtx` |
| `internal/profiles/lane.go` | `AcquireFor(p, clientID)` checks the viewing-slot capacity (`SetCapacityFunc`); `ActiveOwner`; debrid start and manual tracking start left `playbackStartRoutes` |
| `internal/profiles/middleware.go` | `acquireLaneIfPlayback` passes the client id |
| `internal/core/profiles.go` | `ProfilesRuntime.Viewers`; `installViewerSessions` (relayed lane counts as a viewer, capacity check, ended session cancels its debrid resolve, debrid observer, sweep loop); `markProfileWatchingOnStart` (shared by the media core and direct sessions); `ProfileAutoSelectCtxHook`; `ViewerSessions()`, `OnDirectSessionPlaying` |
| `internal/handlers/debrid.go` | Start: direct type → viewer session, relayed → lane (`respondPlaybackSlotError`, slot freed on error); cancel: `CancelStreamFor(client)` + session release |
| `internal/handlers/playback_manager.go` | Manual tracking start/cancel and sync-current-progress go through the client's session (`startSessionManualTracking`, …); cancel accepts `{mediaId, episodeNumber}` |
| `internal/handlers/websocket.go` | `viewerWebsocketConnected` / `viewerWebsocketDisconnected` (disconnect grace) |
| `internal/handlers/profiles.go` | `registerProfileViewerRoutes(v1)`; `OnProfile` touches the session (idle timeout); delete → `Viewers.ForgetProfile` |
| `seanime-web/.../requests.ts`, `profiles.hooks.ts`, `profiles-settings.tsx` | LANES_FULL with names; `useGetViewerSessions`, `useStopViewerSession`; "Now watching" card |

## Subtitle-aware debrid auto-select

| File | Hook |
|---|---|
| `internal/debrid/client/track_probe.go` (own file) | `TrackPreferenceCtxHook`: the requesting profile's TV player languages (subtitles, audio, ignored labels) |
| `internal/core/profiles.go` | Sets `debrid_client.TrackPreferenceCtxHook` from `Settings.PlayerPrefs(ctx, profile).TV` |
| `internal/debrid/client/finder.go`, `stream.go` | `findBestTorrent` passes `autoselect.WithTrackProbe(...)` (preference of the profile in ctx) and an `onProgress` that only reaches the requesting client |

## Runtime switches

- `SEANIME_MAX_VIEWERS` (default 3), `SEANIME_VIEWER_IDLE_TIMEOUT` (default `15m`),
  `SEANIME_VIEWER_DISCONNECT_GRACE` (default `45s`): viewer sessions (phase 2).
- `SEANIME_AUTOSELECT_PROBE_TOP` (default 4, max 10, `0` = off): how many cached debrid
  candidates the subtitle-aware auto-select may read the MKV header of.
- `SEANIME_PROFILES_ENFORCE=true`: routes outside `internal/profiles/public_routes.go`
  answer `401 {"error":"PROFILE_REQUIRED"}` without a valid profile token. Still off by
  default until the web (M3) and TV (M4) clients send profiles. With it off, requests
  without a profile see the public catalog, empty lists, and their list writes fail
  with `PROFILE_REQUIRED` (they are never written to some profile).

## Phase 1 playback lane

Since phase 2 this lane is only for **relayed** playback (web player, torrent streams,
library files, transcoding); direct debrid streams use viewer sessions (above), and the
lane counts as one of their slots. Originally: one playback at a time on the whole server. The routes in `profiles.playbackStartRoutes`
take the lane in the middleware; another profile gets `409 PLAYBACK_BUSY` while the
owner's playback is being prepared (90 s grace) or the media core has an active session.
The owner stays after the playback ends, so late progress updates still reach it.
