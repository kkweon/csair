package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kkweon/csair/internal/airport"
	"github.com/kkweon/csair/internal/auth"
	"github.com/kkweon/csair/internal/clierr"
	"github.com/kkweon/csair/internal/domain"
	"github.com/kkweon/csair/internal/ita"
	"github.com/kkweon/csair/internal/render"
	"github.com/kkweon/csair/internal/transport"
)

var (
	sFrom, sTo, sDate      string
	sCabin, sCarrier, sSort string
	sAdults, sChildren     int
	sInfants, sMinSeats    int
	sDirect                bool
)

var searchCmd = &cobra.Command{
	Use:   "search [FROM TO DATE]",
	Short: "Search a route+date and show seats available per booking class",
	Example: "  csair search SFO CAN 2026-06-14\n" +
		"  csair search --from SFO --to CAN --date 2026-06-14 --cabin business --direct",
	Args: cobra.MaximumNArgs(3),
	RunE: runSearch,
}

func init() {
	f := searchCmd.Flags()
	f.StringVar(&sFrom, "from", "", "origin IATA")
	f.StringVar(&sTo, "to", "", "destination IATA")
	f.StringVar(&sDate, "date", "", "departure date YYYY-MM-DD")
	f.IntVarP(&sAdults, "adults", "a", 1, "adult passengers")
	f.IntVarP(&sChildren, "children", "c", 0, "child passengers (2-11)")
	f.IntVarP(&sInfants, "infants", "i", 0, "lap infants (<2)")
	f.StringVar(&sCabin, "cabin", "all", "economy|premium|business|first|all")
	f.StringVar(&sCarrier, "carrier", "", "only this marketing carrier, e.g. CZ")
	f.BoolVar(&sDirect, "direct", false, "nonstop only")
	f.IntVar(&sMinSeats, "min-seats", 0, "only classes with >= N seats")
	f.StringVar(&sSort, "sort", "price", "price|duration|departure")
	rootCmd.AddCommand(searchCmd)
}

func runSearch(cmd *cobra.Command, args []string) error {
	from, to, date := sFrom, sTo, sDate
	if len(args) >= 1 {
		from = args[0]
	}
	if len(args) >= 2 {
		to = args[1]
	}
	if len(args) >= 3 {
		date = args[2]
	}
	if from == "" || to == "" || date == "" {
		return fmt.Errorf("%w: need FROM, TO and DATE (positional or --from/--to/--date)", clierr.ErrUsage)
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return fmt.Errorf("%w: bad date %q (want YYYY-MM-DD)", clierr.ErrUsage, date)
	}

	req := domain.SearchRequest{
		Origin:      strings.ToUpper(from),
		Destination: strings.ToUpper(to),
		Date:        d,
		Pax:         domain.Pax{Adults: sAdults, Children: sChildren, Infants: sInfants},
	}

	ctx := cmd.Context()
	tok, err := auth.EnvProvider{}.Token(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "no acw token — run 'csair auth' (bootstrap) or set CSAIR_ACW=<acw_sc__v2>")
		return err
	}
	hc, err := transport.New(tok)
	if err != nil {
		return err
	}

	q := ita.NewQueryService(hc, ita.NewParser(), airport.NewStatic())
	res, err := q.Search(ctx, req)
	if err != nil {
		return err
	}

	filterResult(res)
	return render.Result(os.Stdout, res, outputMode())
}

// filterResult applies --direct/--carrier/--cabin/--min-seats and --sort in place.
func filterResult(res *domain.SearchResult) {
	cabin := cabinFilter(sCabin)
	carrier := strings.ToUpper(sCarrier)

	kept := res.Itineraries[:0]
	for _, it := range res.Itineraries {
		if sDirect && it.Stops != 0 {
			continue
		}
		if carrier != "" && !hasCarrier(it, carrier) {
			continue
		}
		it.Cabins = filterCabins(it.Cabins, cabin)
		if len(it.Cabins) == 0 {
			continue
		}
		kept = append(kept, it)
	}
	res.Itineraries = kept
	sortItineraries(res.Itineraries, sSort)
}

func cabinFilter(s string) domain.Cabin {
	switch strings.ToLower(s) {
	case "economy", "y":
		return domain.CabinEconomy
	case "premium", "w":
		return domain.CabinPremiumEconomy
	case "business", "c", "j":
		return domain.CabinBusiness
	case "first", "f":
		return domain.CabinFirst
	default:
		return "" // all
	}
}

func filterCabins(cbs []domain.CabinAvail, cabin domain.Cabin) []domain.CabinAvail {
	out := cbs[:0]
	for _, cb := range cbs {
		if cabin != "" && cb.Cabin != cabin {
			continue
		}
		if cb.Seats < sMinSeats {
			continue
		}
		out = append(out, cb)
	}
	return out
}

func hasCarrier(it domain.Itinerary, carrier string) bool {
	for _, s := range it.Segments {
		if strings.EqualFold(s.Carrier, carrier) {
			return true
		}
	}
	return false
}

func sortItineraries(its []domain.Itinerary, by string) {
	switch by {
	case "duration":
		sort.SliceStable(its, func(i, j int) bool { return its[i].Duration < its[j].Duration })
	case "departure":
		sort.SliceStable(its, func(i, j int) bool {
			return its[i].Segments[0].Departs.Before(its[j].Segments[0].Departs)
		})
	default: // price
		sort.SliceStable(its, func(i, j int) bool { return its[i].Lowest.Amount < its[j].Lowest.Amount })
	}
}
