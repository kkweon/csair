package airport

import (
	"testing"
	"time"
)

func TestZone(t *testing.T) {
	cat := NewStatic()
	cases := map[string]string{
		"SFO": "America/Los_Angeles",
		"sfo": "America/Los_Angeles", // case-insensitive
		"CAN": "Asia/Shanghai",
		"JFK": "America/New_York",
		"LHR": "Europe/London",
	}
	for code, want := range cases {
		got, err := cat.Zone(code)
		if err != nil {
			t.Errorf("Zone(%q): %v", code, err)
			continue
		}
		if got != want {
			t.Errorf("Zone(%q) = %q, want %q", code, got, want)
		}
		// Every mapped zone must be loadable (valid IANA name).
		if _, err := time.LoadLocation(got); err != nil {
			t.Errorf("Zone(%q)=%q not loadable: %v", code, got, err)
		}
	}
	if _, err := cat.Zone("ZZZ"); err == nil {
		t.Error("Zone(ZZZ) = nil error, want unknown-airport error")
	}
}

// TestEveryCountryAirportHasZone keeps the country and zone maps in lockstep so
// a route can't resolve a country but silently lack a timezone.
func TestEveryCountryAirportHasZone(t *testing.T) {
	for code := range seed {
		if _, ok := zones[code]; !ok {
			t.Errorf("airport %q has a country but no timezone", code)
		}
	}
}
