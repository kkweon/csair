// Package auth supplies the anti-bot cookies the /ita engine needs.
package auth

import (
	"context"
	"os"
	"time"

	"github.com/kkweon/csair/internal/clierr"
)

// Token holds the anti-bot cookies harvested from a browser session, plus the
// full cookie set from that session (so requests look like that real browser).
type Token struct {
	AcwScV2 string            // acw_sc__v2
	AcwTc   string            // acw_tc
	Cookies map[string]string `json:",omitempty"` // all csair.com cookies from the session
	Expires time.Time         // zero = unknown
}

// Valid reports whether the token looks usable right now. A session is usable
// if it has the acw token or any harvested cookies (the WAF only sets acw when
// it challenges).
func (t Token) Valid() bool {
	if t.AcwScV2 == "" && len(t.Cookies) == 0 {
		return false
	}
	return t.Expires.IsZero() || time.Now().Before(t.Expires)
}

// Provider supplies a Token, refreshing/bootstrapping as needed.
type Provider interface {
	Token(ctx context.Context) (Token, error)
}

// Refresher is an optional capability: force-mint a new token (e.g. after a
// token is blocked mid-flight).
type Refresher interface {
	Refresh(ctx context.Context) (Token, error)
}

// EnvProvider reads the token from environment variables:
//
//	CSAIR_ACW     -> acw_sc__v2 (required)
//	CSAIR_ACW_TC  -> acw_tc     (optional)
type EnvProvider struct{}

func (EnvProvider) Token(context.Context) (Token, error) {
	t := Token{AcwScV2: os.Getenv("CSAIR_ACW"), AcwTc: os.Getenv("CSAIR_ACW_TC")}
	if !t.Valid() {
		return Token{}, clierr.ErrTokenExpired
	}
	return t, nil
}

// Static wraps an already-known token (e.g. from a --acw flag or the cache).
type Static struct{ T Token }

func (s Static) Token(context.Context) (Token, error) {
	if !s.T.Valid() {
		return Token{}, clierr.ErrTokenExpired
	}
	return s.T, nil
}

// TODO(bootstrap): a BrowserProvider that drives headed Chrome (chromedp) to
// harvest acw_sc__v2, caches it under ~/.config/csair/cookies.json, and
// re-bootstraps on clierr.ErrTokenExpired. Tracked for the `csair auth` command.
