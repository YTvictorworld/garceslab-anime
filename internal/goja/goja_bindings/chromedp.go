package goja_bindings

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	gojautil "seanime/internal/util/goja"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp/jsonv2"
	"github.com/chromedp/chromedp"
	"github.com/dop251/goja"
	"github.com/rs/zerolog/log"
)

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
// ChromeDP
//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

const (
	defaultBrowserTimeout = 30 * time.Second
)

type ChromeDP struct {
	vm         *goja.Runtime
	chromeSem  chan struct{}
	responseCh chan func()
	browsers   map[string]*Browser
	browserMu  sync.RWMutex
	closeOnce  sync.Once
}

// Browser represents a browser instance
type Browser struct {
	id          string
	chromedp    *ChromeDP
	allocCtx    context.Context
	ctx         context.Context
	cancel      context.CancelFunc
	allocCancel context.CancelFunc
	timeout     time.Duration
}

func NewChromeDP(vm *goja.Runtime) *ChromeDP {
	return &ChromeDP{
		vm:         vm,
		chromeSem:  make(chan struct{}, 5), // limit concurrent browser instances
		responseCh: make(chan func(), 10),
		browsers:   make(map[string]*Browser),
	}
}

func (c *ChromeDP) ResponseChannel() <-chan func() {
	return c.responseCh
}

func (c *ChromeDP) Close() {
	defer func() {
		if r := recover(); r != nil {
		}
	}()

	// Close all browsers
	c.browserMu.Lock()
	for _, browser := range c.browsers {
		browser.close()
	}
	c.browserMu.Unlock()

	c.closeOnce.Do(func() {
		close(c.responseCh)
	})
}

type chromeOptions struct {
	Timeout      int    // seconds
	WaitSelector string // CSS selector to wait for
	WaitDuration int    // milliseconds to wait after page load
	UserAgent    string // custom user agent
	Headless     bool   // run in headless mode (default: true)
}

// BindChromeDP binds the ChromeDP utilities to the VM
func BindChromeDP(vm *goja.Runtime) *ChromeDP {
	return BindChromeDPWithScheduler(vm, nil)
}

// BindChromeDPWithScheduler binds the ChromeDP utilities to the VM
func BindChromeDPWithScheduler(vm *goja.Runtime, scheduler *gojautil.Scheduler) *ChromeDP {
	c := NewChromeDP(vm)

	// Create ChromeDP object with methods
	chromeDPObj := vm.NewObject()

	// modular API
	chromeDPObj.Set("newBrowser", c.NewBrowser)

	// one-shot functions
	chromeDPObj.Set("scrape", c.Scrape)
	chromeDPObj.Set("screenshot", c.Screenshot)
	chromeDPObj.Set("evaluate", c.Evaluate)

	vm.Set("ChromeDP", chromeDPObj)

	// Start response handler
	go func() {
		for fn := range c.ResponseChannel() {
			if scheduler != nil {
				scheduler.ScheduleAsync(func() error {
					defer func() {
						if r := recover(); r != nil {
							log.Warn().Msgf("extension: chromedp response channel panic: %v", r)
						}
					}()
					fn()
					return nil
				})
			} else {
				defer func() {
					if r := recover(); r != nil {
						log.Warn().Msgf("extension: chromedp response channel panic: %v", r)
					}
				}()
				fn()
			}
		}
	}()

	return c
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
// Browser Instance API
//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

// NewBrowser creates a new browser instance
func (c *ChromeDP) NewBrowser(call goja.FunctionCall) goja.Value {
	opts := chromeOptions{
		Timeout:  int(defaultBrowserTimeout.Seconds()),
		Headless: true,
	}

	if len(call.Arguments) > 0 {
		if optObj := call.Argument(0).ToObject(c.vm); optObj != nil {
			if v := optObj.Get("timeout"); gojaValueIsDefined(v) {
				opts.Timeout = int(v.ToInteger())
			}
			if v := optObj.Get("userAgent"); gojaValueIsDefined(v) {
				opts.UserAgent = v.String()
			}
			if v := optObj.Get("headless"); gojaValueIsDefined(v) {
				opts.Headless = v.ToBoolean()
			}
		}
	}

	promise, resolve, reject := c.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.responseCh <- func() {
					reject(NewErrorString(c.vm, fmt.Sprintf("chromedp panic: %v", r)))
				}
			}
		}()

		c.chromeSem <- struct{}{}

		browser, err := c.createBrowser(opts)
		if err != nil {
			<-c.chromeSem
			c.responseCh <- func() {
				reject(NewError(c.vm, err))
			}
			return
		}

		c.responseCh <- func() {
			resolve(c.newBrowserObject(browser))
		}
	}()

	return c.vm.ToValue(promise)
}

