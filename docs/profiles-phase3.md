# Profiles, phase 3: AniList-like features

Everything here is stored per profile in `profiles.db`; AniList is only used
anonymously, except for the optional one-time token import.

## What a profile has

- **Favourites** (anime; manga, characters, staff and studios are kept for imports and
  exports). Heart button on the anime page (web) and a *Favourite* pill (TV).
- **Full entry editor**: status, score, progress, start/completion dates (clearable),
  rewatches, private, notes and custom lists. "Add to list" adds the anime as *Planning*.
- **Custom lists**: create, rename, reorder and delete from *My lists → Manage lists*;
  membership from the entry editor. They appear in the raw collection as
  `Lists[].isCustomList = true`.
- **Statistics**: `GET /api/v1/anilist/stats` is computed locally from the list and the
  media snapshots (count, episodes, minutes, mean score, statuses, formats, genres, score
  and year distributions; no studios). Planned anime are not counted, like AniList.
- **Recently watched**: from the profile's watch history (Stats tab on the web).

## Import and export

- `POST /api/v1/profiles/:id/import/anilist`
  - `{"username": "..."}`: a public list, anonymously.
  - `{"token": "..."}`: the token owner's list, private entries included. The token is
    used for that request only: never stored, cached or logged. Get it from your own
    AniList API client (redirect URL `https://anilist.co/api/v2/oauth/pin`); the web's
    import dialog builds the authorization link from its client ID.
  - `"favourites": false` skips the favourites (imported by default), `"mode"`: `merge`
    (newest entry wins) or `replace`.
- `GET /api/v1/profiles/:id/export` → `{format, version, exportedAt, profile, settings,
  theme, autoselect, entries, customLists, favourites, watchHistory, episodeProgress,
  activity}`; `POST /api/v1/profiles/:id/import/json` reads it back (favourites included).

## Endpoints added

| Method and route | What it does |
|---|---|
| `GET /api/v1/profiles/me/favourites[?kind=]` | Favourites (anime ones with their media) |
| `POST`/`DELETE /api/v1/profiles/me/favourites/:kind/:refId` | Add / remove a favourite |
| `GET`/`PATCH /api/v1/profiles/me/entries/:mediaId` | Read / edit every field of an entry |
| `PATCH /api/v1/profiles/me/lists/:listId` | Rename and/or move a custom list |
| `GET /api/v1/profiles/me/recently-watched[?limit=]` | Latest watched anime |

The upstream list endpoints (`/anilist/list-entry`, …) keep working and go to the
profile's list.
