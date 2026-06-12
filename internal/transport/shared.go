package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"sync"
	"time"

	"github.com/kkweon/csair/internal/auth"
	"github.com/kkweon/csair/internal/clierr"
)

const (
	domainBase     = "https://b2c.csair.com"
	defaultReferer = domainBase + "/ita/book/zh/booking"
)

// HTTPRequester issues paced, browser-like HTTP requests and returns raw bodies.
type HTTPRequester interface {
	Get(ctx context.Context, rawURL string) ([]byte, error)
	PostForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error)
	PostJSON(ctx context.Context, rawURL string, body any) ([]byte, error)
}

// NewRequester builds the requester selected by useTLS: the default stdlib
// net/http Client, or the Chrome-fingerprinted tlsClient (--use-tls-client) that
// replays the anti-bot token over a Chrome-matching JA3/HTTP-2 handshake. Both
// share the cookie seeding, pacing, headers, and response classification below.
func NewRequester(tok auth.Token, useTLS bool, opts ...Option) (HTTPRequester, error) {
	if useTLS {
		return newTLS(tok, opts...)
	}
	return New(tok, opts...)
}

// config holds the tunables shared by both requester implementations.
type config struct {
	minGap, jitter time.Duration
	logw           io.Writer
}

func defaultConfig() config {
	return config{minGap: 600 * time.Millisecond, jitter: 500 * time.Millisecond}
}

// Option customizes a requester at construction.
type Option func(*config)

// WithPacing overrides the inter-request throttle (minimum gap + random jitter).
// Used by the seat monitor to space a whole multi-target run more politely than
// the snappier interactive-search default.
func WithPacing(minGap, jitter time.Duration) Option {
	return func(c *config) { c.minGap, c.jitter = minGap, jitter }
}

// WithVerbose makes the requester narrate each request to w (method, URL, status,
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

// pacer throttles requests with a minimum gap plus randomized jitter. Embedded by
// both requesters so a shared client paces a whole run across goroutines.
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

// cookieSet builds the cookie name→value set a fresh jar is seeded with: the
// static Site/language pair, the full captured browser cookies, then the acw
// anti-bot tokens. Returned as plain data so the stdlib and fhttp jars can each
// seed it with their own cookie type.
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

// Browser identity for the stdlib path (current Chrome major).
const (
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"
	secChUA = `"Chromium";v="148", "Google Chrome";v="148", "Not/A)Brand";v="99"`
)

// Browser identity for the TLS path. The major version is pinned to the
// tls-client Chrome profile (see tlsclient.go) so the advertised UA and the JA3
// fingerprint agree — a UA/fingerprint mismatch is itself an anti-bot tell.
const (
	tlsUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	tlsSecChUA = `"Chromium";v="146", "Google Chrome";v="146", "Not/A)Brand";v="99"`
)

// headerSetter is satisfied by both http.Header and fhttp.Header (both are
// map[string][]string with the same Set/Get methods), so the browser header set
// is written once for either transport.
type headerSetter interface {
	Set(key, value string)
	Get(key string) string
}

// ua bundles the User-Agent-coupled header values that must match the TLS profile.
type ua struct{ userAgent, secChUA string }

var (
	stdlibUA = ua{userAgent: userAgent, secChUA: secChUA}
	tlsUA    = ua{userAgent: tlsUserAgent, secChUA: tlsSecChUA}
)

// applyBrowserHeaders writes the standard browser header set, set-if-absent so a
// caller's per-request override (e.g. Sec-Fetch-Dest on Get, Content-Type on a
// POST) stands.
func applyBrowserHeaders(h headerSetter, id ua) {
	setIfAbsent(h, "Accept", "application/json, text/plain, */*")
	setIfAbsent(h, "Accept-Language", "en-US,en;q=0.9")
	setIfAbsent(h, "Origin", domainBase)
	setIfAbsent(h, "Referer", defaultReferer)
	setIfAbsent(h, "Priority", "u=1, i")
	setIfAbsent(h, "User-Agent", id.userAgent)
	setIfAbsent(h, "sec-ch-ua", id.secChUA)
	setIfAbsent(h, "sec-ch-ua-mobile", "?0")
	setIfAbsent(h, "sec-ch-ua-platform", `"Linux"`)
	setIfAbsent(h, "Sec-Fetch-Site", "same-origin")
	if h.Get("Sec-Fetch-Dest") == "" {
		h.Set("Sec-Fetch-Dest", "empty")
		h.Set("Sec-Fetch-Mode", "cors")
	}
}

func setIfAbsent(h headerSetter, k, v string) {
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

// logBlockOrUpstream narrates a classified failure with the message matching its
// kind, shared by both requesters' verbose traces.
func logBlockOrUpstream(w io.Writer, rawURL string, status int, err error) {
	if errors.Is(err, clierr.ErrBlocked) {
		logf(w, "http: %s blocked by anti-bot (status %d)", rawURL, status)
	} else {
		logf(w, "http: %s upstream error (status %d)", rawURL, status)
	}
}
