package goja_bindings

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/dop251/goja"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests lock the JS API that extensions (goja plugins) get from the ChromeDP
// bindings, so a chromedp upgrade (v0.16 -> v0.24 rewrote its Go API) can't change it
// silently. The tests in this first part need no browser and run in CI.

// The JS names are the extension API: never change this list without a migration note.
var (
	wantChromeDPGlobalMethods = []string{"evaluate", "newBrowser", "scrape", "screenshot"}
	wantBrowserMethods        = []string{
		"attribute", "click", "close", "evaluate", "executeCDP", "fullScreenshot", "innerHTML",
		"listenBrowser", "listenTarget", "navigate", "outerHTML", "screenshot", "sendKeys",
		"sleep", "text", "waitReady", "waitVisible",
	}
)

func sortedFunctionKeys(t *testing.T, obj *goja.Object) []string {
	t.Helper()
	keys := obj.Keys()
	for _, k := range keys {
		_, ok := goja.AssertFunction(obj.Get(k))
		assert.Truef(t, ok, "%q must be a function", k)
	}
	slices.Sort(keys)
	return keys
}

func TestChromeDPBindingSurface_GlobalObject(t *testing.T) {
	vm := goja.New()
	c := BindChromeDP(vm)
	defer c.Close()

	obj := vm.Get("ChromeDP")
	require.NotNil(t, obj, "ChromeDP global must exist")
	assert.Equal(t, wantChromeDPGlobalMethods, sortedFunctionKeys(t, obj.ToObject(vm)))
}

func TestChromeDPBindingSurface_BrowserObject(t *testing.T) {
	vm := goja.New()
	c := NewChromeDP(vm)
	obj := c.newBrowserObject(&Browser{chromedp: c, ctx: context.Background(), timeout: time.Second})
	assert.Equal(t, wantBrowserMethods, sortedFunctionKeys(t, obj))
}

// Argument validation runs before any browser call, so it works on a browser object that
// was never started. The messages are part of what extensions see.
func TestChromeDPBindingSurface_ArgumentErrors(t *testing.T) {
	vm := goja.New()
	c := NewChromeDP(vm)
	require.NoError(t, vm.Set("b", c.newBrowserObject(&Browser{chromedp: c, ctx: context.Background(), timeout: time.Second})))

	cases := map[string]string{
		"b.navigate()":           "navigate requires a URL argument",
		"b.waitVisible()":        "waitVisible requires a selector argument",
		"b.waitReady()":          "waitReady requires a selector argument",
		"b.click()":              "click requires a selector argument",
		"b.sendKeys('#a')":       "sendKeys requires selector and keys arguments",
		"b.evaluate()":           "evaluate requires a JavaScript expression",
		"b.innerHTML()":          "innerHTML requires a selector argument",
		"b.outerHTML()":          "outerHTML requires a selector argument",
		"b.text()":               "text requires a selector argument",
		"b.attribute('#a')":      "attribute requires selector and attribute name arguments",
		"b.screenshot()":         "screenshot requires a selector argument",
		"b.sleep()":              "sleep requires a duration in milliseconds",
		"b.listenBrowser()":      "listenBrowser requires a callback function",
		"b.listenBrowser(1)":     "listenBrowser argument must be a function",
		"b.listenTarget()":       "listenTarget requires a callback function",
		"b.listenTarget('x')":    "listenTarget argument must be a function",
		"b.executeCDP()":         "executeCDP requires at least a method argument",
		"ChromeDP_scrape()":      "scrape requires at least a URL argument",
		"ChromeDP_screenshot()":  "screenshot requires at least a URL argument",
		"ChromeDP_evaluate('u')": "evaluate requires a URL and a JavaScript expression",
	}
	require.NoError(t, vm.Set("ChromeDP_scrape", c.Scrape))
	require.NoError(t, vm.Set("ChromeDP_screenshot", c.Screenshot))
	require.NoError(t, vm.Set("ChromeDP_evaluate", c.Evaluate))

	for code, want := range cases {
		t.Run(code, func(t *testing.T) {
			_, err := vm.RunString(code)
			require.Error(t, err)
			assert.Contains(t, err.Error(), want)
		})
	}
}

