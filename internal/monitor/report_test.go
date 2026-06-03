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

func TestChangeDigest(t *testing.T) {
	// Two targets: one changed (06-14), one unchanged (06-10, excluded).
	prev14 := biz("SFO", "CAN", "2026-06-14", []flightSeats{
		{"CZ658", 8}, {"CZ330", 5}, {"CZ302", 9},
	})
	cur14 := biz("SFO", "CAN", "2026-06-14", []flightSeats{
		{"CZ658", 7}, {"CZ660+CZ3368", 2}, {"CZ302", 9}, {"CZ400", -1},
	})
	same10 := biz("SFO", "CAN", "2026-06-10", []flightSeats{{"CZ327", 9}})

	items := []DiffItem{
		{Prev: same10, Cur: same10}, // unchanged → omitted
		{Prev: prev14, Cur: cur14},  // changed
	}
	want := `Business seat changes  ·  As of 2026-06-03 09:07 PDT

SFO → CAN  ·  2026-06-14  ·  Business
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
	if got := ChangeDigest(items, fixedNow); got != want {
		t.Errorf("ChangeDigest mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestChangeDigestEmpty(t *testing.T) {
	s := biz("SFO", "CAN", "2026-06-14", []flightSeats{{"CZ658", 7}})
	if got := ChangeDigest([]DiffItem{{Prev: s, Cur: s}}, fixedNow); got != "" {
		t.Errorf("ChangeDigest with no changes = %q, want empty", got)
	}
}

func TestConfig(t *testing.T) {
	c := Config{
		SnapshotDir: "data/monitor",
		Targets: []Target{
			{From: "SFO", To: "CAN", Date: "2026-06-14"},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got, want := c.SnapshotPath(c.Targets[0]), "data/monitor/SFO-CAN-2026-06-14.json"; got != want {
		t.Errorf("SnapshotPath = %q, want %q", got, want)
	}

	bad := []Config{
		{Targets: []Target{{From: "SFO", To: "CAN", Date: "2026-06-14"}}}, // no dir
		{SnapshotDir: "d"}, // no targets
		{SnapshotDir: "d", Targets: []Target{{From: "SFO", To: "CAN"}}}, // missing date
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("bad[%d] Validate = nil, want error", i)
		}
	}
}

func TestAnyDue(t *testing.T) {
	// Fixed instant: 2026-06-14 23:30 Pacific (PDT) = 2026-06-15 06:30 UTC.
	// In Pacific it is still the 14th; in UTC it is already the 15th — the case
	// the departure-airport timezone must get right.
	la, _ := time.LoadLocation("America/Los_Angeles")
	now := time.Date(2026, 6, 14, 23, 30, 0, 0, la)

	sfo := func(string) (string, error) { return "America/Los_Angeles", nil }
	cfg := func(date string) Config {
		return Config{SnapshotDir: "d", Targets: []Target{{From: "SFO", To: "CAN", Date: date}}}
	}

	if !AnyDue(cfg("2026-06-14"), now, sfo) {
		t.Error("date == today (Pacific) should still be due")
	}
	if AnyDue(cfg("2026-06-13"), now, sfo) {
		t.Error("date strictly before today (Pacific) should be retired")
	}
	if !AnyDue(cfg("2026-06-15"), now, sfo) {
		t.Error("future date should be due")
	}

	// Same instant, but the date is in mainland China (Asia/Shanghai, already
	// the 15th there): the 14th has passed in the departure zone.
	cn := func(string) (string, error) { return "Asia/Shanghai", nil }
	if AnyDue(Config{SnapshotDir: "d", Targets: []Target{{From: "CAN", To: "SFO", Date: "2026-06-14"}}}, now, cn) {
		t.Error("CAN 06-14 should be retired when it is already 06-15 in Shanghai")
	}

	// Any due target keeps the whole set alive; unknown zone fails open.
	multi := Config{SnapshotDir: "d", Targets: []Target{
		{From: "SFO", To: "CAN", Date: "2026-06-01"}, // past
		{From: "SFO", To: "CAN", Date: "2026-06-20"}, // future
	}}
	if !AnyDue(multi, now, sfo) {
		t.Error("a future target should keep the set due")
	}
	bad := func(string) (string, error) { return "", errStub }
	if !AnyDue(cfg("2026-06-01"), now, bad) {
		t.Error("unresolvable zone should fail open (due)")
	}
}

var errStub = errorString("no zone")

type errorString string

func (e errorString) Error() string { return string(e) }

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
