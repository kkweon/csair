package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/kkweon/csair/internal/airport"
	"github.com/kkweon/csair/internal/auth"
	"github.com/kkweon/csair/internal/clierr"
	"github.com/kkweon/csair/internal/domain"
	"github.com/kkweon/csair/internal/ita"
	"github.com/kkweon/csair/internal/monitor"
	"github.com/kkweon/csair/internal/render"
	"github.com/kkweon/csair/internal/transport"
)

var (
	reportWrite bool
	reportOut   string
)

// Subject lines for the two report modes; emitted in the --out result so the
// orchestration script reads .subject instead of recomputing it.
const (
	subjectDiff   = "[csair] business seats changed"
	subjectStatus = "[csair] business seats — daily status"
)

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
	const outUsage = "write a structured JSON result to this path (also narrates progress to stdout)"
	reportDiffCmd.Flags().StringVar(&reportOut, "out", "", outUsage)
	reportStatusCmd.Flags().StringVar(&reportOut, "out", "", outUsage)
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
	log := reportLogger(cmd)
	now := time.Now()

	log.logf("status run: %d target(s) (config %s)", len(mc.Targets), viper.ConfigFileUsed())
	due, retired := dueTargets(mc, now, log)

	var snaps []monitor.Snapshot
	var targets []targetResult
	var summary reportSummary

	// Every target's departure date has passed in its own zone: nothing to
	// fetch. Not a failure — a clean no-op (no token, no email).
	if len(due) == 0 {
		log.logf("status: all %d target(s) retired (departure dates passed) — nothing to do", retired)
		if reportOut != "" {
			return writeReportResult(reportResult{
				Mode: "status", AsOf: now, Email: false, Subject: subjectStatus,
				Body: monitor.StatusBody(nil, now), Targets: targets, Summary: summary,
			})
		}
		return nil
	}

	qs, tok, err := newReportService(ctx)
	if err != nil {
		return err
	}
	log.logf("token: %s", tokenLine(tok))

	for _, t := range due {
		summary.Checked++
		tr := targetResult{From: t.From, To: t.To, Date: t.Date}
		res, err := searchTarget(ctx, qs, t, log)
		if err != nil {
			summary.Failed++
			tr.Outcome, tr.Error = "failed", err.Error()
			targets = append(targets, tr)
			fmt.Fprintf(os.Stderr, "report: %s→%s %s: %v (omitted)\n", t.From, t.To, t.Date, err)
			log.logf("%s→%s %s: FAILED: %v", t.From, t.To, t.Date, err)
			continue
		}
		snap := snapshotFromResult(res)
		snaps = append(snaps, snap)
		seats := snap.BusinessSeats()
		tr.OK, tr.Itineraries, tr.BusinessFlights, tr.Seats, tr.Outcome = true, len(res.Itineraries), len(seats), seats, "current"
		targets = append(targets, tr)
		log.logf("%s→%s %s: %d itinerary(ies), %d business flight(s)", t.From, t.To, t.Date, len(res.Itineraries), len(seats))
		log.logf("  seats: %s", seatsLine(seats))
	}
	// Error only when there was work and all of it failed (the WAF/token case),
	// never when targets were simply retired above.
	if summary.Checked > 0 && len(snaps) == 0 {
		return fmt.Errorf("%w: every monitored search failed", clierr.ErrBlocked)
	}
	body := monitor.StatusBody(snaps, now)
	log.logf("summary: checked %d, failed %d, retired %d", summary.Checked, summary.Failed, retired)
	log.logf("status digest: %d target(s) — emailing", len(snaps))

	if reportOut != "" {
		return writeReportResult(reportResult{
			Mode: "status", AsOf: now, Email: len(snaps) > 0, Subject: subjectStatus,
			Body: body, Token: tok, Targets: targets, Summary: summary,
		})
	}
	fmt.Fprint(cmd.OutOrStdout(), body)
	return nil
}

