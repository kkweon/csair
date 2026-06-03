// Package monitor renders the seat-monitor email bodies: a per-date change
// report (old → new) and a combined current-status digest across one or more
// dates. It deliberately works on a small Snapshot struct parsed from
// `csair search --json` output rather than the live domain types, so the
// rendering is pure and unit-testable with fixtures (no network, no clock).
package monitor

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	// Embed the zone database so "America/Los_Angeles" resolves to PDT/PST in
	// any runtime (CI containers, scratch images) — the "as of" stamp depends
	// on it. Also linked into the test binary, keeping golden tests stable.
	_ "time/tzdata"
)

// businessCabin is the cabin name the monitor watches (snapshots emit lowercase).
const businessCabin = "business"

// reportZone is the traveler-facing zone for the "as of" freshness stamp.
const reportZone = "America/Los_Angeles"

// Snapshot is the subset of a `csair search --json` document the monitor reads.
type Snapshot struct {
	Origin      string      `json:"origin"`
	Destination string      `json:"destination"`
	Date        string      `json:"date"` // YYYY-MM-DD
	Itineraries []Itinerary `json:"itineraries"`
}

// Itinerary is one option: its flight legs and per-cabin availability.
type Itinerary struct {
	Flights []string `json:"flights"`
	Cabins  []Cabin  `json:"cabins"`
}

// Cabin is one cabin's headline seat count on an itinerary.
type Cabin struct {
	Cabin string `json:"cabin"`
	Seats int    `json:"seats"`
}

// ParseSnapshot decodes one snapshot from a `csair search --json` document.
func ParseSnapshot(r io.Reader) (Snapshot, error) {
	var s Snapshot
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}

// FlightKey joins the itinerary's flight numbers, e.g. "CZ658" or, for a
// connection, "CZ660+CZ3368".
func (it Itinerary) FlightKey() string { return strings.Join(it.Flights, "+") }

// BusinessSeats maps flight key -> business-cabin seat count for the snapshot.
// A flight present without a business cabin reads as 0 (sold out / no business
// inventory) rather than being dropped, so the reports can surface it as
// "NO SEATS".
func (s Snapshot) BusinessSeats() map[string]int {
	m := make(map[string]int, len(s.Itineraries))
	for _, it := range s.Itineraries {
		seats := 0
		for _, c := range it.Cabins {
			if strings.EqualFold(c.Cabin, businessCabin) {
				seats = c.Seats
				break
			}
		}
		m[it.FlightKey()] = seats
	}
	return m
}

// Change is one flight's business-seat change between two snapshots. A nil Old
// means the flight is newly present; a nil New means it disappeared.
type Change struct {
	Flight string
	Old    *int
	New    *int
}

// Diff returns the flights whose business-seat count changed from prev to cur,
// sorted by flight key. It is empty when nothing changed (price-only moves are
// invisible here — only seat counts are compared).
func Diff(prev, cur Snapshot) []Change {
	pm, cm := prev.BusinessSeats(), cur.BusinessSeats()

	keys := make(map[string]struct{}, len(pm)+len(cm))
	for k := range pm {
		keys[k] = struct{}{}
	}
	for k := range cm {
		keys[k] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)

	var out []Change
	for _, k := range ordered {
		pv, pok := pm[k]
		cv, cok := cm[k]
		if pok && cok && pv == cv {
			continue // unchanged
		}
		ch := Change{Flight: k}
		if pok {
			ch.Old = intPtr(pv)
		}
		if cok {
			ch.New = intPtr(cv)
		}
		out = append(out, ch)
	}
	return out
}

// BookingURL builds the China Southern deep-link for a route and date (date as
// YYYY-MM-DD). Clicking it lands on availability for that exact route/date.
// (Mirrors the flights-page format the browser bootstrap uses.)
func BookingURL(origin, dest, date string) string {
	ymd := strings.ReplaceAll(date, "-", "")
	return fmt.Sprintf(
		"https://b2c.csair.com/ita/intl/zh/flights?flex=1&m=0&p=100&t=%s-%s-%s&egs=ITA,ITA&open=1",
		origin, dest, ymd)
}

