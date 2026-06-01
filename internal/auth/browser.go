package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/kkweon/csair/internal/clierr"
)

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
		chromedp.Flag("lang", "zh-CN,zh"),
		chromedp.NoSandbox,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-gpu", true),
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
	err := chromedp.Run(bctx,
		network.Enable(),
		chromedp.Navigate(b.flightsURL()),
		chromedp.ActionFunc(func(ctx context.Context) error {
			deadline := time.Now().Add(50 * time.Second)
			for {
				cs, err := network.GetCookies().Do(ctx)
				if err == nil {
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
	)
	if err != nil {
		return Token{}, fmt.Errorf("browser bootstrap: %w", err)
	}

	sc := cookieVal(cookies, "acw_sc__v2")
	if sc == "" {
		return Token{}, fmt.Errorf("%w: browser did not yield acw_sc__v2 (try 'csair auth --headed')", clierr.ErrBlocked)
	}
	return Token{
		AcwScV2: sc,
		AcwTc:   cookieVal(cookies, "acw_tc"),
		Expires: time.Now().Add(b.TTL),
	}, nil
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
