package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/kkweon/csair/internal/clierr"
)

// stealthJS masks the most obvious automation tells before any page script runs.
const stealthJS = `
Object.defineProperty(navigator, 'webdriver', {get: () => undefined});
Object.defineProperty(navigator, 'languages', {get: () => ['zh-CN','zh','en']});
Object.defineProperty(navigator, 'plugins', {get: () => [1,2,3,4,5]});
window.chrome = window.chrome || {runtime: {}};
`

// challengeJS issues a same-origin protected request so antidom.js runs the
// Aliyun challenge and sets acw_sc__v2. antidom wraps XMLHttpRequest (not fetch),
// so we must use XHR for the interception to fire. Returns immediately.
const challengeJS = `(function(){try{` +
	`var x=new XMLHttpRequest();` +
	`x.open('POST','/ita/intl/app',true);` +
	`x.setRequestHeader('Content-Type','application/x-www-form-urlencoded;charset=UTF-8');` +
	`x.send('language=zh&country=zh&m=0&flexible=1&adt=1&cnn=0&inf=0&dep[]=SFO&depArea[]=US&arr[]=CAN&arrArea[]=CN&date[]=2026-12-01');` +
	`}catch(e){}return 1;})()`

const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"

// BrowserProvider mints (and caches) the acw_sc__v2/acw_tc anti-bot cookies by
// driving Chrome through the /ita search flow, which triggers the Aliyun JS
// challenge. CDP exposes httpOnly cookies, so we capture acw_tc too.
type BrowserProvider struct {
	Route     string        // throwaway route to trigger a search, e.g. "SFO-CAN"
	Headless  bool          // false shows the window (use if headless is challenged)
	TTL       time.Duration // how long a harvested token is treated as fresh
	CachePath string
}

// NewBrowserProvider returns a provider with sensible defaults.
func NewBrowserProvider() *BrowserProvider {
	return &BrowserProvider{
		Route:     "SFO-CAN",
		Headless:  true,
		TTL:       15 * time.Minute,
		CachePath: defaultCachePath(),
	}
}

func defaultCachePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "csair", "cookies.json")
}

// Token returns a cached valid token, otherwise bootstraps a fresh one.
func (b *BrowserProvider) Token(ctx context.Context) (Token, error) {
	if t, ok := b.Load(); ok && t.Valid() {
		return t, nil
	}
	return b.Refresh(ctx)
}

// Refresh always bootstraps a new token and caches it.
func (b *BrowserProvider) Refresh(ctx context.Context) (Token, error) {
	t, err := b.bootstrap(ctx)
	if err != nil {
		return Token{}, err
	}
	b.save(t)
	return t, nil
}

// Load reads the cached token (valid or not).
func (b *BrowserProvider) Load() (Token, bool) {
	data, err := os.ReadFile(b.CachePath)
	if err != nil {
		return Token{}, false
	}
	var t Token
	if json.Unmarshal(data, &t) != nil {
		return Token{}, false
	}
	return t, true
}

// Clear removes the cached token.
func (b *BrowserProvider) Clear() error {
	err := os.Remove(b.CachePath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (b *BrowserProvider) save(t Token) {
	_ = os.MkdirAll(filepath.Dir(b.CachePath), 0o700)
	data, _ := json.MarshalIndent(t, "", "  ")
	_ = os.WriteFile(b.CachePath, data, 0o600)
}

func (b *BrowserProvider) bootstrap(ctx context.Context) (Token, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", b.Headless),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-infobars", true),
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
	bctx, cancelTO := context.WithTimeout(bctx, 90*time.Second)
	defer cancelTO()

	var cookies []*network.Cookie
	var pageURL, title string
	err := chromedp.Run(bctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(stealthJS).Do(ctx)
			return err
		}),
		network.Enable(),
		chromedp.Navigate(b.flightsURL()),
		chromedp.Sleep(2*time.Second), // let antidom.js load
		chromedp.ActionFunc(func(ctx context.Context) error {
			deadline := time.Now().Add(55 * time.Second)
			var dummy int
			for i := 0; ; i++ {
				// Actively provoke the Aliyun challenge: a protected XHR makes
				// antidom.js compute and set acw_sc__v2. Re-trigger periodically.
				if i%5 == 0 {
					_ = chromedp.Evaluate(challengeJS, &dummy).Do(ctx)
				}
				if cs, err := network.GetCookies().Do(ctx); err == nil {
					cookies = cs
					if cookieVal(cs, "acw_sc__v2") != "" {
						return nil
					}
				}
				if time.Now().After(deadline) {
					return nil
				}
				time.Sleep(time.Second)
			}
		}),
		chromedp.Location(&pageURL),
		chromedp.Title(&title),
	)
	if err != nil {
		return Token{}, fmt.Errorf("browser bootstrap: %w", err)
	}

	all := map[string]string{}
	names := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if strings.Contains(c.Domain, "csair.com") {
			all[c.Name] = c.Value
			names = append(names, c.Name)
		}
	}
	sort.Strings(names)

	// A real session is usable even without acw_sc__v2 (the WAF only sets it when
	// it challenges). Require *some* session cookie; acw is a bonus.
	hasSession := all["acw_sc__v2"] != "" || all["JSESSIONID"] != "" ||
		all["cz-book"] != "" || all["acw_tc"] != ""
	if !hasSession {
		return Token{}, fmt.Errorf("%w: bootstrap got no session cookies (url=%s title=%q cookies=%v) — try 'csair auth --headed'",
			clierr.ErrBlocked, trimURL(pageURL), title, names)
	}
	if all["acw_sc__v2"] == "" {
		fmt.Fprintf(os.Stderr, "note: proceeding without acw_sc__v2 (session has %d cookies: %s)\n", len(names), strings.Join(names, ", "))
	}
	return Token{
		AcwScV2: all["acw_sc__v2"],
		AcwTc:   all["acw_tc"],
		Cookies: all,
		Expires: time.Now().Add(b.TTL),
	}, nil
}

func trimURL(u string) string {
	if i := strings.IndexByte(u, '?'); i > 0 {
		return u[:i]
	}
	return u
}

// flightsURL is the /ita deep-link that creates a search from a route token and
// redirects to the booking page (which auto-fires queryInterFlight).
func (b *BrowserProvider) flightsURL() string {
	o, dst := "SFO", "CAN"
	if p := strings.SplitN(b.Route, "-", 2); len(p) == 2 {
		o, dst = p[0], p[1]
	}
	date := time.Now().AddDate(0, 0, 21).Format("20060102")
	return fmt.Sprintf("https://b2c.csair.com/ita/intl/zh/flights?flex=1&m=0&p=100&t=%s-%s-%s&egs=ITA,ITA&open=1", o, dst, date)
}

func cookieVal(cs []*network.Cookie, name string) string {
	for _, c := range cs {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// compile-time assertion that BrowserProvider satisfies both interfaces.
var (
	_ Provider  = (*BrowserProvider)(nil)
	_ Refresher = (*BrowserProvider)(nil)
)
