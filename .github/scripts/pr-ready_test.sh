#!/usr/bin/env bash
# Tests for pr-ready.sh. Run: bash .github/scripts/pr-ready_test.sh
set -uo pipefail

script="$(dirname "$0")/pr-ready.sh"
failures=0
tab=$'\t'

required=$'Build\nTests'
all_green="Build${tab}completed${tab}success
Tests${tab}completed${tab}success"
one_red="Build${tab}completed${tab}success
Tests${tab}completed${tab}failure"
one_running="Build${tab}completed${tab}success
Tests${tab}in_progress${tab}"
ready_body=$'Closes #12\n\n- [x] Tests\n- [x] Docs (n/a)'

expect() { # expect <wanted exit> <description> <title> <body> <checks> [wanted text]
    local want="$1" what="$2" out code
    out=$(PR_TITLE="$3" PR_BODY="$4" REQUIRED_CHECKS="$required" CHECK_RESULTS="$5" bash "$script" 2>&1)
    code=$?
    if [ "$code" -ne "$want" ]; then
        echo "FAIL: $what (exit $code, wanted $want)"
        echo "$out" | sed 's/^/    /'
        failures=$((failures + 1))
    elif [ -n "${6:-}" ] && ! grep -qF -- "$6" <<<"$out"; then
        echo "FAIL: $what (output lacks \"$6\")"
        echo "$out" | sed 's/^/    /'
        failures=$((failures + 1))
    else
        echo "ok: $what"
    fi
}

expect 0 "ready PR passes" "feat: x" "$ready_body" "$all_green"
expect 0 "\"Fixes #N\" counts as a linked issue" "fix: x" $'Fixes #3\n- [x] a' "$all_green"
expect 0 "\"resolves #N\" in lower case counts" "fix: x" $'resolves #3\n- [x] a' "$all_green"
expect 1 "no linked issue fails" "feat: x" $'- [x] Tests' "$all_green"
expect 1 "\"#12\" alone is not a linked issue" "feat: x" $'See #12\n- [x] Tests' "$all_green"
expect 1 "an unticked box fails" "feat: x" $'Closes #1\n- [ ] Tests' "$all_green"
expect 1 "a failed check fails" "feat: x" "$ready_body" "$one_red"
expect 1 "a running check fails (no wait)" "feat: x" "$ready_body" "$one_running"
expect 1 "a missing check fails" "feat: x" "$ready_body" "Build${tab}completed${tab}success"
expect 0 "(WIP) passes with problems" "(WIP) feat: x" $'- [ ] Tests' "$one_red"
expect 0 "(WIP) passes when everything is ready" "(WIP) feat: x" "$ready_body" "$all_green"
expect 1 "WIP not at the start does not count" "feat: x (WIP)" $'- [ ] Tests' "$all_green"

