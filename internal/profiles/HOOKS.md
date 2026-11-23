# Profiles hooks in upstream files

Garceslab Anime is a hard fork, so these hooks don't have to stay minimal for rebases,
but they document every place where the profiles feature touches Seanime code.
Find them with `grep -rn "profiles-hook" --include=*.go .`

New files (not upstream): `internal/profiles/**`, `internal/core/profiles.go`,
`internal/handlers/profiles.go`, `internal/handlers/profiles_test.go`.

| File | Hook |
|---|---|
| `internal/core/app.go` | `App.Profiles` field; `initProfiles(cfg, logger)` right after `db.NewDatabase` (fatal if profiles.db can't be opened or foreign keys are off); `Profiles:` in the `App` literal; cleanup closes the store |
| `internal/core/echo.go` | Static web skipper ignores `/profile-avatars/` (otherwise the HTML5 fallback would answer with `index.html`) |
| `internal/handlers/routes.go` | CORS allows `X-Seanime-Profile`; `profilesMiddleware()` on `/events` (for `?profile=`) and on `/api/v1` right after `OptionalAuthMiddleware`; `registerProfileRoutes(e, v1)` after `/status` |
| `internal/handlers/status.go` | `Status.Profiles` and `Status.CurrentProfile`; `fillProfilesStatus` at the end of `NewStatus` (not on restricted status) |

## Runtime switches

- `SEANIME_PROFILES_ENFORCE=true`: routes outside `internal/profiles/public_routes.go`
  answer `401 {"error":"PROFILE_REQUIRED"}` without a valid profile token. Off by default
  until the local platform (M2) routes user data per profile.
