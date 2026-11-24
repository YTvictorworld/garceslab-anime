#!/usr/bin/env bash
# Regenerates the MKV fixtures of the trackprobe tests (tiny files: 1 s of video).
# multisub.mkv is copied from the TV repo (tests/fixtures/mkv/make-fixtures.sh):
# an Erai-like MultiSub release with nine ASS tracks (~400 KiB Tracks element).
# Needs Docker (ffmpeg and mkvtoolnix run in Alpine; --dns because the build PC's
# Docker has no DNS of its own).
set -euo pipefail
cd "$(dirname "$0")"
docker run --rm --dns 1.1.1.1 -v "$PWD":/w -w /w alpine:3.20 sh -c '
    set -e
    apk add --no-cache ffmpeg mkvtoolnix >/dev/null
    t=$(mktemp -d)
    m() { mkvmerge "$@" || [ $? -eq 1 ]; } # exit code 1 = warnings only
    ffmpeg -hide_banner -loglevel error -y -f lavfi -i testsrc=size=64x36:rate=12:duration=1 \
        -c:v libx264 -preset veryfast -crf 45 "$t/v.mkv"
    ffmpeg -hide_banner -loglevel error -y -f lavfi -i sine=frequency=440:duration=1 -c:a aac -b:a 16k -ac 1 "$t/a.mka"
    printf "1\n00:00:00,000 --> 00:00:00,900\nhello\n" > "$t/s.srt"
    # 3 MiB "font" (zeros, so git stores it small): attachments are skipped, never downloaded.
    head -c 3145728 /dev/zero > "$t/Big.ttf"
    # English + French subtitles only, big attachment.
    m -q -o eng-fre.mkv "$t/v.mkv" --language 0:jpn --track-name 0:Japanese "$t/a.mka" \
        --language 0:eng --track-name 0:English "$t/s.srt" \
        --language 0:fre --track-name 0:French "$t/s.srt" \
        --attachment-mime-type font/ttf --attach-file "$t/Big.ttf"
    # English full subtitles + Spanish forced signs only: no full Spanish subtitles.
    m -q -o spa-signs.mkv "$t/v.mkv" --language 0:jpn "$t/a.mka" \
        --language 0:eng --track-name 0:English "$t/s.srt" \
        --language 0:spa --track-name 0:"Signs & Songs" --forced-display-flag 0:yes "$t/s.srt"
    # Spanish dub (Latin America, language left undefined, named in the title) + English subtitles.
    m -q -o spa-dub.mkv "$t/v.mkv" --language 0:jpn --track-name 0:Japanese "$t/a.mka" \
        --language 0:und --track-name 0:"Español (Latino)" "$t/a.mka" \
        --language 0:eng --track-name 0:English "$t/s.srt"
    # Not an MKV.
    ffmpeg -hide_banner -loglevel error -y -i "$t/v.mkv" -c copy video.mp4
    chown '"$(id -u):$(id -g)"' *.mkv *.mp4
'