# The title becomes the squash commit on main: it must be a Conventional Commit.
not_cc="is not a Conventional Commit"
expect 0 "title passes: feat(profiles): add a PIN to profiles" 'feat(profiles): add a PIN to profiles' "$ready_body" "$all_green"
expect 0 "title passes: fix: keep the focus on the card" 'fix: keep the focus on the card' "$ready_body" "$all_green"
expect 0 "title passes: docs: squash merge policy" 'docs: squash merge policy' "$ready_body" "$all_green"
expect 0 "title passes: test: lock the card start" 'test: lock the card start' "$ready_body" "$all_green"
expect 0 "title passes: ci: nightly channel" 'ci: nightly channel' "$ready_body" "$all_green"
expect 0 "title passes: build: lower the JS for Chromium 79" 'build: lower the JS for Chromium 79' "$ready_body" "$all_green"
expect 0 "title passes: chore: tidy the scripts" 'chore: tidy the scripts' "$ready_body" "$all_green"
expect 0 "title passes: refactor(player): one source hook" 'refactor(player): one source hook' "$ready_body" "$all_green"
expect 0 "title passes: perf(webos): fewer re-renders" 'perf(webos): fewer re-renders' "$ready_body" "$all_green"
expect 0 "title passes: style: gofmt" 'style: gofmt' "$ready_body" "$all_green"
expect 0 "title passes: revert: feat(tv): seasons tabs" 'revert: feat(tv): seasons tabs' "$ready_body" "$all_green"
expect 0 "title passes: deps: update chromedp" 'deps: update chromedp' "$ready_body" "$all_green"
expect 0 "title passes: feat(mpv/iina): open in IINA" 'feat(mpv/iina): open in IINA' "$ready_body" "$all_green"
expect 0 "title passes: fix(video-core): seek" 'fix(video-core): seek' "$ready_body" "$all_green"
expect 0 "title passes: feat!: drop the old API" 'feat!: drop the old API' "$ready_body" "$all_green"
expect 0 "title passes: refactor(api)!: rename the routes" 'refactor(api)!: rename the routes' "$ready_body" "$all_green"
expect 0 "title passes: chore(deps): bump actions/checkout from 6 to 7" 'chore(deps): bump actions/checkout from 6 to 7' "$ready_body" "$all_green"
expect 0 "title passes: build(deps): bump ws from 7.5.14 to 8.23.0" 'build(deps): bump ws from 7.5.14 to 8.23.0' "$ready_body" "$all_green"
expect 0 "title passes: build(deps-dev): bump typescript from 6.0.3 to 7.0.2" 'build(deps-dev): bump typescript from 6.0.3 to 7.0.2' "$ready_body" "$all_green"
expect 0 "title passes: chore(deps): bump js-sha256 from 0.11.1 to 1.1.0 in /seanime-web" 'chore(deps): bump js-sha256 from 0.11.1 to 1.1.0 in /seanime-web' "$ready_body" "$all_green"
expect 0 "title passes: chore(deps): bump the go-minor-patch group across 1 directory with 28 updates" 'chore(deps): bump the go-minor-patch group across 1 directory with 28 updates' "$ready_body" "$all_green"
expect 1 "title fails: GA Test: permanent TEST ENVIRONMENT tag on every screen" 'GA Test: permanent TEST ENVIRONMENT tag on every screen' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: Add profiles" 'Add profiles' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: feature: x" 'feature: x' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: Feat: x" 'Feat: x' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: feat:x" 'feat:x' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: feat: " 'feat: ' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: feat" 'feat' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: feat(): x" 'feat(): x' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: feat (tv): x" 'feat (tv): x' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: webOS: keep the focus" 'webOS: keep the focus' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: wip: x" 'wip: x' "$ready_body" "$all_green" "$not_cc"
expect 1 "title fails: fix(tv) : x" 'fix(tv) : x' "$ready_body" "$all_green" "$not_cc"
expect 1 "\"(WIP)\" at the end of the title fails on its own" "feat: x (WIP)" "$ready_body" "$all_green" "the title has \"(WIP)\" in it"
expect 0 "(WIP) with a non-conventional title passes and says why it isn't ready" "(WIP) Add profiles" "$ready_body" "$all_green" "$not_cc"
expect 0 "(WIP) without a space before the type is checked without the prefix" "(WIP)feat: x" "$ready_body" "$all_green" "Everything is in place"

# Reading the check runs from GitHub: the newest run of each job wins (a re-run that passed
# replaces an older failure). A fake `gh` prints what the real one's --jq filter would.
fake_bin=$(mktemp -d)
cat >"$fake_bin/gh" <<'GH'
#!/usr/bin/env bash
printf '%s\t%s\t%s\t%s\n' \
    2026-01-01T10:00:00Z Tests completed failure \
    2026-01-01T11:00:00Z Tests completed success \
    2026-01-01T10:30:00Z Build completed success
GH
chmod +x "$fake_bin/gh"
out=$(PATH="$fake_bin:$PATH" PR_TITLE="feat: x" PR_BODY="$ready_body" REQUIRED_CHECKS="$required" \
    GITHUB_REPOSITORY=o/r HEAD_SHA=abc bash "$script" 2>&1)
if [ $? -eq 0 ]; then echo "ok: newest check run wins"; else
    echo "FAIL: newest check run wins"; echo "$out" | sed 's/^/    /'; failures=$((failures + 1)); fi
rm -rf "$fake_bin"

if [ "$failures" -gt 0 ]; then
    echo "$failures test(s) failed"
    exit 1
fi
echo "all tests passed"
