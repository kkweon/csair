package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/viper"

	"github.com/kkweon/csair/internal/domain"
	"github.com/kkweon/csair/internal/monitor"
)

// seg builds a single marketing segment (no times needed for filtering).
func seg(carrier, no, from, to string) domain.Segment {
	return domain.Segment{Carrier: carrier, FlightNo: no, Origin: from, Destination: to}
}

func bizCabin(seats int) domain.CabinAvail {
	return domain.CabinAvail{Cabin: domain.CabinBusiness, Seats: seats}
}

func econCabin(seats int) domain.CabinAvail {
	return domain.CabinAvail{Cabin: domain.CabinEconomy, Seats: seats}
}

func itin(stops int, segs []domain.Segment, cabins ...domain.CabinAvail) domain.Itinerary {
	return domain.Itinerary{Segments: segs, Stops: stops, Cabins: cabins}
}

func TestFlightKey(t *testing.T) {
	cases := []struct {
		segs []domain.Segment
		want string
	}{
		{[]domain.Segment{seg("CZ", "658", "SFO", "CAN")}, "CZ658"},
		// through-flight: one segment SFO→CAN (Wuhan stop is internal) → "CZ660"
		{[]domain.Segment{seg("CZ", "660", "SFO", "CAN")}, "CZ660"},
		// true connection on two flight numbers → "CZ660+CZ8004"
		{[]domain.Segment{seg("CZ", "660", "SFO", "WUH"), seg("CZ", "8004", "WUH", "CAN")}, "CZ660+CZ8004"},
	}
	for _, c := range cases {
		if got := flightKey(domain.Itinerary{Segments: c.segs}); got != c.want {
			t.Errorf("flightKey = %q, want %q", got, c.want)
		}
	}
}

func TestBusinessTracked(t *testing.T) {
	// Fresh itineraries per case: businessTracked compacts in place, so reusing
	// shared backing arrays across subtests would be unsafe.
	nonstop := func() domain.Itinerary {
		return itin(0, []domain.Segment{seg("CZ", "658", "SFO", "CAN")}, bizCabin(7), econCabin(9))
	}
	through := func() domain.Itinerary { // 1-stop through-flight, key "CZ660"
		return itin(1, []domain.Segment{seg("CZ", "660", "SFO", "CAN")}, bizCabin(9), econCabin(9))
	}
	combo := func() domain.Itinerary { // 1-stop connection, key "CZ660+CZ8004"
		return itin(1, []domain.Segment{seg("CZ", "660", "SFO", "WUH"), seg("CZ", "8004", "WUH", "CAN")}, bizCabin(4), econCabin(9))
	}
	econOnly := func() domain.Itinerary { // key "CZ660" but no business inventory
		return itin(1, []domain.Segment{seg("CZ", "660", "SFO", "CAN")}, econCabin(9))
	}

	tests := []struct {
		name   string
		target monitor.Target
		in     []domain.Itinerary
		want   []string // flight keys expected to remain
	}{
		{
			name:   "default keeps nonstop business only",
			target: monitor.Target{From: "SFO", To: "CAN", Date: "2026-06-17"},
			in:     []domain.Itinerary{nonstop(), through(), combo()},
			want:   []string{"CZ658"},
		},
		{
			name:   "allowlist keeps the through-flight regardless of stops",
			target: monitor.Target{From: "SFO", To: "CAN", Date: "2026-06-16", Flights: []string{"CZ660"}},
			in:     []domain.Itinerary{nonstop(), through(), combo()},
			want:   []string{"CZ660"},
		},
		{
			name:   "allowlist matches an explicit connection key",
			target: monitor.Target{From: "SFO", To: "CAN", Date: "2026-06-16", Flights: []string{"CZ660+CZ8004"}},
			in:     []domain.Itinerary{nonstop(), through(), combo()},
			want:   []string{"CZ660+CZ8004"},
		},
		{
			name:   "allowlist still drops itineraries without business",
			target: monitor.Target{From: "SFO", To: "CAN", Date: "2026-06-16", Flights: []string{"CZ660"}},
			in:     []domain.Itinerary{econOnly()},
			want:   nil,
		},
		{
			name:   "default drops connections (nonstop gate)",
			target: monitor.Target{From: "SFO", To: "CAN", Date: "2026-06-16"},
			in:     []domain.Itinerary{through(), combo()},
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := &domain.SearchResult{Itineraries: tc.in}
			businessTracked(res, tc.target)

			var got []string
			for _, it := range res.Itineraries {
				got = append(got, flightKey(it))
				for _, cb := range it.Cabins {
					if cb.Cabin != domain.CabinBusiness {
						t.Errorf("non-business cabin retained on %s: %s", flightKey(it), cb.Cabin)
					}
				}
			}
			if !equalStrings(got, tc.want) {
				t.Errorf("kept = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMonitorConfigDecodesFlights exercises the production decode path
// (viper + mapstructure) to confirm the optional `flights` field round-trips.
func TestMonitorConfigDecodesFlights(t *testing.T) {
	const cfg = `
[monitor]
snapshotDir = "data/monitor"

[[monitor.targets]]
from = "SFO"
to   = "CAN"
date = "2026-06-16"
flights = ["CZ660"]

[[monitor.targets]]
from = "SFO"
to   = "CAN"
date = "2026-06-17"
`
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(bytes.NewBufferString(cfg)); err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	var mc monitor.Config
	if err := v.UnmarshalKey("monitor", &mc); err != nil {
		t.Fatalf("UnmarshalKey: %v", err)
	}
	if len(mc.Targets) != 2 {
		t.Fatalf("targets = %d, want 2", len(mc.Targets))
	}
	if got := mc.Targets[0].Flights; len(got) != 1 || got[0] != "CZ660" {
		t.Errorf("targets[0].Flights = %v, want [CZ660]", got)
	}
	if got := mc.Targets[1].Flights; len(got) != 0 {
		t.Errorf("targets[1].Flights = %v, want empty (default nonstop-only)", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