// listenTarget/listenBrowser callbacks get {method, params}; the method name is derived
// from the event type, which moved to cdproto v0.160's typed structs.
func TestGetEventMethod_TypedCDProto(t *testing.T) {
	assert.Equal(t, "Page.loadEventFired", getEventMethod(&page.EventLoadEventFired{}))
	assert.Equal(t, "Network.requestWillBeSent", getEventMethod(&network.EventRequestWillBeSent{}))
	assert.Equal(t, "Network.responseReceived", getEventMethod(&network.EventResponseReceived{}))
	assert.Equal(t, "Target.targetCreated", getEventMethod(&target.EventTargetCreated{}))
	assert.Equal(t, "", getEventMethod(nil))
}

func TestEventParams_UsesProtocolWireNames(t *testing.T) {
	ev := &network.EventRequestWillBeSent{
		RequestID: "42.1",
		Request:   &network.Request{URL: "http://192.0.2.1/page", Method: "GET"},
		Type:      network.ResourceTypeDocument,
	}
	params := eventParams(ev)
	require.NotNil(t, params)
	assert.Equal(t, "42.1", params["requestId"])
	assert.Equal(t, "Document", params["type"])
	req, ok := params["request"].(map[string]interface{})
	require.True(t, ok, "request must be an object, got %T", params["request"])
	assert.Equal(t, "http://192.0.2.1/page", req["url"])
	assert.Equal(t, "GET", req["method"])
}

type fakeCDPCaller struct {
	method string
	params any
	reply  string
	err    error
}

func (f *fakeCDPCaller) Call(_ context.Context, method string, params, res any) error {
	f.method, f.params = method, params
	if f.err != nil {
		return f.err
	}
	if f.reply == "" {
		return nil
	}
	// Same decoding as chromedp's Target.Call.
	return jsonv2.Unmarshal([]byte(f.reply), res, chromedp.DefaultUnmarshalOptions)
}

func TestCallRawCDP_DecodesResultToPlainValues(t *testing.T) {
	f := &fakeCDPCaller{reply: `{"result":{"type":"number","value":2},"list":["a",1,true,null]}`}
	params := map[string]interface{}{"expression": "1+1", "returnByValue": true}

	got, err := callRawCDP(context.Background(), f, "Runtime.evaluate", params)
	require.NoError(t, err)
	assert.Equal(t, "Runtime.evaluate", f.method)
	assert.Equal(t, params, f.params)
	assert.Equal(t, map[string]interface{}{
		"result": map[string]interface{}{"type": "number", "value": float64(2)},
		"list":   []interface{}{"a", float64(1), true, nil},
	}, got)
}

func TestCallRawCDP_EmptyResultAndErrors(t *testing.T) {
	got, err := callRawCDP(context.Background(), &fakeCDPCaller{}, "Network.enable", nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	boom := errors.New("boom")
	_, err = callRawCDP(context.Background(), &fakeCDPCaller{err: boom}, "Network.enable", nil)
	assert.ErrorIs(t, err, boom)
}

// stepsThen replaces chromedp.Run(ctx, steps..., final): same order, stops at the first error.
func TestStepsThen_OrderAndErrors(t *testing.T) {
	var order []string
	step := func(name string, err error) chromedp.Action[chromedp.Void] {
		return func(context.Context, *chromedp.Target) (chromedp.Void, error) {
			order = append(order, name)
			return chromedp.Void{}, err
		}
	}
	final := func(context.Context, *chromedp.Target) (string, error) {
		order = append(order, "final")
		return "html", nil
	}

	got, err := stepsThen([]chromedp.Action[chromedp.Void]{step("navigate", nil), step("wait", nil)}, final)(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, "html", got)
	assert.Equal(t, []string{"navigate", "wait", "final"}, order)

	order = nil
	boom := errors.New("page load error")
	got, err = stepsThen([]chromedp.Action[chromedp.Void]{step("navigate", boom), step("wait", nil)}, final)(context.Background(), nil)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, "", got)
	assert.Equal(t, []string{"navigate"}, order)
}
