// Package domain holds the pure business models. No JSON tags, no I/O, no deps.
package domain

import "time"

// Cabin is a normalized travel cabin.
type Cabin string

const (
	CabinEconomy        Cabin = "economy"
	CabinPremiumEconomy Cabin = "premium"
	CabinBusiness       Cabin = "business"
	CabinFirst          Cabin = "first"
)

// SeatCap is the value at/above which the engine reports availability as a
// capped "N or more" rather than an exact count.
const SeatCap = 9

// Money is an amount in a given ISO-4217 currency.
type Money struct {
	Amount   float64
	Currency string
}

// Pax is a passenger mix.
type Pax struct {
	Adults   int
	Children int
	Infants  int
}

// SearchRequest is the input to a flight search (built from CLI flags/config).
type SearchRequest struct {
	Origin      string    // origin IATA, e.g. "SFO"
	Destination string    // destination IATA, e.g. "CAN"
	Date        time.Time // departure date (date-only; time component ignored)
	Pax         Pax
}

// Segment is a single operated/marketed flight leg in an itinerary.
type Segment struct {
	Carrier     string    // marketing carrier, e.g. "CZ"
	FlightNo    string    // e.g. "658"
	Origin      string    // departure airport IATA
	Destination string    // arrival airport IATA
	Departs     time.Time // local departure (with zone when available)
	Arrives     time.Time // local arrival (with zone when available)
	Aircraft    string    // equipment code, e.g. "77W"
	DepTerminal string
	ArrTerminal string
	CodeShare   bool
	Vias        []string // intermediate airports within this segment (through-flight stops), e.g. ["WUH"]
}

// Number is the human flight designator, e.g. "CZ658".
func (s Segment) Number() string { return s.Carrier + s.FlightNo }

// ClassAvail is one bookable RBD (booking class) bucket within a cabin — the
// fine-grained detail behind a cabin's availability.
type ClassAvail struct {
	Cabin        Cabin
	BookingClass string // RBD letter, e.g. "I"
	Seats        int    // available seats in this RBD
	AtLeast      bool   // true when Seats is the display cap (>= SeatCap → "N or more")
	Brand        string // fare brand code, e.g. "JFFA"
	Price        Money  // displayed price for this fare tier
}

// CabinAvail is the availability for a whole cabin on an itinerary.
//
// A cabin is sold through several fare tiers/booking classes; the *true* number
// of seats you can buy is the highest tier's count (paying up covers the cheaper
// seats), i.e. the max across Classes — not any single tier's count.
type CabinAvail struct {
	Cabin   Cabin
	Seats   int   // true availability = max seats across the cabin's booking classes
	AtLeast bool  // true when Seats is the display cap (>= SeatCap)
	From    Money // cheapest fare offered in this cabin
	Classes []ClassAvail
}

// Itinerary is one search result option (one or more connecting segments).
type Itinerary struct {
	Segments []Segment
	Stops    int
	Duration time.Duration
	Cabins   []CabinAvail
	Lowest   Money
}

// Path is the full airport sequence: origin, then each segment's intra-segment
// vias (through-flight stops) and its destination. A single-segment CZ660
// SFO→CAN with an internal Wuhan stop expands to SFO, WUH, CAN.
func (it Itinerary) Path() []string {
	if len(it.Segments) == 0 {
		return nil
	}
	pts := []string{it.Segments[0].Origin}
	for _, s := range it.Segments {
		pts = append(pts, s.Vias...)
		pts = append(pts, s.Destination)
	}
	return pts
}

// Vias is the intermediate airports only (Path without the first origin and the
// final destination) — the connection/through points, e.g. ["WUH"].
func (it Itinerary) Vias() []string {
	p := it.Path()
	if len(p) <= 2 {
		return nil
	}
	return p[1 : len(p)-1]
}

// SearchResult bundles a request with its itineraries.
type SearchResult struct {
	Request     SearchRequest
	Itineraries []Itinerary
}

// CalendarEntry is one day in the low-price calendar.
type CalendarEntry struct {
	Date   time.Time
	Lowest Money
}

// DateOption is one itinerary on a specific date together with the availability
// for the cabin of interest — the unit of a range scan.
type DateOption struct {
	Date      time.Time
	Itinerary Itinerary
	Cabin     CabinAvail
}

// OpenClasses is the count of distinct open booking classes in the cabin — a
// proxy for how empty the flight is that the 9-seat cap can't express.
func (o DateOption) OpenClasses() int { return len(o.Cabin.Classes) }

// ScanResult ranks date options across a range for a route+cabin.
type ScanResult struct {
	Origin, Destination string
	Cabin               Cabin
	Start, End          time.Time
	DirectOnly          bool
	Options             []DateOption // ranked best-first by the caller
	NoAvailability      []time.Time  // dates with no qualifying option
}

// CabinRank orders cabins from premium to economy for stable display.
func CabinRank(c Cabin) int {
	switch c {
	case CabinFirst:
		return 0
	case CabinBusiness:
		return 1
	case CabinPremiumEconomy:
		return 2
	case CabinEconomy:
		return 3
	default:
		return 9
	}
}
