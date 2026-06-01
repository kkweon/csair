package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kkweon/csair/internal/airport"
	"github.com/kkweon/csair/internal/clierr"
	"github.com/kkweon/csair/internal/domain"
	"github.com/kkweon/csair/internal/render"
)

const maxScanDays = 31

var (
	scanCabin   string
	scanCarrier string
	scanAny     bool
)

var scanCmd = &cobra.Command{
	Use:   "scan FROM TO START..END",
	Short: "Scan a date range and rank dates by availability (for standby travel)",
	Long: `Scan searches one date at a time across a range and ranks the dates by how
open the flights are — most availability first. Useful for standby/non-rev
travel where you want the emptiest dates.

Ranking favors the number of open booking classes (a better emptiness signal
than the seat count, which the engine caps at 9), then capped seats, then price.`,
	Example: "  csair scan SFO CAN 2026-06-10..2026-06-20\n" +
		"  csair scan SFO CAN 2026-06-10..2026-06-20 --cabin business --json",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 3 {
			return fmt.Errorf("%w: expected at most FROM TO START..END", clierr.ErrUsage)
		}
		return nil
	},
	RunE: runScan,
}

func init() {
	f := scanCmd.Flags()
	f.StringVar(&sFrom, "from", "", "origin IATA")
	f.StringVar(&sTo, "to", "", "destination IATA")
	f.StringVar(&scanCabin, "cabin", "economy", "cabin to rank: economy|premium|business|first")
	f.StringVar(&scanCarrier, "carrier", "", "only this marketing carrier, e.g. CZ")
	f.BoolVar(&scanAny, "any", false, "include connecting itineraries (default: direct only)")
	f.IntVarP(&sAdults, "adults", "a", 1, "adult passengers")
	f.BoolVar(&sNoBootstrap, "no-bootstrap", false, "do not auto-launch the browser bootstrap")
	f.BoolVar(&sHeaded, "headed", false, "show the browser window during bootstrap")
	f.StringVar(&sAcw, "acw", "", "acw_sc__v2 token (else cache/env/bootstrap)")
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	from, to, rng := sFrom, sTo, ""
	if len(args) >= 1 {
		from = args[0]
	}
	if len(args) >= 2 {
		to = args[1]
	}
	if len(args) >= 3 {
		rng = args[2]
	}
	if from == "" || to == "" || rng == "" {
		return fmt.Errorf("%w: need FROM, TO and START..END\n  e.g. csair scan SFO CAN 2026-06-10..2026-06-20", clierr.ErrUsage)
	}

	start, end, err := parseRange(rng)
	if err != nil {
		return err
	}
	cabin, err := cabinFromFlag(scanCabin)
	if err != nil {
		return err
	}
	if cabin == "" { // scan ranks a single cabin; default to economy
		cabin = domain.CabinEconomy
	}
	if sAdults < 1 {
		return fmt.Errorf("%w: --adults must be at least 1", clierr.ErrUsage)
	}

	origin, dest := strings.ToUpper(from), strings.ToUpper(to)
	cat := airport.NewStatic()
	for _, code := range []string{origin, dest} {
		if _, err := cat.Country(code); err != nil {
			return fmt.Errorf("%w: unknown airport code %q — use a 3-letter IATA code (e.g. SFO, CAN, PEK)", clierr.ErrUsage, code)
		}
	}

	ctx := cmd.Context()
	prov, err := buildProvider(origin, dest)
	if err != nil {
		return err
	}
	tok, err := prov.Token(ctx)
	if err != nil {
		if isStale(err) {
			fmt.Fprintln(os.Stderr, "no acw token — run 'csair auth' or set CSAIR_ACW=<acw_sc__v2>")
		}
		return err
	}
	qs, err := newQueryService(tok)
	if err != nil {
		return err
	}

	res := &domain.ScanResult{
		Origin: origin, Destination: dest, Cabin: cabin,
		Start: start, End: end, DirectOnly: !scanAny,
	}
	carrier := strings.ToUpper(scanCarrier)

	days := int(end.Sub(start).Hours()/24) + 1
	for i, d := 0, start; !d.After(end); i, d = i+1, d.AddDate(0, 0, 1) {
		fmt.Fprintf(os.Stderr, "\rscanning %s (%d/%d)…", d.Format("2006-01-02"), i+1, days)
		req := domain.SearchRequest{Origin: origin, Destination: dest, Date: d, Pax: domain.Pax{Adults: sAdults}}

		sr, err := qs.Search(ctx, req)
		if isStale(err) && canReauth() { // token went stale mid-scan: re-auth once and retry this date
			fmt.Fprintln(os.Stderr, "\nblocked by anti-bot — re-running browser auth…")
			if fresh, rerr := reauthToken(ctx, origin, dest); rerr == nil {
				if qs, err = newQueryService(fresh); err == nil {
					sr, err = qs.Search(ctx, req)
				}
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "\r%s: %v\n", d.Format("2006-01-02"), err)
			continue
		}

		if opt, ok := bestOption(d, sr.Itineraries, cabin, !scanAny, carrier); ok {
			res.Options = append(res.Options, opt)
		} else {
			res.NoAvailability = append(res.NoAvailability, d)
		}
	}
	fmt.Fprint(os.Stderr, "\r\033[K") // clear progress line

	rankOptions(res.Options)
	return render.Scan(os.Stdout, res, outputMode())
}