// ChangeBody renders the per-date change email: the route/date header, an "as
// of" stamp, the changed flights (old → new, with (new)/(gone) and a NO SEATS
// flag at zero), the full current seat map, and a booking link.
func ChangeBody(prev, cur Snapshot, now time.Time) string {
	var b strings.Builder
	fmt.Fprintln(&b, headerLine(cur))
	fmt.Fprintln(&b, asOfLine(now))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Changed:")
	for _, ch := range Diff(prev, cur) {
		fmt.Fprintln(&b, changeLine(ch))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "All business seats now:")
	writeSeatLines(&b, cur)
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Book: %s\n", BookingURL(cur.Origin, cur.Destination, cur.Date))
	return b.String()
}

// StatusBody renders the combined current-status digest: one "as of" stamp,
// then a per-date section (header, full seat map, booking link) for every
// snapshot. One email covers all monitored dates/routes.
func StatusBody(snaps []Snapshot, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "All business seats now  ·  %s\n", asOfLine(now))
	for _, s := range snaps {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, headerLine(s))
		writeSeatLines(&b, s)
		fmt.Fprintf(&b, "  Book: %s\n", BookingURL(s.Origin, s.Destination, s.Date))
	}
	return b.String()
}

// --- rendering helpers ---

func headerLine(s Snapshot) string {
	return fmt.Sprintf("%s → %s  ·  %s  ·  Business", s.Origin, s.Destination, s.Date)
}

// asOfLine formats now in the traveler's zone as "As of 2006-01-02 15:04 PDT"
// (the abbreviation auto-switches PDT/PST), falling back to UTC if the zone is
// somehow unavailable.
func asOfLine(now time.Time) string {
	loc, err := time.LoadLocation(reportZone)
	if err != nil {
		loc = time.UTC
	}
	return "As of " + now.In(loc).Format("2006-01-02 15:04 MST")
}

// writeSeatLines writes the "all business seats now" block: every flight in the
// snapshot, sorted by seats desc then flight asc, zero flagged as NO SEATS.
func writeSeatLines(b *strings.Builder, s Snapshot) {
	for _, e := range sortedSeats(s.BusinessSeats()) {
		fmt.Fprintln(b, seatLine(e.flight, e.seats))
	}
}

type seatEntry struct {
	flight string
	seats  int
}

func sortedSeats(m map[string]int) []seatEntry {
	es := make([]seatEntry, 0, len(m))
	for f, n := range m {
		es = append(es, seatEntry{f, n})
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].seats != es[j].seats {
			return es[i].seats > es[j].seats // most seats first
		}
		return es[i].flight < es[j].flight // then flight asc
	})
	return es
}

// seatLine renders one "  CZ658:        7 seats" / "  CZ400:        ⚠️ NO SEATS"
// row. The flight label column is padded to a fixed width; labels are ASCII so
// byte and display width match.
func seatLine(flight string, seats int) string {
	val := "⚠️ NO SEATS"
	if seats > 0 {
		val = fmt.Sprintf("%d seats", seats)
	}
	return fmt.Sprintf("  %-15s%s", flight+":", val)
}

// changeLine renders one "  CZ658:        8 → 7 seats" row: the old count (or
// "(new)"), an arrow, then the new count (or "(gone)" / NO SEATS at zero).
func changeLine(ch Change) string {
	old := "(new)"
	if ch.Old != nil {
		old = fmt.Sprintf("%d", *ch.Old)
	}
	var cur string
	switch {
	case ch.New == nil:
		cur = "(gone)"
	case *ch.New == 0:
		cur = "⚠️ NO SEATS"
	default:
		cur = fmt.Sprintf("%d seats", *ch.New)
	}
	return fmt.Sprintf("  %-15s%s → %s", ch.Flight+":", old, cur)
}

func intPtr(n int) *int { return &n }
