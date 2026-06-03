package monitor

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// fixedNow is 2026-06-03 16:07 UTC = 09:07 PDT, so the "as of" stamp is stable.
var fixedNow = time.Date(2026, 6, 3, 16, 7, 0, 0, time.UTC)

// biz builds a snapshot from flight->seats pairs, each as a single-flight
// itinerary with a business cabin (seats < 0 means "no business cabin").
func biz(origin, dest, date string, flights []flightSeats) Snapshot {
	s := Snapshot{Origin: origin, Destination: dest, Date: date}
	for _, fs := range flights {
		it := Itinerary{Flights: strings.Split(fs.key, "+")}
		if fs.seats >= 0 {
			it.Cabins = []Cabin{{Cabin: "business", Seats: fs.seats}}
		} else {
			// present but no business inventory (e.g. economy-only itinerary)
			it.Cabins = []Cabin{{Cabin: "economy", Seats: 4}}
		}
		s.Itineraries = append(s.Itineraries, it)
	}
	return s
}

type flightSeats struct {
	key   string
	seats int
}

func TestBusinessSeats(t *testing.T) {
	s := biz("SFO", "CAN", "2026-06-14", []flightSeats{
		{"CZ658", 7},
		{"CZ660+CZ3368", 2},
		{"CZ400", -1}, // economy-only -> 0
	})
	got := s.BusinessSeats()
	want := map[string]int{"CZ658": 7, "CZ660+CZ3368": 2, "CZ400": 0}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
}

func TestDiff(t *testing.T) {
	prev := biz("SFO", "CAN", "2026-06-14", []flightSeats{
		{"CZ658", 8}, {"CZ330", 5}, {"CZ302", 9},
	})
	cur := biz("SFO", "CAN", "2026-06-14", []flightSeats{
		{"CZ658", 7},        // changed 8 -> 7
		{"CZ660+CZ3368", 2}, // new
		{"CZ302", 9},        // unchanged -> excluded
		{"CZ400", -1},       // new, no business -> 0
	})
	got := Diff(prev, cur)

	type wc struct {
		flight   string
		old, new string // "nil" sentinel for absent
	}
	want := []wc{
		{"CZ330", "5", "nil"},
		{"CZ400", "nil", "0"},
		{"CZ658", "8", "7"},
		{"CZ660+CZ3368", "nil", "2"},
	}
	if len(got) != len(want) {
		t.Fatalf("Diff len = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Flight != w.flight {
			t.Errorf("[%d] flight = %q, want %q", i, got[i].Flight, w.flight)
		}
		if ptrStr(got[i].Old) != w.old {
			t.Errorf("[%d] old = %q, want %q", i, ptrStr(got[i].Old), w.old)
		}
		if ptrStr(got[i].New) != w.new {
			t.Errorf("[%d] new = %q, want %q", i, ptrStr(got[i].New), w.new)
		}
	}
}

func TestDiffNoChange(t *testing.T) {
	s := biz("SFO", "CAN", "2026-06-14", []flightSeats{{"CZ658", 7}, {"CZ302", 9}})
	if d := Diff(s, s); len(d) != 0 {
		t.Errorf("Diff of identical snapshots = %+v, want empty", d)
	}
}

func TestBookingURL(t *testing.T) {
	got := BookingURL("SFO", "CAN", "2026-06-14")
	want := "https://b2c.csair.com/ita/intl/zh/flights?flex=1&m=0&p=100&t=SFO-CAN-20260614&egs=ITA,ITA&open=1"
	if got != want {
		t.Errorf("BookingURL\n got: %s\nwant: %s", got, want)
	}
}

func TestChangeBody(t *testing.T) {
	prev := biz("SFO", "CAN", "2026-06-14", []flightSeats{
		{"CZ658", 8}, {"CZ330", 5}, {"CZ302", 9},
	})
	cur := biz("SFO", "CAN", "2026-06-14", []flightSeats{
		{"CZ658", 7}, {"CZ660+CZ3368", 2}, {"CZ302", 9}, {"CZ400", -1},
	})
	want := `SFO → CAN  ·  2026-06-14  ·  Business
As of 2026-06-03 09:07 PDT

Changed:
  CZ330:         5 → (gone)
  CZ400:         (new) → ⚠️ NO SEATS
  CZ658:         8 → 7 seats
  CZ660+CZ3368:  (new) → 2 seats

All business seats now:
  CZ302:         9 seats
  CZ658:         7 seats
  CZ660+CZ3368:  2 seats
  CZ400:         ⚠️ NO SEATS

Book: https://b2c.csair.com/ita/intl/zh/flights?flex=1&m=0&p=100&t=SFO-CAN-20260614&egs=ITA,ITA&open=1
`
	if got := ChangeBody(prev, cur, fixedNow); got != want {
		t.Errorf("ChangeBody mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestStatusBody(t *testing.T) {
	a := biz("SFO", "CAN", "2026-06-10", []flightSeats{{"CZ327", 9}, {"CZ658", 4}})
	b := biz("SFO", "CAN", "2026-06-14", []flightSeats{
		{"CZ302", 9}, {"CZ658", 7}, {"CZ660+CZ3368", 2}, {"CZ400", -1},
	})
	want := `All business seats now  ·  As of 2026-06-03 09:07 PDT

SFO → CAN  ·  2026-06-10  ·  Business
  CZ327:         9 seats
  CZ658:         4 seats
  Book: https://b2c.csair.com/ita/intl/zh/flights?flex=1&m=0&p=100&t=SFO-CAN-20260610&egs=ITA,ITA&open=1

SFO → CAN  ·  2026-06-14  ·  Business
  CZ302:         9 seats
  CZ658:         7 seats
  CZ660+CZ3368:  2 seats
  CZ400:         ⚠️ NO SEATS
  Book: https://b2c.csair.com/ita/intl/zh/flights?flex=1&m=0&p=100&t=SFO-CAN-20260614&egs=ITA,ITA&open=1
`
	if got := StatusBody([]Snapshot{a, b}, fixedNow); got != want {
		t.Errorf("StatusBody mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func ptrStr(p *int) string {
	if p == nil {
		return "nil"
	}
	return strconv.Itoa(*p)
}