// newBrowserObject builds the JS object that ChromeDP.newBrowser() resolves with. It is
// split out of NewBrowser so that a test can check the method names that extensions see
// without starting a browser (see chromedp_surface_test.go).
func (c *ChromeDP) newBrowserObject(browser *Browser) *goja.Object {
	browserObj := c.vm.NewObject()
	browserObj.Set("navigate", browser.Navigate)
	browserObj.Set("waitVisible", browser.WaitVisible)
	browserObj.Set("waitReady", browser.WaitReady)
	browserObj.Set("click", browser.Click)
	browserObj.Set("sendKeys", browser.SendKeys)
	browserObj.Set("evaluate", browser.EvaluateJS)
	browserObj.Set("innerHTML", browser.InnerHTML)
	browserObj.Set("outerHTML", browser.OuterHTML)
	browserObj.Set("text", browser.Text)
	browserObj.Set("attribute", browser.Attribute)
	browserObj.Set("screenshot", browser.Screenshot)
	browserObj.Set("fullScreenshot", browser.FullScreenshot)
	browserObj.Set("sleep", browser.Sleep)
	browserObj.Set("close", browser.Close)
	browserObj.Set("listenBrowser", browser.ListenBrowser)
	browserObj.Set("listenTarget", browser.ListenTarget)
	browserObj.Set("executeCDP", browser.ExecuteCDP)
	return browserObj
}

func (c *ChromeDP) createBrowser(opts chromeOptions) (*Browser, error) {
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", opts.Headless),
	)

	if opts.UserAgent != "" {
		allocOpts = append(allocOpts, chromedp.UserAgent(opts.UserAgent))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	browser := &Browser{
		id:          fmt.Sprintf("browser-%d", time.Now().UnixNano()),
		chromedp:    c,
		allocCtx:    allocCtx,
		ctx:         ctx,
		cancel:      cancel,
		allocCancel: allocCancel,
		timeout:     time.Duration(opts.Timeout) * time.Second,
	}

	// chromedp v0.24: Do with no steps starts the browser and opens the tab (was Run(ctx)).
	if err := chromedp.Do(ctx); err != nil {
		cancel()
		allocCancel()
		return nil, fmt.Errorf("failed to start browser: %w", err)
	}

	c.browserMu.Lock()
	c.browsers[browser.id] = browser
	c.browserMu.Unlock()

	return browser, nil
}

func (b *Browser) close() {
	if b.cancel != nil {
		b.cancel()
	}
	if b.allocCancel != nil {
		b.allocCancel()
	}
}

