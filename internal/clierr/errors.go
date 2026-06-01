// Package clierr defines the typed error taxonomy and CLI exit codes.
package clierr

import "errors"

var (
	// ErrUsage: bad flags/args.
	ErrUsage = errors.New("usage error")
	// ErrTokenExpired: acw_sc__v2 missing or stale; caller should (re)bootstrap.
	ErrTokenExpired = errors.New("auth token expired")
	// ErrNoFlights: the search succeeded but returned no itineraries.
	ErrNoFlights = errors.New("no flights found")
	// ErrBlocked: anti-bot / rate-limit wall (e.g. CZWEB000010 captcha).
	ErrBlocked = errors.New("blocked by anti-bot")
	// ErrUpstream: malformed/unexpected upstream response.
	ErrUpstream = errors.New("upstream error")
)

// ExitCode maps an error to the process exit code documented in docs/cli.md.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage):
		return 2
	case errors.Is(err, ErrTokenExpired):
		return 3
	case errors.Is(err, ErrNoFlights):
		return 4
	case errors.Is(err, ErrBlocked):
		return 5
	default:
		return 1
	}
}
