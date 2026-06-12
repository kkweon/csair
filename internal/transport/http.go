// Package transport provides human-like HTTP requesters: browser headers, a
// persistent cookie jar (so Set-Cookie carries across the app→query sequence),
// anti-bot cookie injection, and polite pacing to avoid the rate-limit captcha.
// Two implementations sit behind HTTPRequester — the default stdlib Client here,
// and the Chrome-fingerprinted tlsClient (--use-tls-client) in tlsclient.go;
// shared.go holds the parts that don't depend on which HTTP stack is used.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/kkweon/csair/internal/auth"
	"github.com/kkweon/csair/internal/clierr"
)

// Client is the default HTTPRequester, built on stdlib net/http.
type Client struct {
	pacer
	hc   *http.Client
	logw io.Writer // when non-nil, one line per request (method, URL, status, bytes, elapsed)
}

// New builds a Client whose cookie jar is seeded with the anti-bot token.
func New(tok auth.Token, opts ...Option) (*Client, error) {
	cfg := applyOptions(opts)
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(domainBase)
	set := cookieSet(tok)
	cookies := make([]*http.Cookie, 0, len(set))
	for k, v := range set {
		cookies = append(cookies, &http.Cookie{Name: k, Value: v})
	}
	jar.SetCookies(base, cookies)

	return &Client{
		pacer: pacer{minGap: cfg.minGap, jitter: cfg.jitter},
		hc: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		logw: cfg.logw,
	}, nil
}

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
		logf(c.logw, "http: %s returned HTML (anti-bot interstitial — acw token likely expired)", rawURL)
		return nil, fmt.Errorf("%w: JSON endpoint returned HTML (acw token likely expired — run 'csair auth')", clierr.ErrBlocked)
	}
	return b, nil
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	applyBrowserHeaders(req.Header, stdlibUA)
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

var _ HTTPRequester = (*Client)(nil)