func runReportDiff(cmd *cobra.Command, args []string) error {
	mc, err := loadMonitorConfig()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	log := reportLogger(cmd)
	now := time.Now()

	log.logf("diff run: %d target(s) (config %s)", len(mc.Targets), viper.ConfigFileUsed())
	due, retired := dueTargets(mc, now, log)

	type fetched struct {
		path    string
		res     *domain.SearchResult
		cur     monitor.Snapshot
		prev    monitor.Snapshot
		hasPrev bool
	}

	var got []fetched
	var targets []targetResult
	var summary reportSummary

	// Every target's departure date has passed: nothing to diff (no token, no
	// email). Distinct from "all searches failed" below.
	if len(due) == 0 {
		log.logf("diff: all %d target(s) retired (departure dates passed) — nothing to do", retired)
		if reportOut != "" {
			return writeReportResult(reportResult{
				Mode: "diff", AsOf: now, Email: false, Subject: subjectDiff,
				Body: "", Targets: targets, Summary: summary,
			})
		}
		return nil
	}

	qs, tok, err := newReportService(ctx)
	if err != nil {
		return err
	}
	log.logf("token: %s", tokenLine(tok))

	for _, t := range due {
		summary.Checked++
		tr := targetResult{From: t.From, To: t.To, Date: t.Date}
		res, err := searchTarget(ctx, qs, t, log)
		if err != nil {
			summary.Failed++
			tr.Outcome, tr.Error = "failed", err.Error()
			targets = append(targets, tr)
			fmt.Fprintf(os.Stderr, "report: %s→%s %s: %v (omitted)\n", t.From, t.To, t.Date, err)
			log.logf("%s→%s %s: FAILED: %v", t.From, t.To, t.Date, err)
			continue
		}
		path := mc.SnapshotPath(t)
		cur := snapshotFromResult(res)
		prev, hasPrev, err := loadSnapshotFile(path)
		if err != nil {
			return err
		}
		seats := cur.BusinessSeats()
		tr.OK, tr.Itineraries, tr.BusinessFlights, tr.Seats, tr.Snapshot = true, len(res.Itineraries), len(seats), seats, path
		log.logf("%s→%s %s: %d itinerary(ies), %d business flight(s)", t.From, t.To, t.Date, len(res.Itineraries), len(seats))
		log.logf("  seats: %s", seatsLine(seats))

		if hasPrev {
			tr.Prior = "compared"
			if changes := monitor.Diff(prev, cur); len(changes) == 0 {
				tr.Outcome = "unchanged"
				summary.Unchanged++
				log.logf("  compared to %s — no change (%d flight(s) steady)", path, len(seats))
			} else {
				tr.Outcome, tr.Changes = "changed", changes
				summary.Changed++
				log.logf("  compared to %s — %d change(s): %s", path, len(changes), changesLine(changes))
			}
		} else {
			tr.Prior, tr.Outcome = "baseline", "baseline"
			summary.Baseline++
			log.logf("  first seen — baseline, no prior snapshot at %s", path)
		}
		targets = append(targets, tr)
		got = append(got, fetched{path: path, res: res, cur: cur, prev: prev, hasPrev: hasPrev})
	}
	// Error only when there was work and all of it failed (the WAF/token case),
	// never when targets were simply retired above.
	if summary.Checked > 0 && len(got) == 0 {
		return fmt.Errorf("%w: every monitored search failed", clierr.ErrBlocked)
	}

	var items []monitor.DiffItem
	for _, f := range got {
		if f.hasPrev {
			items = append(items, monitor.DiffItem{Prev: f.prev, Cur: f.cur})
		}
	}
	digest := monitor.ChangeDigest(items, now)

	// --write persists a snapshot for first-seen (baseline) and changed targets;
	// unchanged ones are left untouched to avoid no-op churn in git history.
	if reportWrite {
		for _, f := range got {
			switch {
			case !f.hasPrev:
				if err := writeSnapshotJSON(f.path, f.res); err != nil {
					return err
				}
				log.logf("baseline written %s", f.path)
			case len(monitor.Diff(f.prev, f.cur)) > 0:
				if err := writeSnapshotJSON(f.path, f.res); err != nil {
					return err
				}
				log.logf("snapshot updated %s", f.path)
			}
		}
	}

	log.logf("summary: checked %d, changed %d, baseline %d, unchanged %d, failed %d, retired %d",
		summary.Checked, summary.Changed, summary.Baseline, summary.Unchanged, summary.Failed, retired)
	email := digest != ""
	if email {
		log.logf("change digest ready (%d target(s) changed) — emailing", summary.Changed)
	} else {
		log.logf("no changes across %d compared target(s) — no email", summary.Changed+summary.Unchanged)
	}

	if reportOut != "" {
		return writeReportResult(reportResult{
			Mode: "diff", AsOf: now, Email: email, Subject: subjectDiff,
			Body: digest, Token: tok, Targets: targets, Summary: summary,
		})
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

// dueTargets splits the configured targets into the still-due ones (to be
// searched) and logs each retired target — one whose departure date has already
// passed in its DEPARTURE-airport timezone (monitor.TargetDue). Retired targets
// are dropped from the search/digest entirely; the log line makes each skip
// visible in the CI run. Returns the due targets (config order preserved) and
// the retired count for the summary line.
func dueTargets(mc monitor.Config, now time.Time, log rlogger) ([]monitor.Target, int) {
	zoneOf := airport.NewStatic().Zone
	var due []monitor.Target
	var retired int
	for _, t := range mc.Targets {
		if !monitor.TargetDue(t, now, zoneOf) {
			retired++
			log.logf("%s→%s %s: retired — departure date passed (departure-airport TZ); skipping", t.From, t.To, t.Date)
			continue
		}
		due = append(due, t)
	}
	return due, retired
}

// Monitor pacing: a whole report run shares one transport client, so the pacer
// throttles across every target's request (not just within a single search).
// The gap is longer than interactive search to stay under the WAF's burst
// threshold — a background run minutes-long is fine; bursts get challenged.
const (
	reportMinGap = 1500 * time.Millisecond
	reportJitter = 1000 * time.Millisecond
)

// newReportService builds the single paced query service shared by every target
// in a report run. Sharing one client means the transport pacer spans the whole
// run instead of resetting per target; WithPacing widens the gap for the monitor.
// It also returns where the token came from (for the result/narration), and in
// --out mode turns on per-request HTTP logging to stdout.
func newReportService(ctx context.Context) (ita.QueryService, tokenInfo, error) {
	tok, info, err := reportToken(ctx)
	if err != nil {
		return nil, tokenInfo{}, err
	}
	opts := []transport.Option{transport.WithPacing(reportMinGap, reportJitter)}
	if reportOut != "" {
		opts = append(opts, transport.WithVerbose(os.Stdout))
	}
	qs, err := newQueryService(tok, opts...)
	if err != nil {
		return nil, tokenInfo{}, err
	}
	return qs, info, nil
}

// searchTarget runs a direct, business-only search for one target on the shared,
// paced query service. The result is filtered and sorted to match a
// `search --json` snapshot.
func searchTarget(ctx context.Context, qs ita.QueryService, t monitor.Target, log rlogger) (*domain.SearchResult, error) {
	d, err := time.Parse("2006-01-02", t.Date)
	if err != nil {
		return nil, fmt.Errorf("%w: bad date %q", clierr.ErrUsage, t.Date)
	}
	req := domain.SearchRequest{
		Origin: strings.ToUpper(t.From), Destination: strings.ToUpper(t.To),
		Date: d, Pax: domain.Pax{Adults: 1},
	}
	res, err := qs.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	log.logf("  %s→%s %s: engine returned %d itinerary(ies) before business filter", t.From, t.To, t.Date, len(res.Itineraries))
	businessTracked(res, t)
	sortItineraries(res.Itineraries, "price")
	return res, nil
}

// reportToken returns the cached (else env) token without bootstrapping a
// browser; missing/expired is a token error the caller surfaces (exit 3). The
// tokenInfo records which source won, for the result file and narration.
func reportToken(ctx context.Context) (auth.Token, tokenInfo, error) {
	if t, ok := auth.NewBrowserProvider().Load(); ok && t.Valid() {
		return t, tokenInfo{Source: "cache", Cookies: len(t.Cookies), ACW: acwStr(t), Expires: expiresStr(t)}, nil
	}
	if t, err := (auth.EnvProvider{}).Token(ctx); err == nil {
		return t, tokenInfo{Source: "env", ACW: acwStr(t), Expires: expiresStr(t)}, nil
	}
	return auth.Token{}, tokenInfo{}, clierr.ErrTokenExpired
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
		mi := monitor.Itinerary{Stops: it.Stops, Via: it.Vias()}
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

// --- structured result + progress narration (--out mode) ---

// reportResult is the machine-readable document written to --out: an explicit
// email decision plus the full per-target story behind it, so the orchestration
// script never has to infer "no email" from an empty body.
type reportResult struct {
	Mode    string         `json:"mode"` // "diff" | "status"
	AsOf    time.Time      `json:"asOf"`
	Email   bool           `json:"email"`
	Subject string         `json:"subject"`
	Body    string         `json:"body"`
	Token   tokenInfo      `json:"token"`
	Targets []targetResult `json:"targets"`
	Summary reportSummary  `json:"summary"`
}

// tokenInfo records which token the report ran with (not the mint step) so the
// log can prove a valid session was actually loaded.
type tokenInfo struct {
	Source  string `json:"source"`            // "cache" | "env"
	Cookies int    `json:"cookies,omitempty"` // cached sessions only
	ACW     string `json:"acw,omitempty"`
	Expires string `json:"expires,omitempty"` // RFC3339; empty when unknown
}

// targetResult is one monitored route/date's outcome: what was fetched, whether
// it was compared or baselined, and the change verdict.
type targetResult struct {
	From            string           `json:"from"`
	To              string           `json:"to"`
	Date            string           `json:"date"`
	OK              bool             `json:"ok"`
	Error           string           `json:"error,omitempty"`
	Itineraries     int              `json:"itineraries"`
	BusinessFlights int              `json:"businessFlights"`
	Seats           map[string]int   `json:"seats,omitempty"` // flight key -> business seats
	Prior           string           `json:"prior,omitempty"` // "compared" | "baseline"
	Snapshot        string           `json:"snapshot,omitempty"`
	Outcome         string           `json:"outcome"` // changed|unchanged|baseline|current|failed
	Changes         []monitor.Change `json:"changes,omitempty"`
}

// reportSummary tallies the run for an at-a-glance "what happened".
type reportSummary struct {
	Checked   int `json:"checked"`
	Changed   int `json:"changed"`
	Baseline  int `json:"baseline"`
	Unchanged int `json:"unchanged"`
	Failed    int `json:"failed"`
}

// rlogger narrates report progress, prefixing each line with "report: ". A nil
// writer disables output, so the default (no --out) interactive mode stays quiet
// and only the email body reaches stdout.
type rlogger struct{ w io.Writer }

func (l rlogger) logf(format string, a ...any) {
	if l.w != nil {
		fmt.Fprintf(l.w, "report: "+format+"\n", a...)
	}
}

// reportLogger narrates to stdout only in --out mode (where stdout is free of
// the email body).
func reportLogger(cmd *cobra.Command) rlogger {
	if reportOut != "" {
		return rlogger{w: cmd.OutOrStdout()}
	}
	return rlogger{}
}

// writeReportResult encodes the result as indented JSON to the --out path.
func writeReportResult(r reportResult) error {
	if err := os.MkdirAll(filepath.Dir(reportOut), 0o755); err != nil {
		return err
	}
	f, err := os.Create(reportOut)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func expiresStr(t auth.Token) string {
	if t.Expires.IsZero() {
		return ""
	}
	return t.Expires.Format(time.RFC3339)
}

// tokenLine renders a tokenInfo for the narration.
func tokenLine(t tokenInfo) string {
	exp := t.Expires
	if exp == "" {
		exp = "unknown"
	}
	switch t.Source {
	case "cache":
		return fmt.Sprintf("cached browser session (%d cookies, acw %s, expires %s)", t.Cookies, t.ACW, exp)
	case "env":
		return fmt.Sprintf("CSAIR_ACW env token (acw %s, expires %s)", t.ACW, exp)
	default:
		return t.Source
	}
}

// seatsLine renders a "flight=seats" summary sorted by flight key, flagging
// sold-out flights as NO-SEATS.
func seatsLine(seats map[string]int) string {
	if len(seats) == 0 {
		return "(none)"
	}
	keys := make([]string, 0, len(seats))
	for k := range seats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if seats[k] == 0 {
			parts = append(parts, k+"=NO-SEATS")
		} else {
			parts = append(parts, fmt.Sprintf("%s=%d", k, seats[k]))
		}
	}
	return strings.Join(parts, " ")
}

// changesLine renders "CZ658 8→7; CZ660 new→0" for the narration.
func changesLine(changes []monitor.Change) string {
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		old, cur := "new", "gone"
		if c.Old != nil {
			old = strconv.Itoa(*c.Old)
		}
		if c.New != nil {
			cur = strconv.Itoa(*c.New)
		}
		parts = append(parts, fmt.Sprintf("%s %s→%s", c.Flight, old, cur))
	}
	return strings.Join(parts, "; ")
}
