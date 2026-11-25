<!--
Title: start it with "(WIP)" and keep the PR as a draft until EVERY box below is ticked and
CI is green. The "PR ready (WIP rule)" check enforces this (see CONTRIBUTING.md).
For a box that doesn't apply, write "n/a" next to it and tick it.
-->

Closes #

## Summary

What does this change, and why?

## How it was tested

Commands you ran, and what you checked by hand.

## Screenshots (UI changes)

| Before | After |
|---|---|
| | |

## Checklist

- [ ] Linked issue: `Closes #N` above.
- [ ] Tests added or updated: a regression test that fails before a bug fix and passes after it, or tests that lock a new feature's behaviour.
- [ ] Go: `scripts/check.sh` passes (`go build ./...`, `go vet`, `go test` on the touched packages and the whole regression suite).
- [ ] Web (if `seanime-web` changed): `npm test` and `npm run build` pass.
- [ ] UI changes: BEFORE and AFTER screenshots attached (TV at 1920×1080), and saved in `docs/images/` if they document a feature.
- [ ] Docs updated in `./docs` (and the index in `docs/README.md` for a new doc).
- [ ] No secrets or private data (IPs, server URLs, tokens, emails, real names) in code, commits or screenshots.
- [ ] `MODIFICATIONS.md` updated if the change modifies code that comes from Seanime.
