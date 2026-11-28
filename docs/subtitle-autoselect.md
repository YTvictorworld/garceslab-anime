# Subtitle-aware auto-select

Debrid release names rarely say which subtitle languages a file has, so "auto-select" could
pick a release without subtitles in your language. Garceslab Anime checks.

## How it works

1. Auto-select ranks the releases by name as before (resolution, release group, filters and
   scores of the profile's auto-select settings, cached releases first).
2. For a **debrid** stream it then reads the MKV header (EBML header, SeekHead, Tracks;
   attachments and clusters are skipped) of up to `SEANIME_AUTOSELECT_PROBE_TOP` (default 4)
   **cached** candidates over HTTP Range. TorBox gives a link to one file of a cached torrent
   (`add_only_if_cached`, never a download), which is removed again unless it is the pick.
3. Among candidates with the **same resolution**, it prefers the highest-ranked one that has a
   full subtitle track (not forced, not "Signs & Songs") in the profile's preferred subtitle
   language, or a preferred dub language. Otherwise the name-based choice stays.

The preferred languages come from the requesting **profile's TV player settings**, else from
its auto-select profile's languages; without either, nothing is probed. Language codes, IETF
tags and names are normalised (`spa`, `es-419`, "Español", "Latino" and "Castellano" are all
Spanish).

While it checks, the requesting device (only that one) shows "Checking subtitle languages…
(2/4)". The decision is logged at INFO level without links.

## Track codecs

Each probe also records, per track, the Matroska **track number** and **`CodecID`**, and for
subtitle tracks a **kind**:

| Kind | CodecIDs |
|---|---|
| `text` | `S_TEXT/ASS`, `S_TEXT/SSA` (and the old `S_ASS`, `S_SSA`), `S_TEXT/UTF8` (SRT), `S_TEXT/ASCII`, `S_TEXT/WEBVTT`, `D_WEBVTT/*`, `S_TEXT/USF`, `S_HDMV/TEXTST` |
| `image` | `S_HDMV/PGS`, `S_VOBSUB`, `S_DVBSUB`, `S_IMAGE/BMP` |
| `unknown` | no `CodecID`, or any other (`S_KATE`, `S_ARIBSUB`, …) |

Auto-select doesn't use them: its choice still depends only on languages, the forced flag and
the track name. They matter for the planned **subtitle translation**: a text track can be
read and machine translated as is, an image track (PGS, VobSub, DVB) would need OCR first. The
report below tells how many releases have only image subtitles, which decides whether OCR is
worth its own phase.

Every probe (not a cache hit) logs one INFO line with the file name (never the link):

```
INF trackprobe > Subtitle tracks file="[Group] Show - 01 (1080p).mkv" tracks="en:ass, es:ass, fr:pgs" audio="ja:aac"
```

Languages are the normalised keys (`es-419` → `es`, `und` when unknown); a forced track is
marked `(forced)`, and `tracks="none"` means no subtitle track.

### Report

`seanime trackprobe-stats` reads the cache directory (read only) and prints, instead of
starting the server: the number of probed files; MKV files with a text subtitle, with image
subtitles only, without subtitles, with only unknown codecs, and v1 entries without codecs;
tracks and files per subtitle and audio `CodecID`; subtitle tracks per language × kind; and the
files with image subtitles only. In the image:

```sh
docker exec garceslab-anime /app/seanime trackprobe-stats          # text
docker exec garceslab-anime /app/seanime trackprobe-stats --json   # JSON
```

It finds the cache like the server does (`$SEANIME_DATA_DIR/track-probe`, else
`~/.config/Seanime/track-probe`, `/home/seanime/.config/Seanime/track-probe` in the image);
`--datadir DIR` or `--dir DIR` point it elsewhere. The output has release file names, so
keep it private. Example (made-up data):

```
Probed files:                     5
Not MKV:                          0
MKV files:                        5
  with a text subtitle:           2  40.0%
  image subtitles only:           2  40.0%
  no subtitles:                   1  20.0%
  unknown subtitle codec:         0  0.0%
  codec not recorded (v1 entry):  0  0.0%

Subtitle codecs  tracks  files
  S_HDMV/PGS     3       2
  S_TEXT/ASS     2       1
  S_TEXT/UTF8    1       1
  S_VOBSUB       1       1

Audio codecs  tracks  files
  A_AAC       5       5

Subtitle tracks by language  text  image  unknown
  en                         2     2      0
  es                         1     1      0
  fr                         0     1      0

Files with image subtitles only (2):
  [Group] Show - 03 [BD 1080p].mkv [torbox 01234567] en:pgs, es:pgs
  [Group] Show - 04 [DVD].mkv [torbox 01234567] en:vobsub
```

A file counts as "with a text subtitle" if any of its subtitle tracks is text (even a forced
one), and as "image subtitles only" if it has subtitles and all of them are images. Expired
entries are counted too (a file in a torrent never changes).

## Limits

- At most 2 MiB and 5 s per file, 10 s in all; link requests at least 500 ms apart; a 429 from
  the debrid service stops probing.
- Results are cached per (provider, info hash, file path) in `<data dir>/track-probe` for 180
  days, one JSON file per file, named by a hash of the key.

### Cache versions

| Version | Per track |
|---|---|
| v1 (no `version` field) | languages, name, forced flag |
| v2 (`"version": 2`) | also `number`, `codecId` and, for subtitles, `kind` |

A **v1 entry is kept**: auto-select still reads it as a hit and decides exactly as before, so
no file is probed again (that would cost a debrid link and Range requests for languages it
already has). `Cache.Get` returns it with `Tracks.CodecsUnknown` set, so a future caller that
needs codecs (subtitle translation) can probe that file again and store a v2 entry over it.
Every new probe writes v2, and v1 entries also expire after 180 days. The report counts v1
entries with subtitles as "codec not recorded"; to collect codecs for them sooner, delete
those entries (or the whole `track-probe` directory) and they are probed on the next
auto-select.
- Torrent streams are not probed (that would need data from peers).

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `SEANIME_AUTOSELECT_PROBE_TOP` | `4` | candidates to probe; `0` turns the step off, at most 10 |

## Code and tests

`internal/torrents/trackprobe` (MKV header reader; `codec.go` kinds, `cache.go` versions,
`stats.go` report, run from `main.go`), `internal/torrents/autoselect/track_select.go`
(the choice), `internal/debrid/torbox/cached_link.go` (links to cached files),
`internal/debrid/client/track_probe.go` (wiring). Tests:

```sh
go test ./internal/torrents/trackprobe/ ./internal/torrents/autoselect/ ./internal/debrid/torbox/ ./internal/debrid/client/
```

The MKV fixtures are in `internal/torrents/trackprobe/testdata/` (regenerated by
`make-fixtures.sh`). The codec tests (`codec_test.go`) build synthetic MKV headers in Go (EBML
header, Segment, Tracks, no media): ASS, SRT, PGS, VobSub, mixed, missing `CodecID`, plus the
v1 → v2 cache handling and the report.
