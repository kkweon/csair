// Package transport provides the human-like HTTP requester the /ita engine needs:
// a Chrome-fingerprinted TLS/HTTP-2 client (via bogdanfinn/tls-client) so the
// anti-bot token minted in headless Chrome is replayed over a handshake that
// matches a real Chrome — Go's stdlib fingerprint is singled out and challenged
// by the Aliyun WAF. It also carries the browser header set, a persistent cookie
// jar seeded with the token, and polite pacing to avoid the rate-limit captcha.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"github.com/kkweon/csair/internal/auth"
	"github.com/kkweon/csair/internal/clierr"
)

const (
	domainBase     = "https://b2c.csair.com"
	defaultReferer = domainBase + "/ita/book/zh/booking"
)

// Browser identity. The Chrome major version is pinned to tlsProfile so the
// advertised User-Agent and the JA3/HTTP-2 fingerprint agree — a UA/fingerprint
// mismatch is itself an anti-bot tell.
const (
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	secChUA = `"Chromium";v="146", "Google Chrome";v="146", "Not/A)Brand";v="99"`
)

// tlsProfile is the Chrome client profile impersonated at the TLS layer.
var tlsProfile = profiles.Chrome_146

// HTTPRequester issues paced, browser-like HTTP requests and returns raw bodies.
type HTTPRequester interface {
	Get(ctx context.Context, rawURL string) ([]byte, error)
	PostForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error)
	PostJSON(ctx context.Context, rawURL string, body any) ([]byte, error)
}

// config holds the construction tunables set by Option.
type config struct {
	minGap, jitter time.Duration
	logw           io.Writer
}

func defaultConfig() config {
	return config{minGap: 600 * time.Millisecond, jitter: 500 * time.Millisecond}
}

// Option customizes a Client at construction.
type Option func(*config)

// WithPacing overrides the inter-request throttle (minimum gap + random jitter).
// Used by the seat monitor to space a whole multi-target run more politely than
// the snappier interactive-search default.
func WithPacing(minGap, jitter time.Duration) Option {
	return func(c *config) { c.minGap, c.jitter = minGap, jitter }
}

// WithVerbose makes the client narrate each request to w (method, URL, status,
// response size, elapsed) plus anti-bot/upstream failures. The seat monitor turns
// this on so a run leaves a complete trace of what it fetched.
func WithVerbose(w io.Writer) Option {
	return func(c *config) { c.logw = w }
}

func applyOptions(opts []Option) config {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// pacer throttles requests with a minimum gap plus randomized jitter; embedded in
// Client so a shared client paces a whole run across goroutines.
type pacer struct {
	mu     sync.Mutex
	last   time.Time
	minGap time.Duration
	jitter time.Duration
}

func (p *pacer) pace() {
	p.mu.Lock()
	defer p.mu.Unlock()
	wait := p.minGap + time.Duration(rand.Int63n(int64(p.jitter)+1))
	if d := wait - time.Since(p.last); d > 0 {
		time.Sleep(d)
	}
	p.last = time.Now()
}

// Client is the Chrome-fingerprinted HTTPRequester. It presents a real Chrome
// JA3/HTTP-2 fingerprint (matching the headless Chrome that mints the token) so
// the WAF treats the replayed session as the same browser.
type Client struct {
	pacer
	hc   tls_client.HttpClient
	logw io.Writer // when non-nil, one line per request (method, URL, status, bytes, elapsed)
}

// New builds a Client whose cookie jar is seeded with the anti-bot token.
func New(tok auth.Token, opts ...Option) (*Client, error) {
	cfg := applyOptions(opts)
	jar := tls_client.NewCookieJar()
	// No WithNotFollowRedirects: follow redirects like a browser would.
	hc, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(tlsProfile),
		tls_client.WithCookieJar(jar),
	)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(domainBase)
	set := cookieSet(tok)
	cookies := make([]*fhttp.Cookie, 0, len(set))
	for k, v := range set {
		cookies = append(cookies, &fhttp.Cookie{Name: k, Value: v})
	}
	hc.SetCookies(base, cookies)

	return &Client{
		pacer: pacer{minGap: cfg.minGap, jitter: cfg.jitter},
		hc:    hc,
		logw:  cfg.logw,
	}, nil
}

func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	return c.do(req)
}

func (c *Client) PostForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	return c.do(req)
}

func (c *Client) PostJSON(ctx context.Context, rawURL string, body any) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodPost, rawURL, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	b, err := c.do(req)
	if err != nil {
		return nil, err
	}
	// A JSON endpoint returning HTML means the anti-bot served an interstitial
	// (typically a stale/expired acw_sc__v2 token).
	if looksHTML(b) {
		logf(c.logw, "http: %s returned HTML (anti-bot interstitial — acw token likely expired)", rawURL)
		return nil, fmt.Errorf("%w: JSON endpoint returned HTML (acw token likely expired — run 'csair auth')", clierr.ErrBlocked)
	}
	return b, nil
}

