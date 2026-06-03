package cmd

import (
	"context"
	"errors"
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
	sFrom, sTo, sDate       string
	sCabin, sCarrier, sSort string
	sAdults, sChildren      int
	sInfants, sMinSeats     int
	sDirect                 bool
)

var searchCmd = &cobra.Command{
	Use:   "search [FROM TO DATE]",
	Short: "Search a route+date and show seats available per booking class",
	Example: "  csair search SFO CAN 2026-06-14\n" +
		"  csair search --from SFO --to CAN --date 2026-06-14 --cabin business --direct",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 3 {
			return fmt.Errorf("%w: too many arguments — expected at most FROM TO DATE, got %d", clierr.ErrUsage, len(args))
		}
		return nil
	},
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
	f.IntVar(&sMinSeats, "min-seats", 0, "only cabins with >= N seats")
	f.StringVar(&sSort, "sort", "price", "price|duration|departure")
	f.StringVar(&sAcw, "acw", "", "acw_sc__v2 token (else cache/env/bootstrap)")
	f.BoolVar(&sNoBootstrap, "no-bootstrap", false, "do not auto-launch the browser bootstrap")
	f.BoolVar(&sHeaded, "headed", false, "show the browser window during bootstrap")
	rootCmd.AddCommand(searchCmd)
}

var (
	sAcw         string
	sNoBootstrap bool
	sHeaded      bool
)

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
		return fmt.Errorf("%w: need FROM, TO and DATE\n  e.g. csair search SFO CAN 2026-06-14\n  or   csair search --from SFO --to CAN --date 2026-06-14", clierr.ErrUsage)
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return fmt.Errorf("%w: bad date %q — use YYYY-MM-DD, e.g. 2026-06-14", clierr.ErrUsage, date)
	}
	if _, err := cabinFromFlag(sCabin); err != nil {
		return err
	}
	if !validSort(sSort) {
		return fmt.Errorf("%w: invalid --sort %q (want price, duration or departure)", clierr.ErrUsage, sSort)
	}
	if sAdults < 1 {
		return fmt.Errorf("%w: --adults must be at least 1", clierr.ErrUsage)
	}
	if sChildren < 0 || sInfants < 0 {
		return fmt.Errorf("%w: passenger counts cannot be negative", clierr.ErrUsage)
	}

	origin, dest := strings.ToUpper(from), strings.ToUpper(to)
	cat := airport.NewStatic()
	for _, code := range []string{origin, dest} {
		if _, err := cat.Country(code); err != nil {
			return fmt.Errorf("%w: unknown airport code %q — use a 3-letter IATA code (e.g. SFO, CAN, PEK)", clierr.ErrUsage, code)
		}
	}

	req := domain.SearchRequest{
		Origin:      origin,
		Destination: dest,
		Date:        d,
		Pax:         domain.Pax{Adults: sAdults, Children: sChildren, Infants: sInfants},
	}

	ctx := cmd.Context()
	prov, err := buildProvider(req.Origin, req.Destination)
	if err != nil {
		return err
	}
	tok, err := prov.Token(ctx)
	if err != nil {
		if errors.Is(err, clierr.ErrTokenExpired) {
			fmt.Fprintln(os.Stderr, "no acw token — run 'csair auth' or set CSAIR_ACW=<acw_sc__v2>")
		}
		return err
	}

	res, err := doSearch(ctx, tok, req)
	if isStale(err) && canReauth() {
		fmt.Fprintln(os.Stderr, "blocked by anti-bot — re-running browser auth and retrying…")
		if fresh, rerr := reauthToken(ctx, req.Origin, req.Destination); rerr == nil {
			res, err = doSearch(ctx, fresh, req)
		}
	}
	if err != nil {
		return err
	}

	filterResult(res)
	return render.Result(os.Stdout, res, outputMode())
}

func doSearch(ctx context.Context, tok auth.Token, req domain.SearchRequest) (*domain.SearchResult, error) {
	q, err := newQueryService(tok)
	if err != nil {
		return nil, err
	}
	return q.Search(ctx, req)
}

// newQueryService wires a paced transport + parser + catalog for one session.
// opts tune the transport (e.g. transport.WithPacing for the monitor's run-wide
// throttle); with none it keeps the interactive-search default pacing.
func newQueryService(tok auth.Token, opts ...transport.Option) (ita.QueryService, error) {
	hc, err := transport.New(tok, opts...)
	if err != nil {
		return nil, err
	}
	return ita.NewQueryService(hc, ita.NewParser(), airport.NewStatic()), nil
}

// canReauth reports whether an anti-bot block should trigger a fresh browser
// bootstrap (--reauth, on by default) unless bootstrapping is disabled.
func canReauth() bool { return flagReauth && !sNoBootstrap }

// reauthToken force-mints a fresh token via the browser, regardless of where
// the original token came from (cache, env, or --acw).
func reauthToken(ctx context.Context, origin, dest string) (auth.Token, error) {
	bp := auth.NewBrowserProvider()
	bp.Headless = !sHeaded
	if origin != "" && dest != "" {
		bp.Route = origin + "-" + dest
	}
	return bp.Refresh(ctx)
}

func isStale(err error) bool {
	return errors.Is(err, clierr.ErrBlocked) || errors.Is(err, clierr.ErrTokenExpired)
}

// buildProvider selects the token source: --acw flag, then (unless
// --no-bootstrap) the browser cache+bootstrap, else env, else cache-only.
func buildProvider(origin, dest string) (auth.Provider, error) {
	if sAcw != "" {
		return auth.Static{T: auth.Token{AcwScV2: sAcw, AcwTc: os.Getenv("CSAIR_ACW_TC")}}, nil
	}
	bp := auth.NewBrowserProvider()
	bp.Headless = !sHeaded
	bp.Route = origin + "-" + dest

	if sNoBootstrap {
		if t, ok := bp.Load(); ok && t.Valid() {
			return auth.Static{T: t}, nil
		}
		if t, err := (auth.EnvProvider{}).Token(context.Background()); err == nil {
			return auth.Static{T: t}, nil
		}
		return nil, clierr.ErrTokenExpired
	}
	if os.Getenv("CSAIR_ACW") != "" {
		return auth.EnvProvider{}, nil
	}
	return bp, nil
}

// filterResult applies --direct/--carrier/--cabin/--min-seats and --sort in place.
func filterResult(res *domain.SearchResult) {
	cabin, _ := cabinFromFlag(sCabin) // already validated in runSearch
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

// cabinFromFlag maps the --cabin value to a domain.Cabin ("" means "all"),
// returning a usage error for anything unrecognized.
func cabinFromFlag(s string) (domain.Cabin, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "all":
		return "", nil
	case "economy", "y":
		return domain.CabinEconomy, nil
	case "premium", "premiumeconomy", "w":
		return domain.CabinPremiumEconomy, nil
	case "business", "c", "j":
		return domain.CabinBusiness, nil
	case "first", "f":
		return domain.CabinFirst, nil
	default:
		return "", fmt.Errorf("%w: invalid --cabin %q (want all, economy, premium, business or first)", clierr.ErrUsage, s)
	}
}

func validSort(s string) bool {
	switch s {
	case "price", "duration", "departure":
		return true
	}
	return false
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