// Navigate navigates to a URL
func (b *Browser) Navigate(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "navigate requires a URL argument")
	}

	url := call.Argument(0).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("navigate panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		err := chromedp.Do(ctx, chromedp.Navigate(url))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(goja.Undefined())
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// WaitVisible waits for a selector to be visible
func (b *Browser) WaitVisible(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "waitVisible requires a selector argument")
	}

	selector := call.Argument(0).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("waitVisible panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		err := chromedp.Do(ctx, chromedp.WaitVisible(selector))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(goja.Undefined())
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// WaitReady waits for the page to be ready
func (b *Browser) WaitReady(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "waitReady requires a selector argument")
	}

	selector := call.Argument(0).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("waitReady panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		err := chromedp.Do(ctx, chromedp.WaitReady(selector))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(goja.Undefined())
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// Click clicks on an element
func (b *Browser) Click(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "click requires a selector argument")
	}

	selector := call.Argument(0).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("click panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		err := chromedp.Do(ctx, chromedp.Click(selector))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(goja.Undefined())
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// SendKeys types into an element
func (b *Browser) SendKeys(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		PanicThrowErrorString(b.chromedp.vm, "sendKeys requires selector and keys arguments")
	}

	selector := call.Argument(0).String()
	keys := call.Argument(1).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("sendKeys panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		err := chromedp.Do(ctx, chromedp.SendKeys(selector, keys))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(goja.Undefined())
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// EvaluateJS evaluates JavaScript in the browser context
func (b *Browser) EvaluateJS(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "evaluate requires a JavaScript expression")
	}

	jsCode := call.Argument(0).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("evaluate panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		wrappedJS := jsCode
		if !strings.Contains(jsCode, "return") {
			wrappedJS = fmt.Sprintf("(function() { return %s; })()", jsCode)
		}

		// Evaluate[any]: an interface can be nil, so null/undefined still give nil (no
		// ErrJSNull), as the old Evaluate(expr, &result) did.
		result, err := chromedp.Run(ctx, chromedp.Evaluate[any](wrappedJS))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(b.chromedp.vm.ToValue(result))
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// InnerHTML gets the inner HTML of an element
func (b *Browser) InnerHTML(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "innerHTML requires a selector argument")
	}

	selector := call.Argument(0).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("innerHTML panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		html, err := chromedp.Run(ctx, chromedp.InnerHTML(selector))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(b.chromedp.vm.ToValue(html))
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// OuterHTML gets the outer HTML of an element
func (b *Browser) OuterHTML(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "outerHTML requires a selector argument")
	}

	selector := call.Argument(0).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("outerHTML panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		html, err := chromedp.Run(ctx, chromedp.OuterHTML(selector))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(b.chromedp.vm.ToValue(html))
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// Text gets the text content of an element
func (b *Browser) Text(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "text requires a selector argument")
	}

	selector := call.Argument(0).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("text panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		text, err := chromedp.Run(ctx, chromedp.Text(selector))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(b.chromedp.vm.ToValue(text))
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// Attribute gets an attribute value of an element
func (b *Browser) Attribute(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		PanicThrowErrorString(b.chromedp.vm, "attribute requires selector and attribute name arguments")
	}

	selector := call.Argument(0).String()
	attrName := call.Argument(1).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("attribute panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		attr, err := chromedp.Run(ctx, chromedp.AttributeValue(selector, attrName))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			if attr.Exists {
				resolve(b.chromedp.vm.ToValue(attr.Value))
			} else {
				resolve(goja.Null())
			}
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// Screenshot captures a screenshot of a specific element
func (b *Browser) Screenshot(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "screenshot requires a selector argument")
	}

	selector := call.Argument(0).String()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("screenshot panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		buf, err := chromedp.Run(ctx, chromedp.Screenshot(selector))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(b.chromedp.vm.ToValue(buf))
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// FullScreenshot captures a full page screenshot
func (b *Browser) FullScreenshot(call goja.FunctionCall) goja.Value {
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("fullScreenshot panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		buf, err := chromedp.Run(ctx, chromedp.FullScreenshot(100))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(b.chromedp.vm.ToValue(buf))
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// Sleep waits for a duration
func (b *Browser) Sleep(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "sleep requires a duration in milliseconds")
	}

	duration := call.Argument(0).ToInteger()
	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("sleep panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		err := chromedp.Do(ctx, chromedp.Sleep(time.Duration(duration)*time.Millisecond))
		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(goja.Undefined())
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// Close closes the browser instance
func (b *Browser) Close(call goja.FunctionCall) goja.Value {
	promise, resolve, _ := b.chromedp.vm.NewPromise()

	go func() {
		b.chromedp.browserMu.Lock()
		delete(b.chromedp.browsers, b.id)
		b.chromedp.browserMu.Unlock()

		b.close()
		<-b.chromedp.chromeSem

		b.chromedp.responseCh <- func() {
			resolve(goja.Undefined())
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// ListenBrowser registers a callback to listen to browser-level events
func (b *Browser) ListenBrowser(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "listenBrowser requires a callback function")
	}

	callback, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		PanicThrowErrorString(b.chromedp.vm, "listenBrowser argument must be a function")
	}

	err := chromedp.ListenBrowser(b.ctx, func(ev interface{}) {
		method := getEventMethod(ev)
		params := eventParams(ev)

		b.chromedp.responseCh <- func() {
			defer func() {
				if r := recover(); r != nil {
					log.Warn().Msgf("extension: chromedp listenBrowser callback panic: %v", r)
				}
			}()
			eventObj := b.chromedp.vm.NewObject()
			_ = eventObj.Set("method", method)
			_ = eventObj.Set("params", params)
			_, _ = callback(goja.Undefined(), eventObj)
		}
	})
	if err != nil {
		// v0.24 returns an error when it can't reach the browser or tab (the old func
		// returned nothing). Keep the JS contract, which never threw here: log it.
		log.Warn().Err(err).Msg("extension: chromedp could not register the event listener")
	}

	return goja.Undefined()
}

// ListenTarget registers a callback to listen to target-level events
func (b *Browser) ListenTarget(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "listenTarget requires a callback function")
	}

	callback, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		PanicThrowErrorString(b.chromedp.vm, "listenTarget argument must be a function")
	}

	// chromedp v0.24 renamed ListenTarget to Listen; it calls fn with the same typed event
	// structs (*network.EventRequestWillBeSent, ...).
	err := chromedp.Listen(b.ctx, func(ev interface{}) {
		method := getEventMethod(ev)
		params := eventParams(ev)

		b.chromedp.responseCh <- func() {
			defer func() {
				if r := recover(); r != nil {
					log.Warn().Msgf("extension: chromedp listenTarget callback panic: %v", r)
				}
			}()
			eventObj := b.chromedp.vm.NewObject()
			_ = eventObj.Set("method", method)
			_ = eventObj.Set("params", params)
			_, _ = callback(goja.Undefined(), eventObj)
		}
	})
	if err != nil {
		// v0.24 returns an error when it can't reach the browser or tab (the old func
		// returned nothing). Keep the JS contract, which never threw here: log it.
		log.Warn().Err(err).Msg("extension: chromedp could not register the event listener")
	}

	return goja.Undefined()
}

// ExecuteCDP executes an arbitrary CDP command and returns a promise
func (b *Browser) ExecuteCDP(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(b.chromedp.vm, "executeCDP requires at least a method argument")
	}

	method := call.Argument(0).String()
	var params interface{}
	if len(call.Arguments) > 1 {
		params = call.Argument(1).Export()
	}

	promise, resolve, reject := b.chromedp.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				b.chromedp.responseCh <- func() {
					reject(NewErrorString(b.chromedp.vm, fmt.Sprintf("executeCDP panic: %v", r)))
				}
			}
		}()

		ctx, cancel := context.WithTimeout(b.ctx, b.timeout)
		defer cancel()

		// chromedp v0.24 has no executor in the context; the action's target is the
		// session, and Target.Call sends a raw method with JSON params.
		resVal, err := chromedp.Run(ctx, func(ctx context.Context, t *chromedp.Target) (interface{}, error) {
			return callRawCDP(ctx, t, method, params)
		})

		if err != nil {
			b.chromedp.responseCh <- func() {
				reject(NewError(b.chromedp.vm, err))
			}
			return
		}

		b.chromedp.responseCh <- func() {
			resolve(b.chromedp.vm.ToValue(resVal))
		}
	}()

	return b.chromedp.vm.ToValue(promise)
}