func (c *Client) do(req *fhttp.Request) ([]byte, error) {
	applyBrowserHeaders(req.Header)
	req.Header[fhttp.HeaderOrderKey] = chromeHeaderOrder
	c.pace()

	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		logf(c.logw, "http: %s %s -> error: %v", req.Method, req.URL, err)
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	logf(c.logw, "http: %s %s -> %d (%d bytes, %s)", req.Method, req.URL, resp.StatusCode, len(b), time.Since(start).Round(time.Millisecond))
	if cerr := classifyStatus(resp.StatusCode, b); cerr != nil {
		logBlockOrUpstream(c.logw, req.URL.String(), resp.StatusCode, cerr)
		return nil, cerr
	}
	return b, nil
}

// chromeHeaderOrder is the lowercase header order a real Chrome sends. fhttp
// orders whichever of these are present and ignores the rest, keeping the
// request's header ordering consistent with the impersonated TLS fingerprint.
var chromeHeaderOrder = []string{
	"host",
	"content-length",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"user-agent",
	"content-type",
	"accept",
	"origin",
	"sec-fetch-site",
	"sec-fetch-mode",
	"sec-fetch-dest",
	"referer",
	"accept-encoding",
	"accept-language",
	"priority",
}

// cookieSet builds the cookie name→value set the jar is seeded with: the static
// Site/language pair, the full captured browser cookies, then the acw anti-bot
// tokens.
func cookieSet(tok auth.Token) map[string]string {
	set := map[string]string{"Site": "US", "language": "zh_us"}
	// Prefer the full cookie set captured from the real browser session.
	for k, v := range tok.Cookies {
		set[k] = v
	}
	if tok.AcwScV2 != "" {
		set["acw_sc__v2"] = tok.AcwScV2
	}
	if tok.AcwTc != "" {
		set["acw_tc"] = tok.AcwTc
	}
	return set
}

// applyBrowserHeaders writes the standard browser header set, set-if-absent so a
// caller's per-request override (e.g. Sec-Fetch-Dest on Get, Content-Type on a
// POST) stands.
func applyBrowserHeaders(h fhttp.Header) {
	setIfAbsent(h, "Accept", "application/json, text/plain, */*")
	setIfAbsent(h, "Accept-Language", "en-US,en;q=0.9")
	setIfAbsent(h, "Origin", domainBase)
	setIfAbsent(h, "Referer", defaultReferer)
	setIfAbsent(h, "Priority", "u=1, i")
	setIfAbsent(h, "User-Agent", userAgent)
	setIfAbsent(h, "sec-ch-ua", secChUA)
	setIfAbsent(h, "sec-ch-ua-mobile", "?0")
	setIfAbsent(h, "sec-ch-ua-platform", `"Linux"`)
	setIfAbsent(h, "Sec-Fetch-Site", "same-origin")
	if h.Get("Sec-Fetch-Dest") == "" {
		h.Set("Sec-Fetch-Dest", "empty")
		h.Set("Sec-Fetch-Mode", "cors")
	}
}

func setIfAbsent(h fhttp.Header, k, v string) {
	if h.Get(k) == "" {
		h.Set(k, v)
	}
}

// logf writes one diagnostic line when verbose logging is enabled (no-op when w is nil).
func logf(w io.Writer, format string, a ...any) {
	if w != nil {
		fmt.Fprintf(w, format+"\n", a...)
	}
}

func looksHTML(b []byte) bool {
	t := bytes.TrimSpace(b)
	return bytes.HasPrefix(t, []byte("<!")) || bytes.HasPrefix(t, []byte("<html")) || bytes.HasPrefix(t, []byte("<"))
}

// classifyStatus maps an HTTP status + body to the transport's typed errors: an
// anti-bot block (403 / need.captcha / CZWEB000010) → ErrBlocked, any other
// 4xx/5xx → ErrUpstream, success → nil.
func classifyStatus(status int, body []byte) error {
	if status == 403 || bytes.Contains(body, []byte("need.captcha")) || bytes.Contains(body, []byte("CZWEB000010")) {
		return fmt.Errorf("%w: status %d", clierr.ErrBlocked, status)
	}
	if status >= 400 {
		return fmt.Errorf("%w: status %d", clierr.ErrUpstream, status)
	}
	return nil
}

// logBlockOrUpstream narrates a classified failure with the message matching its kind.
func logBlockOrUpstream(w io.Writer, rawURL string, status int, err error) {
	if errors.Is(err, clierr.ErrBlocked) {
		logf(w, "http: %s blocked by anti-bot (status %d)", rawURL, status)
	} else {
		logf(w, "http: %s upstream error (status %d)", rawURL, status)
	}
}

var _ HTTPRequester = (*Client)(nil)
