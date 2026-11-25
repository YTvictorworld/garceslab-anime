package goja_bindings

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Needs a real Chrome/Chromium (skipped unless GARCESLAB_CHROMEDP_TESTS=1 or live tests are
// on). It checks the values that the browser methods resolve with, the ones that the
// chromedp v0.24 migration rewrote and no older test checked. It uses only the JS API, so it
// also runs unchanged against the code before the migration (it passed there too).
func TestBrowserAPI_ResultValues(t *testing.T) {
	vm, chrome, server := setupChromeVMWithServer(t)
	defer server.Close()
	defer chrome.Close()

	jsCode := fmt.Sprintf(`
		(async () => {
			const browser = await ChromeDP.newBrowser();
			const browserEvents = [];
			browser.listenBrowser((ev) => browserEvents.push(ev.method));
			await browser.navigate(%q);
			await browser.waitVisible("#title");
			const out = {
				innerHTML: await browser.innerHTML("#list"),
				outerHTML: await browser.outerHTML("#title"),
				text: await browser.text("#title"),
				idAttr: await browser.attribute("#title", "id"),
				missingAttr: await browser.attribute("#title", "data-nope"),
				evalNull: await browser.evaluate("null"),
				evalUndefined: await browser.evaluate("undefined"),
				evalObject: await browser.evaluate("({a: 1, b: [1, 'x']})"),
				cdp: await browser.executeCDP("Runtime.evaluate", {expression: "6*7", returnByValue: true}),
				shot: await browser.screenshot("#title"),
				full: await browser.fullScreenshot(),
			};
			out.browserEvents = browserEvents;
			await browser.close();
			return out;
		})();
	`, server.URL)

	result, err := vm.RunString(jsCode)
	require.NoError(t, err)
	promise := result.Export().(*goja.Promise)
	waitForPromise(t, promise, 30*time.Second)
	if promise.State() == goja.PromiseStateRejected {
		t.Fatalf("Promise was rejected: %v", promise.Result())
	}
	out := promise.Result().ToObject(vm)

	assert.Contains(t, out.Get("innerHTML").String(), "<li>Item 1</li>")
	assert.Equal(t, `<h1 id="title">Hello World</h1>`, out.Get("outerHTML").String())
	assert.Equal(t, "Hello World", out.Get("text").String())
	assert.Equal(t, "title", out.Get("idAttr").String())
	assert.True(t, goja.IsNull(out.Get("missingAttr")), "missing attribute resolves with null")
	assert.Nil(t, out.Get("evalNull").Export(), "null resolves with null, no error")
	assert.Nil(t, out.Get("evalUndefined").Export(), "undefined resolves with null, no error")
	assert.Equal(t, map[string]interface{}{"a": float64(1), "b": []interface{}{float64(1), "x"}}, out.Get("evalObject").Export())
	cdpRes, ok := out.Get("cdp").Export().(map[string]interface{})
	require.True(t, ok, "executeCDP resolves with an object, got %T", out.Get("cdp").Export())
	assert.EqualValues(t, 42, cdpRes["result"].(map[string]interface{})["value"])

	pngMagic := []byte("\x89PNG")
	for _, k := range []string{"shot", "full"} {
		buf, ok := out.Get(k).Export().([]byte)
		require.Truef(t, ok, "%s resolves with bytes, got %T", k, out.Get(k).Export())
		assert.Truef(t, bytes.HasPrefix(buf, pngMagic), "%s is a PNG", k)
	}
}
