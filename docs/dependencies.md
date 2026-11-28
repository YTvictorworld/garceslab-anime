# Dependencies and updates

Go modules (`go.mod`), the web interface's npm packages (`seanime-web/package.json`), GitHub
Actions and the Docker base images are kept up to date by Dependabot
([`.github/dependabot.yml`](../.github/dependabot.yml)), once a week:

- Minor and patch updates of each ecosystem come grouped in one pull request
  (`go-minor-patch`, `npm-minor-patch`). Major updates come one per pull request.
- A Dependabot pull request follows the same rules as any other (CI green, see
  [CONTRIBUTING.md](../CONTRIBUTING.md)). When an update needs code changes, it gets an issue
  and a pull request of its own that carries the update and the changes, and the Dependabot
  pull request is closed as superseded.
- Dependabot's titles (`chore(deps): bump …`) are already Conventional Commits, which the
  *PR ready* check requires because the title becomes the squash commit on `main`
  ([Merging](../CONTRIBUTING.md#merging-squash-only)).
- Go modules below 1.0 (`v0.x`) can break their API in a "minor" update, so the minor group
  can need code changes too. chromedp is the example below.

## chromedp (browser automation for extensions)

[chromedp](https://github.com/chromedp/chromedp) drives a headless Chrome/Chromium. Only the
extension (goja plugin) API uses it: the `ChromeDP` global that extensions call, in
[`internal/goja/goja_bindings/chromedp.go`](../internal/goja/goja_bindings/chromedp.go).
The server itself never starts a browser. An extension that uses `ChromeDP` needs Chrome or
Chromium on the machine that runs the server. The Docker image doesn't include one.

### v0.16 to v0.24 (2026-11)

chromedp v0.24.0 (with `github.com/chromedp/cdproto` v0.160.8) rewrote its Go API
(upstream's `docs/MIGRATION.md` lists every change):

| v0.16 | v0.24 |
|---|---|
| `chromedp.Run(ctx, actions...) error`, values through pointers (`InnerHTML(sel, &html)`) | `Run[T](ctx, Action[T]) (T, error)` returns the value; `Do(ctx, steps...)` for actions without one |
| `chromedp.Run(ctx)` starts the browser | `chromedp.Do(ctx)` |
| `AttributeValue(sel, name, &value, &ok)` | `AttributeValue(sel, name)` returns `{Value, Exists}` |
| `Evaluate(expr, &result)` | `Evaluate[any](expr)` (null and undefined still give `nil`, no error) |
| `ListenTarget(ctx, fn)` | `Listen(ctx, fn) error` (same typed event structs) |
| `cdp.ExecutorFromContext(ctx).Execute(...)` | the action's `*chromedp.Target` has `Call(ctx, method, params, res)` |
| Chrome connected over a websocket on a random port | over a pipe (`--remote-debugging-pipe`), no port opened |

The bindings were moved to the new API. **What extensions see is unchanged**: the same
`ChromeDP.newBrowser / scrape / screenshot / evaluate` functions, the same 17 methods on a
browser object (`navigate`, `waitVisible`, `waitReady`, `click`, `sendKeys`, `evaluate`,
`innerHTML`, `outerHTML`, `text`, `attribute`, `screenshot`, `fullScreenshot`, `sleep`,
`close`, `listenBrowser`, `listenTarget`, `executeCDP`), the same arguments, argument-error
messages and result values (strings, `null` for a missing attribute, PNG bytes for screenshots,
`{method, params}` events with the protocol's field names, plain objects from `executeCDP`).
Two small differences that extensions can't rely on anyway: Chrome is reached through a pipe
instead of a local port, and `listenTarget`/`listenBrowser` log a warning (instead of doing
nothing) if they can't reach the browser. Error messages that come from chromedp or Chrome
(timeouts, page load errors) can be worded differently.

### Tests

- `chromedp_surface_test.go` runs in CI without a browser. It locks the binding surface (the
  names of the `ChromeDP` functions and of the browser methods; change that list only with a
  note for extension authors), the argument errors, the event names and `params` of typed
  cdproto events, the decoding of `executeCDP` results and the order of the one-shot steps.
- `chromedp_test.go`, `chromedp_browser_test.go` and `chromedp_values_test.go` start a real
  browser and are skipped unless you ask for them, either with `GARCESLAB_CHROMEDP_TESTS=1`
  (only these tests) or with `flags.enable_live_tests = true` in `test/config.toml` (all live
  tests):

  ```sh
  # Chrome or Chromium in PATH (chromium, google-chrome, ...)
  GARCESLAB_CHROMEDP_TESTS=1 go test -run 'ChromeDP|BrowserAPI' ./internal/goja/goja_bindings/
  ```

  In a container, Chromium needs `--no-sandbox`: put a small `chromium` wrapper that runs
  `/usr/bin/chromium --no-sandbox "$@"` first in `PATH`, and give the container
  `--shm-size=1g`.
