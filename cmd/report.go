package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/kkweon/csair/internal/airport"
	"github.com/kkweon/csair/internal/auth"
	"github.com/kkweon/csair/internal/clierr"
	"github.com/kkweon/csair/internal/domain"
	"github.com/kkweon/csair/internal/monitor"
	"github.com/kkweon/csair/internal/render"
)

var reportWrite bool

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Render seat-monitor emails for the configured routes",
	Long: `Render the seat-monitor email bodies for the routes/dates in the [monitor]
section of the config file (pass --config). Both subcommands search every
configured target themselves and print one combined body.

  [monitor]
  snapshotDir = "data/monitor"
  [[monitor.targets]]
  from = "SFO"
  to   = "CAN"
  date = "2026-06-14"`,
}

var reportDiffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Combined business-seat change report across all targets (empty if none)",
	Args:  cobra.NoArgs,
	RunE:  runReportDiff,
}

var reportStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Combined current-status digest across all targets",
	Args:  cobra.NoArgs,
	RunE:  runReportStatus,
}

var reportDueCmd = &cobra.Command{
	Use:   "due",
	Short: "Print true while any target's date is still upcoming (departure-airport TZ)",
	Long: `Prints "true" while at least one monitored date has not yet completely passed
in its departure airport's local timezone, "false" once they all have. The
monitor uses this to auto-retire after the trip without watching departed dates.`,
	Args: cobra.NoArgs,
	RunE: runReportDue,
}

func init() {
	reportDiffCmd.Flags().BoolVar(&reportWrite, "write", false,
		"persist the freshly-fetched snapshot for new/changed targets to snapshotDir")
	reportCmd.AddCommand(reportDiffCmd, reportStatusCmd, reportDueCmd)
	rootCmd.AddCommand(reportCmd)
}

func runReportDue(cmd *cobra.Command, args []string) error {
	mc, err := loadMonitorConfig()
	if err != nil {
		return err
	}
	due := monitor.AnyDue(mc, time.Now(), airport.NewStatic().Zone)
	fmt.Fprintln(cmd.OutOrStdout(), due)
	return nil
}

func runReportStatus(cmd *cobra.Command, args []string) error {
	mc, err := loadMonitorConfig()
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	var snaps []monitor.Snapshot
	for _, t := range mc.Targets {
		res, err := searchTarget(ctx, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "report: %s→%s %s: %v (omitted)\n", t.From, t.To, t.Date, err)
			continue
		}
		snaps = append(snaps, snapshotFromResult(res))
	}
	if len(snaps) == 0 {
		return fmt.Errorf("%w: every monitored search failed", clierr.ErrBlocked)
	}
	fmt.Fprint(cmd.OutOrStdout(), monitor.StatusBody(snaps, time.Now()))
	return nil
}

func runReportDiff(cmd *cobra.Command, args []string) error {
	mc, err := loadMonitorConfig()
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	type fetched struct {
		path    string
		res     *domain.SearchResult
		cur     monitor.Snapshot
		prev    monitor.Snapshot
		hasPrev bool
	}

	var got []fetched
	for _, t := range mc.Targets {
		res, err := searchTarget(ctx, t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "report: %s→%s %s: %v (omitted)\n", t.From, t.To, t.Date, err)
			continue
		}
		f := fetched{path: mc.SnapshotPath(t), res: res, cur: snapshotFromResult(res)}
		prev, ok, err := loadSnapshotFile(f.path)
		if err != nil {
			return err
		}
		f.prev, f.hasPrev = prev, ok
		got = append(got, f)
	}
	if len(got) == 0 {
		return fmt.Errorf("%w: every monitored search failed", clierr.ErrBlocked)
	}

	var items []monitor.DiffItem
	for _, f := range got {
		if f.hasPrev {
			items = append(items, monitor.DiffItem{Prev: f.prev, Cur: f.cur})
		}
	}
	digest := monitor.ChangeDigest(items, time.Now())

	// --write persists a snapshot for first-seen (baseline) and changed targets;
	// unchanged ones are left untouched to avoid no-op churn in git history.
	if reportWrite {
		for _, f := range got {
			switch {
			case !f.hasPrev:
				if err := writeSnapshotJSON(f.path, f.res); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "report: baseline written %s\n", f.path)
			case len(monitor.Diff(f.prev, f.cur)) > 0:
				if err := writeSnapshotJSON(f.path, f.res); err != nil {
					return err
				}
			}
		}
	}

	fmt.Fprint(cmd.OutOrStdout(), digest)
	return nil
}

// loadMonitorConfig reads and validates the [monitor] section of the config.
func loadMonitorConfig() (monitor.Config, error) {
	var mc monitor.Config
	if err := viper.UnmarshalKey("monitor", &mc); err != nil {
		return mc, fmt.Errorf("%w: bad monitor config: %v", clierr.ErrUsage, err)
	}
	if err := mc.Validate(); err != nil {
		return mc, fmt.Errorf("%w: %v (set [monitor] in --config)", clierr.ErrUsage, err)
	}
	return mc, nil
}