// rawCDPCaller is the part of *chromedp.Target that executeCDP uses (an interface so tests
// can check the result decoding without a browser).
type rawCDPCaller interface {
	Call(ctx context.Context, method string, params, res any) error
}

// callRawCDP sends method with params and decodes the result into plain Go values (maps,
// slices, float64, string, bool, nil) for goja, as the old executor-based code did.
func callRawCDP(ctx context.Context, s rawCDPCaller, method string, params interface{}) (interface{}, error) {
	var result jsonv2.Value
	if err := s.Call(ctx, method, params, &result); err != nil {
		return nil, err
	}
	var resVal interface{}
	if len(result) > 0 {
		_ = json.Unmarshal(result, &resVal)
	}
	return resVal, nil
}

// eventParams turns a typed cdproto event into the "params" object that listenTarget and
// listenBrowser callbacks get. cdproto v0.160 types marshal with encoding/json/v2 tags and
// options, so use chromedp's own marshal options to get the protocol's wire names
// (requestId, frameId, ...), exactly what Chrome sent.
func eventParams(ev interface{}) map[string]interface{} {
	var params map[string]interface{}
	if evBytes, err := jsonv2.Marshal(ev, chromedp.DefaultMarshalOptions); err == nil {
		_ = json.Unmarshal(evBytes, &params)
	}
	return params
}

