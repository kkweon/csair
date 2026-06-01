package ita

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kkweon/csair/internal/clierr"
	"github.com/kkweon/csair/internal/domain"
)

// ParseService converts raw /ita engine payloads into domain models.
type ParseService interface {
	Execution(appHTML []byte) (string, error)
	Flights(grid []byte) ([]domain.Itinerary, error)
}

// parser is the default ParseService.
type parser struct{}

// NewParser returns the default ParseService.
func NewParser() ParseService { return parser{} }

var execRe = regexp.MustCompile(`execution=([a-f0-9]+)`)

// Execution pulls the flow execution token out of the /ita/intl/app HTML,
// which contains a JS redirect to `.../shop/?execution=<EXEC>`.
func (parser) Execution(appHTML []byte) (string, error) {
	m := execRe.FindSubmatch(appHTML)
	if m == nil {
		return "", fmt.Errorf("%w: no execution token in app response", clierr.ErrUpstream)
	}
	return string(m[1]), nil
}

// Flights maps the queryInterFlight grid into domain itineraries.
func (parser) Flights(grid []byte) ([]domain.Itinerary, error) {
	var resp queryResponse
	if err := json.Unmarshal(grid, &resp); err != nil {
		return nil, fmt.Errorf("%w: decode grid: %v", clierr.ErrUpstream, err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("%w: engine reported failure: %s", clierr.ErrUpstream, resp.ErrMsg)
	}
	dfs := resp.Data.Data.DateFlights
	if len(dfs) == 0 {
		return nil, clierr.ErrNoFlights
	}

	out := make([]domain.Itinerary, 0, len(dfs))
	for _, df := range dfs {
		it := domain.Itinerary{
			Stops:    df.StopNumber,
			Duration: time.Duration(df.Duration) * time.Minute,
			Segments: parseSegments(df.Segments),
			Cabins:   parseCabins(df.Prices),
		}
		it.Lowest = lowestPrice(df.Prices)
		out = append(out, it)
	}
	return out, nil
}

func parseSegments(segs []dtoSegment) []domain.Segment {
	res := make([]domain.Segment, 0, len(segs))
	for _, s := range segs {
		dep, arr := segTimes(s)
		res = append(res, domain.Segment{
			Carrier:     s.Carrier,
			FlightNo:    s.FlightNo,
			Origin:      s.DepPort,
			Destination: s.ArrPort,
			Departs:     dep,
			Arrives:     arr,
			Aircraft:    s.Plane,
			DepTerminal: s.DepTerm,
			ArrTerminal: s.ArrTerm,
			CodeShare:   s.CodeShare,
		})
	}
	return res
}

// segTimes prefers the zone-qualified leg times; falls back to date+time.
func segTimes(s dtoSegment) (dep, arr time.Time) {
	if len(s.Legs) > 0 {
		dep = parseZoned(s.Legs[0].DepTimeZone)
		arr = parseZoned(s.Legs[len(s.Legs)-1].ArrTimeZone)
	}
	if dep.IsZero() {
		dep = parseNaive(s.DepDate, s.DepTime)
	}
	if arr.IsZero() {
		arr = parseNaive(s.ArrDate, s.ArrTime)
	}
	return dep, arr
}

func parseZoned(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// e.g. "2026-06-14T00:35-07:00"
	t, err := time.Parse("2006-01-02T15:04-07:00", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseNaive(date, hm string) time.Time {
	if date == "" || hm == "" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02 15:04", date+" "+hm)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseCabins flattens prices→cabins into one CabinAvail per cabin.
//
// Within a cabin, each fare tier/RBD becomes a ClassAvail (deduped by RBD,
// cheapest price kept). The cabin's headline Seats is the MAX across those
// classes — the highest tier's count is the real availability, since buying the
// premium fare covers the cheaper seats. From is the cabin's cheapest fare.
func parseCabins(prices []dtoPrice) []domain.CabinAvail {
	type ckey struct {
		cabin domain.Cabin
		class string
	}
	classByKey := map[ckey]domain.ClassAvail{}
	for _, p := range prices {
		for _, c := range p.Cabins {
			seats, atLeast := parseSeats(c.BookingClassAvail)
			ca := domain.ClassAvail{
				Cabin:        normalizeCabin(c.Type),
				BookingClass: c.Name,
				Seats:        seats,
				AtLeast:      atLeast,
				Brand:        c.BrandCode,
				Price:        domain.Money{Amount: p.DisplayPrice, Currency: p.DisplayCurrency},
			}
			k := ckey{ca.Cabin, ca.BookingClass}
			if cur, ok := classByKey[k]; !ok || ca.Price.Amount < cur.Price.Amount {
				classByKey[k] = ca
			}
		}
	}

	byCabin := map[domain.Cabin]*domain.CabinAvail{}
	for _, ca := range classByKey {
		cb := byCabin[ca.Cabin]
		if cb == nil {
			cb = &domain.CabinAvail{Cabin: ca.Cabin, From: ca.Price}
			byCabin[ca.Cabin] = cb
		}
		cb.Classes = append(cb.Classes, ca)
		if ca.Seats > cb.Seats { // headline availability = max across tiers
			cb.Seats = ca.Seats
			cb.AtLeast = ca.AtLeast
		}
		if ca.Price.Amount > 0 && (cb.From.Amount == 0 || ca.Price.Amount < cb.From.Amount) {
			cb.From = ca.Price
		}
	}

	res := make([]domain.CabinAvail, 0, len(byCabin))
	for _, cb := range byCabin {
		sort.Slice(cb.Classes, func(i, j int) bool { return cb.Classes[i].BookingClass < cb.Classes[j].BookingClass })
		res = append(res, *cb)
	}
	sort.Slice(res, func(i, j int) bool {
		return domain.CabinRank(res[i].Cabin) < domain.CabinRank(res[j].Cabin)
	})
	return res
}

func parseSeats(s string) (n int, atLeast bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, n >= domain.SeatCap
}

func normalizeCabin(t string) domain.Cabin {
	switch strings.ToLower(strings.ReplaceAll(t, " ", "")) {
	case "first":
		return domain.CabinFirst
	case "business":
		return domain.CabinBusiness
	case "premiumeconomy", "pearleconomy":
		return domain.CabinPremiumEconomy
	case "economy":
		return domain.CabinEconomy
	default:
		return domain.Cabin(strings.ToLower(t))
	}
}

func lowestPrice(prices []dtoPrice) domain.Money {
	var m domain.Money
	for _, p := range prices {
		if p.DisplayPrice <= 0 {
			continue
		}
		if m.Amount == 0 || p.DisplayPrice < m.Amount {
			m = domain.Money{Amount: p.DisplayPrice, Currency: p.DisplayCurrency}
		}
	}
	return m
}
