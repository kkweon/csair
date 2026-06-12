package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"github.com/kkweon/csair/internal/auth"
	"github.com/kkweon/csair/internal/clierr"
)

// tlsProfile is the Chrome client profile the TLS path impersonates. Its major
// version is mirrored by tlsUserAgent/tlsSecChUA (shared.go) so the advertised
// UA and the JA3/HTTP-2 fingerprint agree.
var tlsProfile = profiles.Chrome_146

// tlsClient is the Chrome-fingerprinted HTTPRequester selected by
// --use-tls-client. Where the stdlib Client replays the anti-bot token over Go's
// own TLS/HTTP-2 fingerprint (which the WAF can single out and challenge), this
// one uses bogdanfinn/tls-client to present a real Chrome JA3 — matching the
// headless Chrome that minted the token. It reuses the shared cookie seeding,
// pacing, headers, and response classification.
type tlsClient struct {
	pacer
	hc   tls_client.HttpClient
	logw io.Writer
}

func newTLS(tok auth.Token, opts ...Option) (*tlsClient, error) {
	cfg := applyOptions(opts)
	jar := tls_client.NewCookieJar()
	// No WithNotFollowRedirects: match the stdlib Client, which follows redirects.
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

	return &tlsClient{
		pacer: pacer{minGap: cfg.minGap, jitter: cfg.jitter},
		hc:    hc,
		logw:  cfg.logw,
	}, nil
}

func (c *tlsClient) Get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	return c.do(req)
}

func (c *tlsClient) PostForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	return c.do(req)
}

func (c *tlsClient) PostJSON(ctx context.Context, rawURL string, body any) ([]byte, error) {
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

func (c *tlsClient) do(req *fhttp.Request) ([]byte, error) {
	applyBrowserHeaders(req.Header, tlsUA)
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

var _ HTTPRequester = (*tlsClient)(nil)