// searchTarget runs a direct, business-only search for one target using the
// cached/env token (no browser bootstrap — the monitor mints it in 'csair auth'
// first). The result is filtered and sorted to match a `search --json` snapshot.
func searchTarget(ctx context.Context, t monitor.Target) (*domain.SearchResult, error) {
	d, err := time.Parse("2006-01-02", t.Date)
	if err != nil {
		return nil, fmt.Errorf("%w: bad date %q", clierr.ErrUsage, t.Date)
	}
	origin, dest := strings.ToUpper(t.From), strings.ToUpper(t.To)
	tok, err := reportToken(ctx, origin, dest)
	if err != nil {
		return nil, err
	}
	req := domain.SearchRequest{Origin: origin, Destination: dest, Date: d, Pax: domain.Pax{Adults: 1}}
	res, err := doSearch(ctx, tok, req)
	if err != nil {
		return nil, err
	}
	businessTracked(res, t)
	sortItineraries(res.Itineraries, "price")
	return res, nil
}

// reportToken returns the cached (else env) token without bootstrapping a
// browser; missing/expired is a token error the caller surfaces (exit 3).
func reportToken(ctx context.Context, origin, dest string) (auth.Token, error) {
	bp := auth.NewBrowserProvider()
	bp.Route = origin + "-" + dest
	if t, ok := bp.Load(); ok && t.Valid() {
		return t, nil
	}
	if t, err := (auth.EnvProvider{}).Token(ctx); err == nil {
		return t, nil
	}
	return auth.Token{}, clierr.ErrTokenExpired
}

// businessTracked keeps the business cabin of the itineraries this target
// watches: when t.Flights is set, exactly those flight keys (any stop count);
// otherwise nonstop itineraries only — matching how the monitor snapshots are
// produced. Itineraries with no business inventory are dropped.
func businessTracked(res *domain.SearchResult, t monitor.Target) {
	allow := make(map[string]bool, len(t.Flights))
	for _, f := range t.Flights {
		allow[strings.ToUpper(strings.TrimSpace(f))] = true
	}
	kept := res.Itineraries[:0]
	for _, it := range res.Itineraries {
		if len(allow) > 0 {
			if !allow[strings.ToUpper(flightKey(it))] { // allowlist: any stop count
				continue
			}
		} else if it.Stops != 0 { // default: nonstop only
			continue
		}
		cabs := it.Cabins[:0]
		for _, cb := range it.Cabins {
			if cb.Cabin == domain.CabinBusiness {
				cabs = append(cabs, cb)
			}
		}
		if len(cabs) == 0 {
			continue
		}
		it.Cabins = cabs
		kept = append(kept, it)
	}
	res.Itineraries = kept
}

// flightKey is the itinerary's flight key — segment numbers joined by "+", e.g.
// "CZ660" or "CZ660+CZ8004". It mirrors the snapshot's "flights" projection
// (render.segNums) and monitor.Itinerary.FlightKey, so the allowlist match
// agrees with the key later re-parsed from the snapshot.
func flightKey(it domain.Itinerary) string {
	nums := make([]string, len(it.Segments))
	for i, s := range it.Segments {
		nums[i] = s.Number()
	}
	return strings.Join(nums, "+")
}

// snapshotFromResult projects a live search result onto the monitor's snapshot
// shape (flights + business seat counts) for diffing/rendering.
func snapshotFromResult(res *domain.SearchResult) monitor.Snapshot {
	s := monitor.Snapshot{
		Origin:      res.Request.Origin,
		Destination: res.Request.Destination,
		Date:        res.Request.Date.Format("2006-01-02"),
	}
	for _, it := range res.Itineraries {
		mi := monitor.Itinerary{}
		for _, seg := range it.Segments {
			mi.Flights = append(mi.Flights, seg.Number())
		}
		for _, cb := range it.Cabins {
			mi.Cabins = append(mi.Cabins, monitor.Cabin{Cabin: string(cb.Cabin), Seats: cb.Seats})
		}
		s.Itineraries = append(s.Itineraries, mi)
	}
	return s
}

// loadSnapshotFile reads a stored snapshot. ok is false (no error) when the file
// does not exist yet — the first-run baseline case.
func loadSnapshotFile(path string) (monitor.Snapshot, bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return monitor.Snapshot{}, false, nil
	}
	if err != nil {
		return monitor.Snapshot{}, false, fmt.Errorf("%w: %v", clierr.ErrUsage, err)
	}
	defer f.Close()
	s, err := monitor.ParseSnapshot(f)
	if err != nil {
		return monitor.Snapshot{}, false, fmt.Errorf("%w: %s: %v", clierr.ErrUsage, path, err)
	}
	return s, true, nil
}

// writeSnapshotJSON persists a result as the same JSON a `search --json` emits,
// so committed snapshots keep their full shape and history.
func writeSnapshotJSON(path string, res *domain.SearchResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return render.Result(f, res, render.JSON)
}
