# Go tests

How the Go test suite runs, which tests are skipped, and how to keep new tests deterministic.
Running the checks themselves is described in [CONTRIBUTING.md](../CONTRIBUTING.md#running-the-checks).

## Known-failing tests

`scripts/check.sh` (and CI) skips the tests listed in
[`.github/scripts/known-failing-go-tests.txt`](../.github/scripts/known-failing-go-tests.txt). They
already fail on a clean Seanime v3.10.5 checkout, mostly because they need fixtures Seanime keeps out of
git or a desktop session. When you fix one, remove it from the list in the same pull request.

A flaky test is not a known failure: find the cause instead of adding it to the list. To shake out
flakes, run the package many times with the race detector:

```sh
go test -race -count=200 ./internal/playlist/
```

## Keeping tests deterministic

- **No sleeps.** Wait for a condition (`require.Eventually`, a channel) rather than a fixed delay.
- **Know which setters are asynchronous.** If a setter starts a goroutine, a test that uses the value
  right after the call races with it. Prefer setters that store the value before returning and only run
  listeners in the background.
- **Stop background work in `t.Cleanup`**, so a goroutine from one test can't touch the next test's
  state.
- **Test the "not configured" path.** A dependency that is optional on a real server (a media player,
  a debrid provider, AniList) needs a test where it is missing.

## Playlists without a desktop media player (#10)

`TestPlaylistManagerPlayEpisodeNextWithoutCurrentEpisode` and `TestPlaylistManagerPlayEpisodePrevious`
used to panic now and then (more often on the CI runner) with a nil `*mediaplayer.Repository` in
`PlaybackManager.Cancel`.

- **Cause:** `PlaybackManager.SetMediaPlayerRepository` stored the repository from a goroutine. The
  playlist manager calls `Cancel()` before every episode, and `Cancel` dereferenced the repository
  without a nil check. A test that played an episode right after building the playback manager lost the
  race now and then.
- **Real servers too:** the media player repository only exists when the settings have a media player
  section, and it was set asynchronously after start-up or a settings change, so a playlist could hit the
  same panic.
- **Fix:** `SetMediaPlayerRepository` stores the repository (and its event subscriber) before it returns;
  only the event listener runs in a goroutine. Every method that drives the desktop media player
  (`Cancel`, `Pause`, `Resume`, `SeekTo`, `PullStatus`, `AppendToMediaPlayer`, `StartPlayingUsingMediaPlayer`,
  the streaming methods, `PlayNextEpisode`) returns `ErrNoMediaPlayer` when there is no repository instead
  of panicking. Playlists keep working with the native player and stream playback; local files that need
  the desktop player stop the playlist with "Failed to start playing local file".
- **Data races in the playlist manager:** with the panic gone, `go test -race -count=200` still found
  races: the playlist goroutine, `StopPlaylist` and the client event listener read and wrote the current
  playlist, episode, client id and cancel function without a lock, and the playlist goroutine read
  `m.ctx` on every loop (so it could wait on a newer playlist's context). That state now goes through
  small locked getters and setters (`stateMu`), and the goroutine keeps its own context.
  `markCurrentAsCompleted` no longer calls `m.mu.Unlock()` when no playlist is active (it unlocked a
  mutex its caller held or didn't hold). The test `FakePlatform` records update calls under a mutex.
- **Tests:** `TestPlaybackManagerWithoutMediaPlayerReturnsErrorInsteadOfPanicking`,
  `TestPlaybackManagerSetMediaPlayerRepositoryIsVisibleImmediately` and
  `TestPlaylistManagerPlayEpisodeWithoutMediaPlayer` (which panics on the old code every time).
  `TestPlaylistManagerPlayEpisodePrevious` is no longer on the known-failing list. The playlist tests
  read the manager's state through the same locked getters, and
  `go test -race -count=200 ./internal/playlist/` passes.