func getEventMethod(ev interface{}) string {
	if ev == nil {
		return ""
	}
	t := reflect.TypeOf(ev)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return t.String()
	}
	name := t.Name()
	pkg := t.PkgPath()

	pkgName := ""
	if idx := strings.LastIndex(pkg, "/"); idx != -1 {
		pkgName = pkg[idx+1:]
	} else {
		pkgName = pkg
	}

	if strings.HasPrefix(name, "Event") && pkgName != "" {
		domain := strings.Title(pkgName)
		eventName := strings.TrimPrefix(name, "Event")
		if len(eventName) > 0 {
			eventName = strings.ToLower(eventName[:1]) + eventName[1:]
		}
		return domain + "." + eventName
	}

	return pkgName + "." + name
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
// One-Shot Functions
//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

// Scrape navigates to a URL and returns the HTML content
func (c *ChromeDP) Scrape(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(c.vm, "scrape requires at least a URL argument")
	}

	url := call.Argument(0).String()

	// Parse options
	opts := chromeOptions{
		Timeout:  int(defaultBrowserTimeout.Seconds()),
		Headless: true,
	}

	if len(call.Arguments) > 1 {
		if optObj := call.Argument(1).ToObject(c.vm); optObj != nil {
			if v := optObj.Get("timeout"); gojaValueIsDefined(v) {
				opts.Timeout = int(v.ToInteger())
			}
			if v := optObj.Get("waitSelector"); gojaValueIsDefined(v) {
				opts.WaitSelector = v.String()
			}
			if v := optObj.Get("waitDuration"); gojaValueIsDefined(v) {
				opts.WaitDuration = int(v.ToInteger())
			}
			if v := optObj.Get("userAgent"); gojaValueIsDefined(v) {
				opts.UserAgent = v.String()
			}
			if v := optObj.Get("headless"); gojaValueIsDefined(v) {
				opts.Headless = v.ToBoolean()
			}
		}
	}

	promise, resolve, reject := c.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.responseCh <- func() {
					reject(NewErrorString(c.vm, fmt.Sprintf("chromedp panic: %v", r)))
				}
			}
		}()

		c.chromeSem <- struct{}{}
		defer func() { <-c.chromeSem }()

		html, err := c.scrapeURL(url, opts)
		if err != nil {
			c.responseCh <- func() {
				reject(NewError(c.vm, err))
			}
			return
		}

		c.responseCh <- func() {
			resolve(c.vm.ToValue(html))
		}
	}()

	return c.vm.ToValue(promise)
}

