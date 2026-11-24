#!/usr/bin/env bash
# Phase 2 end-to-end check (docs/phase2-direct-play.md): builds the server,
# starts a throwaway instance on a free port with its own data directory and
# the Dummy Debrid provider, runs internal/e2e/phase2 (four fake devices over
# websocket + HTTP) against it, then stops it. No debrid service is called;
# anime metadata comes from AniList anonymously.
#
# Run from the repository root, with Go 1.27 (or inside golang:1.27.1):
#   scripts/e2e-phase2.sh
# Environment: E2E_PORT (default 43391), E2E_DIR (default a temp dir).
set -euo pipefail

PORT="${E2E_PORT:-43391}"
DIR="${E2E_DIR:-$(mktemp -d)}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$DIR/seanime-e2e"

mkdir -p "$DIR/data"
# The binary embeds web/; a placeholder is enough for the API.
if [ ! -e "$ROOT/web/index.html" ]; then
    mkdir -p "$ROOT/web" && echo '<!doctype html><title>e2e</title>' > "$ROOT/web/index.html"
fi
(cd "$ROOT" && go build -o "$BIN" .)

# The fixture the dummy debrid serves (any small MKV).
cp "$ROOT/internal/e2e/phase2/fixture/sample.mkv" "$DIR/data/sample.mkv"

start() {
    SEANIME_SERVER_HOST=127.0.0.1 SEANIME_SERVER_PORT="$PORT" SEANIME_VIEWER_DISCONNECT_GRACE=2s \
        "$BIN" --datadir "$DIR/data" > "$DIR/server.log" 2>&1 &
    PID=$!
    for _ in $(seq 1 120); do
        if curl -fsS "http://127.0.0.1:$PORT/api/v1/status" > /dev/null 2>&1; then return 0; fi
        sleep 0.5
    done
    echo "server did not start; log: $DIR/server.log" >&2
    tail -40 "$DIR/server.log" >&2
    return 1
}
stop() { kill "$PID" 2>/dev/null || true; wait "$PID" 2>/dev/null || true; }
trap stop EXIT

# First start writes config.toml; turn the Dummy Debrid feature on and restart.
start
stop
CFG="$DIR/data/config.toml"
if grep -qi '^dummydebrid' "$CFG"; then
    sed -i 's/^\(dummydebrid\|dummyDebrid\) *= *.*/dummydebrid = true/I' "$CFG"
else
    printf '\n[experimental]\ndummydebrid = true\n' >> "$CFG"
fi
start

cd "$ROOT"
SEANIME_E2E_URL="http://127.0.0.1:$PORT" SEANIME_E2E_FIXTURE="$DIR/data/sample.mkv" \
    go test -count=1 -tags e2e -v ./internal/e2e/phase2/
echo "server log: $DIR/server.log"
