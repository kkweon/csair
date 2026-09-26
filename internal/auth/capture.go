package auth

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Exchange is one /ita/rest request the booking page made, with its response.
type Exchange struct {
	Method   string
	URL      string
	PostData string
	Status   int64
	Body     string
}

// Capture opens the booking deep link for origin→dest on date (YYYYMMDD) in
// Chrome and records every /ita/rest/ XHR the page itself sends, with the
// response body. Diagnostic only: it shows what the real site sends so the
// CLI's request shape can be compared against it.
func (b *BrowserProvider) Capture(ctx context.Context, origin, dest, date string, wait time.Duration) ([]Exchange, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", b.Headless),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("lang", "zh-CN,zh"),
		chromedp.NoSandbox,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.WindowSize(1280, 800),
		chromedp.UserAgent(browserUA),
	)
	if path := os.Getenv("CSAIR_CHROME"); path != "" {
		opts = append(opts, chromedp.ExecPath(path))
	}
	alloc, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	bctx, cancelCtx := chromedp.NewContext(alloc)
	defer cancelCtx()
	bctx, cancelTO := context.WithTimeout(bctx, wait+60*time.Second)
	defer cancelTO()

	var (
		mu    sync.Mutex
		order []network.RequestID
		byID  = map[network.RequestID]*Exchange{}
		done  = map[network.RequestID]bool{}
	)
	chromedp.ListenTarget(bctx, func(ev any) {
		mu.Lock()
		defer mu.Unlock()
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			if !strings.Contains(e.Request.URL, "/ita/rest/") {
				return
			}
			x := &Exchange{Method: e.Request.Method, URL: e.Request.URL}
			for _, entry := range e.Request.PostDataEntries {
				x.PostData += entry.Bytes
			}
			if _, seen := byID[e.RequestID]; !seen {
				order = append(order, e.RequestID)
			}
			byID[e.RequestID] = x
		case *network.EventResponseReceived:
			if x, ok := byID[e.RequestID]; ok {
				x.Status = e.Response.Status
			}
		case *network.EventLoadingFinished:
			if _, ok := byID[e.RequestID]; ok {
				done[e.RequestID] = true
			}
		}
	})

	url := fmt.Sprintf("https://b2c.csair.com/ita/intl/zh/flights?flex=1&m=0&p=100&t=%s-%s-%s&egs=ITA,ITA&open=1", origin, dest, date)
	err := chromedp.Run(bctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(stealthJS).Do(ctx)
			return err
		}),
		network.Enable(),
		chromedp.Navigate(url),
		chromedp.Sleep(wait),
		chromedp.ActionFunc(func(ctx context.Context) error {
			mu.Lock()
			ids := append([]network.RequestID(nil), order...)
			mu.Unlock()
			for _, id := range ids {
				mu.Lock()
				finished := done[id]
				mu.Unlock()
				if !finished {
					continue
				}
				body, err := network.GetResponseBody(id).Do(ctx)
				if err != nil {
					continue
				}
				mu.Lock()
				byID[id].Body = string(body)
				mu.Unlock()
			}
			return nil
		}),
	)
	mu.Lock()
	defer mu.Unlock()
	out := make([]Exchange, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	if err != nil {
		return out, fmt.Errorf("capture: %w", err)
	}
	return out, nil
}
