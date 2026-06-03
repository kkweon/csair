// Package render formats a SearchResult as a table, JSON, or CSV. Output mode
// auto-detects: table on a TTY, JSON when piped/redirected.
package render

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kkweon/csair/internal/domain"
)

// Mode is the output format.
type Mode int

const (
	Auto Mode = iota
	Table
	JSON
	CSV
)

// Resolve turns Auto into Table (TTY) or JSON (piped).
func Resolve(m Mode, w io.Writer) Mode {
	if m != Auto {
		return m
	}
	if f, ok := w.(*os.File); ok && isTTY(f) {
		return Table
	}
	return JSON
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Result writes the search result to w in the given (possibly Auto) mode.
func Result(w io.Writer, res *domain.SearchResult, m Mode) error {
	switch Resolve(m, w) {
	case JSON:
		return renderJSON(w, res)
	case CSV:
		return renderCSV(w, res)
	default:
		return renderTable(w, res)
	}
}

func renderTable(w io.Writer, res *domain.SearchResult) error {
	r := res.Request
	fmt.Fprintf(w, "%s → %s  ·  %s  ·  %d adult(s)\n\n",
		r.Origin, r.Destination, r.Date.Format("2006-01-02"), maxi(r.Pax.Adults, 1))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "FLIGHT\tROUTING\tDEP\tARR\tDUR\tSTOPS\tSEATS\tFROM")
	for _, it := range res.Itineraries {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			flights(it), routing(it), clock(depart(it)), clock(arrive(it)),
			dur(it.Duration), stops(it.Stops), cabins(it.Cabins), money(it.Lowest))
	}
	return tw.Flush()
}

func renderJSON(w io.Writer, res *domain.SearchResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(toView(res))
}

func renderCSV(w io.Writer, res *domain.SearchResult) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"flights", "routing", "dep", "arr", "stops", "duration_min", "cabin", "seats", "at_least", "from_price", "currency"})
	for _, it := range res.Itineraries {
		base := []string{flights(it), routing(it), tstr(depart(it)), tstr(arrive(it)),
			strconv.Itoa(it.Stops), strconv.FormatInt(int64(it.Duration.Minutes()), 10)}
		for _, cb := range it.Cabins {
			row := append(append([]string{}, base...),
				string(cb.Cabin), strconv.Itoa(cb.Seats), strconv.FormatBool(cb.AtLeast),
				strconv.FormatFloat(cb.From.Amount, 'f', -1, 64), cb.From.Currency)
			_ = cw.Write(row)
		}
	}
	cw.Flush()
	return cw.Error()
}

// --- helpers ---

func flights(it domain.Itinerary) string {
	parts := make([]string, len(it.Segments))
	for i, s := range it.Segments {
		parts[i] = s.Number()
	}
	return strings.Join(parts, "+")
}

func routing(it domain.Itinerary) string {
	return strings.Join(it.Path(), "–")
}

func depart(it domain.Itinerary) time.Time {
	if len(it.Segments) == 0 {
		return time.Time{}
	}
	return it.Segments[0].Departs
}

func arrive(it domain.Itinerary) time.Time {
	if len(it.Segments) == 0 {
		return time.Time{}
	}
	return it.Segments[len(it.Segments)-1].Arrives
}

func clock(t time.Time) string {
	if t.IsZero() {
		return "--"
	}
	return t.Format("15:04")
}

