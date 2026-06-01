// Package transport provides a human-like HTTP requester: browser headers, a
// persistent cookie jar (so Set-Cookie carries across the app→query sequence),
// anti-bot cookie injection, and polite pacing to avoid the rate-limit captcha.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kkweon/csair/internal/auth"
	"github.com/kkweon/csair/internal/clierr"
)

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"

// HTTPRequester issues paced, browser-like HTTP requests and returns raw bodies.
type HTTPRequester interface {
	Get(ctx context.Context, rawURL string) ([]byte, error)
	PostForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error)
	PostJSON(ctx context.Context, rawURL string, body any) ([]byte, error)
}

// Client is the default HTTPRequester.
type Client struct {
	hc      *http.Client
	referer string

	mu     sync.Mutex
	last   time.Time
	minGap time.Duration
	jitter time.Duration
}

// New builds a Client whose cookie jar is seeded with the anti-bot token.
func New(tok auth.Token) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(domainBase)
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
	cookies := make([]*http.Cookie, 0, len(set))
	for k, v := range set {
		cookies = append(cookies, &http.Cookie{Name: k, Value: v})
	}
	jar.SetCookies(base, cookies)

	return &Client{
		hc: &http.Client{
			Jar:       jar,
			Timeout:   30 * time.Second,
			Transport: &humanRT{base: http.DefaultTransport},
		},
		referer: domainBase + "/ita/book/zh/booking",
		minGap:  600 * time.Millisecond,
		jitter:  500 * time.Millisecond,
	}, nil
}

const domainBase = "https://b2c.csair.com"

func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	return c.do(req)
}

func (c *Client) PostForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(buf))
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
		return nil, fmt.Errorf("%w: JSON endpoint returned HTML (acw token likely expired — run 'csair auth')", clierr.ErrBlocked)
	}
	return b, nil
}

func looksHTML(b []byte) bool {
	t := bytes.TrimSpace(b)
	return bytes.HasPrefix(t, []byte("<!")) || bytes.HasPrefix(t, []byte("<html")) || bytes.HasPrefix(t, []byte("<"))
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", domainBase)
	req.Header.Set("Priority", "u=1, i")
	if req.Header.Get("Referer") == "" {
		req.Header.Set("Referer", c.referer)
	}
	c.pace()

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden || bytes.Contains(b, []byte("need.captcha")) || bytes.Contains(b, []byte("CZWEB000010")) {
		return nil, fmt.Errorf("%w: status %d", clierr.ErrBlocked, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: status %d", clierr.ErrUpstream, resp.StatusCode)
	}
	return b, nil
}

// pace throttles requests with a minimum gap plus randomized jitter.
func (c *Client) pace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	wait := c.minGap + time.Duration(rand.Int63n(int64(c.jitter)+1))
	if d := wait - time.Since(c.last); d > 0 {
		time.Sleep(d)
	}
	c.last = time.Now()
}

// humanRT injects the standard browser header set on every request.
type humanRT struct{ base http.RoundTripper }

func (h *humanRT) RoundTrip(req *http.Request) (*http.Response, error) {
	setDefault(req, "User-Agent", userAgent)
	setDefault(req, "Accept-Language", "en-US,en;q=0.9")
	setDefault(req, "sec-ch-ua", `"Chromium";v="148", "Google Chrome";v="148", "Not/A)Brand";v="99"`)
	setDefault(req, "sec-ch-ua-mobile", "?0")
	setDefault(req, "sec-ch-ua-platform", `"Linux"`)
	setDefault(req, "Sec-Fetch-Site", "same-origin")
	if req.Header.Get("Sec-Fetch-Dest") == "" {
		req.Header.Set("Sec-Fetch-Dest", "empty")
		req.Header.Set("Sec-Fetch-Mode", "cors")
	}
	return h.base.RoundTrip(req)
}

func setDefault(req *http.Request, k, v string) {
	if req.Header.Get(k) == "" {
		req.Header.Set(k, v)
	}
}