// Screenshot captures a screenshot of a webpage
func (c *ChromeDP) Screenshot(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 1 {
		PanicThrowErrorString(c.vm, "screenshot requires at least a URL argument")
	}

	url := call.Argument(0).String()

	opts := chromeOptions{
		Timeout:  int(defaultBrowserTimeout.Seconds()),
		Headless: true,
	}

	if len(call.Arguments) > 1 {
		if optObj := call.Argument(1).ToObject(c.vm); optObj != nil {
			if v := optObj.Get("timeout"); gojaValueIsDefined(v) {
				opts.Timeout = int(v.ToInteger())
			}
			if v := optObj.Get("waitSelector"); gojaValueIsDefined(v) {
				opts.WaitSelector = v.String()
			}
			if v := optObj.Get("waitDuration"); gojaValueIsDefined(v) {
				opts.WaitDuration = int(v.ToInteger())
			}
			if v := optObj.Get("userAgent"); gojaValueIsDefined(v) {
				opts.UserAgent = v.String()
			}
			if v := optObj.Get("headless"); gojaValueIsDefined(v) {
				opts.Headless = v.ToBoolean()
			}
		}
	}

	promise, resolve, reject := c.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.responseCh <- func() {
					reject(NewErrorString(c.vm, fmt.Sprintf("chromedp panic: %v", r)))
				}
			}
		}()

		c.chromeSem <- struct{}{}
		defer func() { <-c.chromeSem }()

		screenshot, err := c.screenshotURL(url, opts)
		if err != nil {
			c.responseCh <- func() {
				reject(NewError(c.vm, err))
			}
			return
		}

		c.responseCh <- func() {
			resolve(c.vm.ToValue(screenshot))
		}
	}()

	return c.vm.ToValue(promise)
}

// Evaluate runs JavaScript code in the browser context and returns the result
func (c *ChromeDP) Evaluate(call goja.FunctionCall) goja.Value {
	if len(call.Arguments) < 2 {
		PanicThrowErrorString(c.vm, "evaluate requires a URL and a JavaScript expression")
	}

	url := call.Argument(0).String()
	jsCode := call.Argument(1).String()

	opts := chromeOptions{
		Timeout:  int(defaultBrowserTimeout.Seconds()),
		Headless: true,
	}

	if len(call.Arguments) > 2 {
		if optObj := call.Argument(2).ToObject(c.vm); optObj != nil {
			if v := optObj.Get("timeout"); gojaValueIsDefined(v) {
				opts.Timeout = int(v.ToInteger())
			}
			if v := optObj.Get("waitSelector"); gojaValueIsDefined(v) {
				opts.WaitSelector = v.String()
			}
			if v := optObj.Get("waitDuration"); gojaValueIsDefined(v) {
				opts.WaitDuration = int(v.ToInteger())
			}
			if v := optObj.Get("userAgent"); gojaValueIsDefined(v) {
				opts.UserAgent = v.String()
			}
			if v := optObj.Get("headless"); gojaValueIsDefined(v) {
				opts.Headless = v.ToBoolean()
			}
		}
	}

	promise, resolve, reject := c.vm.NewPromise()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.responseCh <- func() {
					reject(NewErrorString(c.vm, fmt.Sprintf("chromedp panic: %v", r)))
				}
			}
		}()

		c.chromeSem <- struct{}{}
		defer func() { <-c.chromeSem }()

		result, err := c.evaluateJS(url, jsCode, opts)
		if err != nil {
			c.responseCh <- func() {
				reject(NewError(c.vm, err))
			}
			return
		}

		c.responseCh <- func() {
			resolve(c.vm.ToValue(result))
		}
	}()

	return c.vm.ToValue(promise)
}