func tstr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func dur(d time.Duration) string {
	d = d.Round(time.Minute)
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func stops(n int) string {
	if n == 0 {
		return "direct"
	}
	return fmt.Sprintf("%d-stop", n)
}

var cabinLabel = map[domain.Cabin]string{
	domain.CabinFirst: "First", domain.CabinBusiness: "Business",
	domain.CabinPremiumEconomy: "Premium", domain.CabinEconomy: "Economy",
}

func cabinName(c domain.Cabin) string {
	if s, ok := cabinLabel[c]; ok {
		return s
	}
	return string(c)
}

// cabins renders one headline seat count per cabin, e.g. "Business 8 · Economy 9+".
func cabins(cbs []domain.CabinAvail) string {
	var parts []string
	for _, cb := range cbs { // already cabin-rank sorted by the parser
		n := strconv.Itoa(cb.Seats)
		if cb.AtLeast {
			n += "+"
		}
		parts = append(parts, cabinName(cb.Cabin)+" "+n)
	}
	if len(parts) == 0 {
		return "--"
	}
	return strings.Join(parts, " · ")
}

func money(m domain.Money) string {
	if m.Amount == 0 {
		return "--"
	}
	return fmt.Sprintf("%s %.0f", m.Currency, m.Amount)
}

func maxi(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- JSON view (keeps domain free of json tags) ---

type view struct {
	Origin      string          `json:"origin"`
	Destination string          `json:"destination"`
	Date        string          `json:"date"`
	Itineraries []itineraryView `json:"itineraries"`
}

type itineraryView struct {
	Flights     []string    `json:"flights"`
	Routing     string      `json:"routing"`
	Via         []string    `json:"via,omitempty"`
	Departs     string      `json:"departs,omitempty"`
	Arrives     string      `json:"arrives,omitempty"`
	Stops       int         `json:"stops"`
	DurationMin int         `json:"durationMin"`
	Lowest      *moneyView  `json:"lowest,omitempty"`
	Cabins      []cabinView `json:"cabins"`
}

type cabinView struct {
	Cabin   string      `json:"cabin"`
	Seats   int         `json:"seats"` // true availability = max tier
	AtLeast bool        `json:"atLeast"`
	From    *moneyView  `json:"from,omitempty"`
	Classes []classView `json:"classes"` // per-RBD detail
}

type classView struct {
	Class   string  `json:"class"`
	Seats   int     `json:"seats"`
	AtLeast bool    `json:"atLeast"`
	Brand   string  `json:"brand,omitempty"`
	Price   float64 `json:"price"`
	Curr    string  `json:"currency"`
}

type moneyView struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

func toView(res *domain.SearchResult) view {
	v := view{Origin: res.Request.Origin, Destination: res.Request.Destination, Date: res.Request.Date.Format("2006-01-02")}
	for _, it := range res.Itineraries {
		iv := itineraryView{
			Flights: segNums(it), Routing: routing(it), Via: it.Vias(), Stops: it.Stops,
			DurationMin: int(it.Duration.Minutes()),
		}
		if d := depart(it); !d.IsZero() {
			iv.Departs = d.Format(time.RFC3339)
		}
		if a := arrive(it); !a.IsZero() {
			iv.Arrives = a.Format(time.RFC3339)
		}
		if it.Lowest.Amount > 0 {
			iv.Lowest = &moneyView{it.Lowest.Amount, it.Lowest.Currency}
		}
		for _, cb := range it.Cabins {
			cv := cabinView{Cabin: string(cb.Cabin), Seats: cb.Seats, AtLeast: cb.AtLeast}
			if cb.From.Amount > 0 {
				cv.From = &moneyView{cb.From.Amount, cb.From.Currency}
			}
			for _, ca := range cb.Classes {
				cv.Classes = append(cv.Classes, classView{
					Class: ca.BookingClass, Seats: ca.Seats, AtLeast: ca.AtLeast,
					Brand: ca.Brand, Price: ca.Price.Amount, Curr: ca.Price.Currency,
				})
			}
			iv.Cabins = append(iv.Cabins, cv)
		}
		v.Itineraries = append(v.Itineraries, iv)
	}
	return v
}

func segNums(it domain.Itinerary) []string {
	out := make([]string, len(it.Segments))
	for i, s := range it.Segments {
		out[i] = s.Number()
	}
	return out
}

// --- scan (date-range) rendering ---

// Scan writes a ranked date-range scan in the given (possibly Auto) mode.
func Scan(w io.Writer, res *domain.ScanResult, m Mode) error {
	switch Resolve(m, w) {
	case JSON:
		return scanJSON(w, res)
	case CSV:
		return scanCSV(w, res)
	default:
		return scanTable(w, res)
	}
}

func scanScope(res *domain.ScanResult) string {
	if res.DirectOnly {
		return "direct"
	}
	return "incl. connections"
}

func scanTable(w io.Writer, res *domain.ScanResult) error {
	fmt.Fprintf(w, "%s → %s  ·  %s  ·  %s  ·  %s → %s\n\n",
		res.Origin, res.Destination, scanScope(res), cabinName(res.Cabin),
		res.Start.Format("2006-01-02"), res.End.Format("2006-01-02"))

	if len(res.Options) == 0 {
		fmt.Fprintln(w, "no availability in range")
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "DATE\tDOW\tFLIGHT\tDEP→ARR\tSEATS\tOPEN CLASSES\tFROM")
		for _, o := range res.Options {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s→%s\t%s\t%s\t%s\n",
				o.Date.Format("2006-01-02"), o.Date.Format("Mon"),
				flights(o.Itinerary), clock(depart(o.Itinerary)), clock(arrive(o.Itinerary)),
				seatStr(o.Cabin), openClasses(o.Cabin), money(o.Cabin.From))
		}
		tw.Flush()
	}

	if len(res.NoAvailability) > 0 {
		ds := make([]string, len(res.NoAvailability))
		for i, d := range res.NoAvailability {
			ds[i] = d.Format("01-02")
		}
		fmt.Fprintf(w, "\nno %s availability: %s\n", cabinName(res.Cabin), strings.Join(ds, ", "))
	}
	return nil
}

