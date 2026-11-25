#!/usr/bin/env sh
# Runs the checks every pull request needs to pass, the same way CI does:
#   go build ./...  ->  go vet  ->  go test (the regression suite)
#
# Usage:
#   scripts/check.sh                       # the whole module (what CI runs)
#   scripts/check.sh ./internal/profiles/... ./internal/handlers/   # vet + test only these
#
# Without a local Go toolchain, run it in the golang image (see CONTRIBUTING.md):
#   mkdir -p ~/.cache/garceslab-go && docker run --rm --dns 1.1.1.1 -u "$(id -u):$(id -g)" -e HOME=/tmp -e GOCACHE=/tmp/go/cache \
#     -e GOMODCACHE=/tmp/go/mod -v "$PWD:/src" -v "$HOME/.cache/garceslab-go:/tmp/go" -w /src \
#     golang:1.27.1 scripts/check.sh
#
# Tests listed in .github/scripts/known-failing-go-tests.txt already fail on a clean
# Seanime v3.10.5 checkout (private fixtures, desktop session, dates) and are skipped,
# as in CI. Everything else must pass.
set -eu

cd "$(dirname "$0")/.."

# main.go embeds web/; vet and tests don't need the real web build.
if [ ! -d web ]; then
    mkdir -p web
    echo ok > web/placeholder.txt
fi

if [ "$#" -eq 0 ]; then
    set -- ./...
fi

echo "==> go build ./..."
go build ./...

echo "==> go vet $*"
# Seanime v3.10.5 already fails three vet checks (assign, copylocks, defers) in code that
# hasn't been changed here; everything else is checked, as in CI.
go vet -assign=false -copylocks=false -defers=false "$@"

skip="^($(grep -v '^#' .github/scripts/known-failing-go-tests.txt | grep . | paste -sd '|' -))$"
echo "==> go test $* (skipping the known pre-existing failures)"
go test -skip "$skip" "$@"

echo "==> all checks passed"