// bestOption picks, for one date, the qualifying itinerary with the most open
// classes in the chosen cabin.
func bestOption(date time.Time, its []domain.Itinerary, cabin domain.Cabin, directOnly bool, carrier string) (domain.DateOption, bool) {
	var best domain.DateOption
	found := false
	for _, it := range its {
		if directOnly && it.Stops != 0 {
			continue
		}
		if carrier != "" && !hasCarrier(it, carrier) {
			continue
		}
		cb, ok := cabinOf(it, cabin)
		if !ok {
			continue
		}
		cand := domain.DateOption{Date: date, Itinerary: it, Cabin: cb}
		if !found || moreOpen(cand, best) {
			best, found = cand, true
		}
	}
	return best, found
}

func cabinOf(it domain.Itinerary, cabin domain.Cabin) (domain.CabinAvail, bool) {
	for _, cb := range it.Cabins {
		if cb.Cabin == cabin {
			return cb, true
		}
	}
	return domain.CabinAvail{}, false
}

// moreOpen reports whether a ranks ahead of b (emptiest-first).
func moreOpen(a, b domain.DateOption) bool {
	if a.OpenClasses() != b.OpenClasses() {
		return a.OpenClasses() > b.OpenClasses()
	}
	if a.Cabin.Seats != b.Cabin.Seats {
		return a.Cabin.Seats > b.Cabin.Seats
	}
	af, bf := a.Cabin.From.Amount, b.Cabin.From.Amount
	if af > 0 && bf > 0 && af != bf {
		return af < bf
	}
	return a.Date.Before(b.Date)
}

func rankOptions(opts []domain.DateOption) {
	sort.SliceStable(opts, func(i, j int) bool { return moreOpen(opts[i], opts[j]) })
}

// parseRange parses "YYYY-MM-DD..YYYY-MM-DD".
func parseRange(s string) (start, end time.Time, err error) {
	parts := strings.SplitN(s, "..", 2)
	if len(parts) != 2 {
		return start, end, fmt.Errorf("%w: range must be START..END, e.g. 2026-06-10..2026-06-20", clierr.ErrUsage)
	}
	if start, err = time.Parse("2006-01-02", strings.TrimSpace(parts[0])); err != nil {
		return start, end, fmt.Errorf("%w: bad start date %q (want YYYY-MM-DD)", clierr.ErrUsage, parts[0])
	}
	if end, err = time.Parse("2006-01-02", strings.TrimSpace(parts[1])); err != nil {
		return start, end, fmt.Errorf("%w: bad end date %q (want YYYY-MM-DD)", clierr.ErrUsage, parts[1])
	}
	if end.Before(start) {
		return start, end, fmt.Errorf("%w: END (%s) is before START (%s)", clierr.ErrUsage, parts[1], parts[0])
	}
	if days := int(end.Sub(start).Hours()/24) + 1; days > maxScanDays {
		return start, end, fmt.Errorf("%w: range is %d days; max is %d (narrow it to stay under the rate limit)", clierr.ErrUsage, days, maxScanDays)
	}
	return start, end, nil
}