//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////
// Helper functions
//////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////////

func (c *ChromeDP) scrapeURL(url string, opts chromeOptions) (string, error) {
	// Create context with options
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", opts.Headless),
	)

	if opts.UserAgent != "" {
		allocOpts = append(allocOpts, chromedp.UserAgent(opts.UserAgent))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.Timeout)*time.Second)
	defer cancel()

	var tasks []chromedp.Action[chromedp.Void]

	// Navigate to URL
	tasks = append(tasks, chromedp.Navigate(url))

	// Wait for selector if provided
	if opts.WaitSelector != "" {
		tasks = append(tasks, chromedp.WaitVisible(opts.WaitSelector))
	}

	// Wait for additional duration if specified
	if opts.WaitDuration > 0 {
		tasks = append(tasks, chromedp.Sleep(time.Duration(opts.WaitDuration)*time.Millisecond))
	}

	// Get the outer HTML
	html, err := chromedp.Run(ctx, stepsThen(tasks, chromedp.OuterHTML("html")))
	if err != nil {
		return "", fmt.Errorf("chromedp run failed: %w", err)
	}

	return html, nil
}

func (c *ChromeDP) screenshotURL(url string, opts chromeOptions) ([]byte, error) {
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", opts.Headless),
	)

	if opts.UserAgent != "" {
		allocOpts = append(allocOpts, chromedp.UserAgent(opts.UserAgent))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.Timeout)*time.Second)
	defer cancel()

	var tasks []chromedp.Action[chromedp.Void]

	tasks = append(tasks, chromedp.Navigate(url))

	if opts.WaitSelector != "" {
		tasks = append(tasks, chromedp.WaitVisible(opts.WaitSelector))
	}

	if opts.WaitDuration > 0 {
		tasks = append(tasks, chromedp.Sleep(time.Duration(opts.WaitDuration)*time.Millisecond))
	}

	buf, err := chromedp.Run(ctx, stepsThen(tasks, chromedp.FullScreenshot(100)))
	if err != nil {
		return nil, fmt.Errorf("chromedp screenshot failed: %w", err)
	}

	return buf, nil
}

func (c *ChromeDP) evaluateJS(url string, jsCode string, opts chromeOptions) (interface{}, error) {
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", opts.Headless),
	)

	if opts.UserAgent != "" {
		allocOpts = append(allocOpts, chromedp.UserAgent(opts.UserAgent))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.Timeout)*time.Second)
	defer cancel()

	var tasks []chromedp.Action[chromedp.Void]

	tasks = append(tasks, chromedp.Navigate(url))

	if opts.WaitSelector != "" {
		tasks = append(tasks, chromedp.WaitVisible(opts.WaitSelector))
	}

	if opts.WaitDuration > 0 {
		tasks = append(tasks, chromedp.Sleep(time.Duration(opts.WaitDuration)*time.Millisecond))
	}

	// Wrap the JS code to ensure it returns a value
	wrappedJS := jsCode
	if !strings.Contains(jsCode, "return") {
		wrappedJS = fmt.Sprintf("(function() { return %s; })()", jsCode)
	}

	result, err := chromedp.Run(ctx, stepsThen(tasks, chromedp.Evaluate[any](wrappedJS)))
	if err != nil {
		return nil, fmt.Errorf("chromedp evaluate failed: %w", err)
	}

	return result, nil
}

// stepsThen runs the steps and then final in one action on the same tab, like the old
// chromedp.Run(ctx, steps..., final) did before chromedp v0.24 made actions return values.
func stepsThen[T any](steps []chromedp.Action[chromedp.Void], final chromedp.Action[T]) chromedp.Action[T] {
	return func(ctx context.Context, t *chromedp.Target) (T, error) {
		if _, err := chromedp.Steps(steps...)(ctx, t); err != nil {
			var zero T
			return zero, err
		}
		return final(ctx, t)
	}
}