func seatStr(cb domain.CabinAvail) string {
	s := strconv.Itoa(cb.Seats)
	if cb.AtLeast {
		s += "+"
	}
	return s
}

func classLetters(cb domain.CabinAvail) []string {
	letters := make([]string, len(cb.Classes))
	for i, c := range cb.Classes {
		letters[i] = c.BookingClass
	}
	sort.Strings(letters)
	return letters
}

func openClasses(cb domain.CabinAvail) string {
	if len(cb.Classes) == 0 {
		return "0"
	}
	l := classLetters(cb)
	return fmt.Sprintf("%d (%s)", len(l), strings.Join(l, "/"))
}

type scanOptView struct {
	Date        string     `json:"date"`
	Weekday     string     `json:"weekday"`
	Flights     []string   `json:"flights"`
	Departs     string     `json:"departs,omitempty"`
	Arrives     string     `json:"arrives,omitempty"`
	Seats       int        `json:"seats"`
	AtLeast     bool       `json:"atLeast"`
	OpenClasses int        `json:"openClasses"`
	Classes     []string   `json:"classes"`
	From        *moneyView `json:"from,omitempty"`
}

type scanView struct {
	Origin         string        `json:"origin"`
	Destination    string        `json:"destination"`
	Cabin          string        `json:"cabin"`
	DirectOnly     bool          `json:"directOnly"`
	Start          string        `json:"start"`
	End            string        `json:"end"`
	Options        []scanOptView `json:"options"`
	NoAvailability []string      `json:"noAvailability,omitempty"`
}

func toScanView(res *domain.ScanResult) scanView {
	v := scanView{
		Origin: res.Origin, Destination: res.Destination, Cabin: string(res.Cabin),
		DirectOnly: res.DirectOnly,
		Start:      res.Start.Format("2006-01-02"), End: res.End.Format("2006-01-02"),
	}
	for _, o := range res.Options {
		ov := scanOptView{
			Date: o.Date.Format("2006-01-02"), Weekday: o.Date.Format("Mon"),
			Flights: segNums(o.Itinerary), Seats: o.Cabin.Seats, AtLeast: o.Cabin.AtLeast,
			OpenClasses: o.OpenClasses(), Classes: classLetters(o.Cabin),
		}
		if d := depart(o.Itinerary); !d.IsZero() {
			ov.Departs = d.Format(time.RFC3339)
		}
		if a := arrive(o.Itinerary); !a.IsZero() {
			ov.Arrives = a.Format(time.RFC3339)
		}
		if o.Cabin.From.Amount > 0 {
			ov.From = &moneyView{o.Cabin.From.Amount, o.Cabin.From.Currency}
		}
		v.Options = append(v.Options, ov)
	}
	for _, d := range res.NoAvailability {
		v.NoAvailability = append(v.NoAvailability, d.Format("2006-01-02"))
	}
	return v
}

func scanJSON(w io.Writer, res *domain.ScanResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(toScanView(res))
}

func scanCSV(w io.Writer, res *domain.ScanResult) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"date", "weekday", "flights", "dep", "arr", "seats", "at_least", "open_classes", "classes", "from_price", "currency"})
	for _, o := range res.Options {
		_ = cw.Write([]string{
			o.Date.Format("2006-01-02"), o.Date.Format("Mon"),
			flights(o.Itinerary), tstr(depart(o.Itinerary)), tstr(arrive(o.Itinerary)),
			strconv.Itoa(o.Cabin.Seats), strconv.FormatBool(o.Cabin.AtLeast),
			strconv.Itoa(o.OpenClasses()), strings.Join(classLetters(o.Cabin), "/"),
			strconv.FormatFloat(o.Cabin.From.Amount, 'f', -1, 64), o.Cabin.From.Currency,
		})
	}
	cw.Flush()
	return cw.Error()
}
