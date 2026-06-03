package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kkweon/csair/internal/clierr"
	"github.com/kkweon/csair/internal/monitor"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Render seat-monitor email bodies from search snapshots",
	Long: `Render the seat-monitor email bodies from saved 'csair search --json' snapshots.

These back the scheduled monitor: 'diff' is the per-date change report (empty
when nothing changed) and 'status' is the combined current-status digest for
one or more dates (one email for all of them).`,
}

var reportDiffCmd = &cobra.Command{
	Use:   "diff OLD.json NEW.json",
	Short: "Print the business-seat change report (no output when unchanged)",
	Args:  cobra.ExactArgs(2),
	RunE:  runReportDiff,
}

var reportStatusCmd = &cobra.Command{
	Use:   "status SNAPSHOT.json [SNAPSHOT.json ...]",
	Short: "Print a combined current-status digest for one or more snapshots",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runReportStatus,
}

func init() {
	reportCmd.AddCommand(reportDiffCmd, reportStatusCmd)
	rootCmd.AddCommand(reportCmd)
}

func runReportDiff(cmd *cobra.Command, args []string) error {
	prev, err := loadSnapshot(args[0])
	if err != nil {
		return err
	}
	cur, err := loadSnapshot(args[1])
	if err != nil {
		return err
	}
	// Unchanged → no output (the orchestration reads "empty stdout" as no email).
	if len(monitor.Diff(prev, cur)) == 0 {
		return nil
	}
	fmt.Fprint(cmd.OutOrStdout(), monitor.ChangeBody(prev, cur, time.Now()))
	return nil
}

func runReportStatus(cmd *cobra.Command, args []string) error {
	snaps := make([]monitor.Snapshot, 0, len(args))
	for _, p := range args {
		s, err := loadSnapshot(p)
		if err != nil {
			return err
		}
		snaps = append(snaps, s)
	}
	fmt.Fprint(cmd.OutOrStdout(), monitor.StatusBody(snaps, time.Now()))
	return nil
}

func loadSnapshot(path string) (monitor.Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return monitor.Snapshot{}, fmt.Errorf("%w: %v", clierr.ErrUsage, err)
	}
	defer f.Close()
	s, err := monitor.ParseSnapshot(f)
	if err != nil {
		return monitor.Snapshot{}, fmt.Errorf("%w: %s: %v", clierr.ErrUsage, path, err)
	}
	return s, nil
}
