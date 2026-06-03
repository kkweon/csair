package ita

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kkweon/csair/internal/domain"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

func TestExecution(t *testing.T) {
	html := []byte(`<script>loc.replace(loc.pathname + "/../zh/shop/?execution=ca5bd079730c9c69c203c911b8db90d2" + search);</script>`)
	got, err := NewParser().Execution(html)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ca5bd079730c9c69c203c911b8db90d2"; got != want {
		t.Fatalf("execution = %q, want %q", got, want)
	}
}

func TestSegVias(t *testing.T) {
	if v := segVias(dtoSegment{Legs: []dtoLeg{{DepPort: "SFO", ArrPort: "CAN"}}}); v != nil {
		t.Errorf("single-leg vias = %v, want nil", v)
	}
	if v := segVias(dtoSegment{}); v != nil {
		t.Errorf("no-leg vias = %v, want nil", v)
	}
	v := segVias(dtoSegment{Legs: []dtoLeg{{ArrPort: "WUH"}, {ArrPort: "CAN"}}})
	if len(v) != 1 || v[0] != "WUH" {
		t.Errorf("through vias = %v, want [WUH]", v)
	}
}

// A single-segment through-flight (one flight number, an internal Wuhan stop)
// must expose the via city from its legs and report stops=1.
func TestFlights_ThroughFlightVias(t *testing.T) {
	grid := []byte(`{
	  "success": true,
	  "data": {"data": {"dateFlights": [
	    {"stopNumber": 1, "duration": 1100, "origin": "SFO", "destination": "CAN",
	     "segments": [
	       {"flightNo": "660", "carrier": "CZ", "depPort": "SFO", "arrPort": "CAN",
	        "legs": [
	          {"depPort": "SFO", "arrPort": "WUH"},
	          {"depPort": "WUH", "arrPort": "CAN"}
	        ]}
	     ],
	     "prices": [{"displayPrice": 1284, "displayCurrency": "USD",
	        "cabins": [{"name": "C", "type": "Business", "bookingClassAvails": "9"}]}]
	    }
	  ]}}
	}`)
	its, err := NewParser().Flights(grid)
	if err != nil {
		t.Fatal(err)
	}
	if len(its) != 1 {
		t.Fatalf("itineraries = %d, want 1", len(its))
	}
	it := its[0]
	if it.Stops != 1 {
		t.Errorf("stops = %d, want 1", it.Stops)
	}
	if len(it.Segments) != 1 {
		t.Fatalf("segments = %d, want 1 (through-flight keeps one flight number)", len(it.Segments))
	}
	if got := it.Segments[0].Number(); got != "CZ660" {
		t.Errorf("flight = %q, want CZ660", got)
	}
	if got := it.Segments[0].Vias; len(got) != 1 || got[0] != "WUH" {
		t.Errorf("vias = %v, want [WUH]", got)
	}
}

func TestFlights_SFOCAN(t *testing.T) {
	grid := loadFixture(t, "queryInterFlight_SFO-CAN_2026-06-14.json")
	its, err := NewParser().Flights(grid)
	if err != nil {
		t.Fatal(err)
	}
	if len(its) != 15 {
		t.Fatalf("itineraries = %d, want 15", len(its))
	}

	// First option is the CZ658 nonstop.
	cz := its[0]
	if len(cz.Segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(cz.Segments))
	}
	if got := cz.Segments[0].Number(); got != "CZ658" {
		t.Fatalf("flight = %q, want CZ658", got)
	}
	if cz.Stops != 0 {
		t.Fatalf("stops = %d, want 0", cz.Stops)
	}
	if cz.Duration != 885*time.Minute {
		t.Fatalf("duration = %v, want 14h45m", cz.Duration)
	}

	// Cabin headline availability is the MAX across fare tiers, not per-RBD.
	// Business has I=6 and C=8 → cabin availability must be 8.
	byCabin := map[domain.Cabin]domain.CabinAvail{}
	for _, cb := range cz.Cabins {
		byCabin[cb.Cabin] = cb
	}

	biz, ok := byCabin[domain.CabinBusiness]
	if !ok {
		t.Fatal("missing Business cabin")
	}
	if biz.Seats != 8 || biz.AtLeast {
		t.Errorf("Business seats = %d (atLeast=%v), want 8 (false) = max of the tiers", biz.Seats, biz.AtLeast)
	}
	if len(biz.Classes) < 2 {
		t.Errorf("Business should retain its per-RBD breakdown, got %d classes", len(biz.Classes))
	}
	if biz.From.Amount <= 0 || biz.From.Currency != "USD" {
		t.Errorf("Business from = %+v, want cheapest positive USD fare", biz.From)
	}

	eco, ok := byCabin[domain.CabinEconomy]
	if !ok {
		t.Fatal("missing Economy cabin")
	}
	if eco.Seats != 9 || !eco.AtLeast {
		t.Errorf("Economy seats = %d (atLeast=%v), want 9 (true, capped)", eco.Seats, eco.AtLeast)
	}
}
