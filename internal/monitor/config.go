package monitor

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// Target is one monitored route+date.
type Target struct {
	From string `mapstructure:"from"`
	To   string `mapstructure:"to"`
	Date string `mapstructure:"date"` // YYYY-MM-DD
}

// Config is the `[monitor]` section of the csair config file: which routes and
// dates to watch and where their snapshots live. It is the single source of
// truth shared by `report diff` and `report status`.
type Config struct {
	SnapshotDir string   `mapstructure:"snapshotDir"`
	Targets     []Target `mapstructure:"targets"`
}

// SnapshotPath is the on-disk snapshot for a target, e.g.
// "data/monitor/SFO-CAN-2026-06-14.json".
func (c Config) SnapshotPath(t Target) string {
	return filepath.Join(c.SnapshotDir, fmt.Sprintf("%s-%s-%s.json", t.From, t.To, t.Date))
}

// ZoneFunc resolves an IATA code to its IANA timezone name.
type ZoneFunc func(iata string) (string, error)

// AnyDue reports whether the monitor still has work to do: at least one target's
// departure date has not yet completely passed in that route's DEPARTURE-airport
// timezone. The comparison is inclusive — a target is due through all of its
// departure date (local), only retiring after local midnight rolls past it.
//
// Using the departure airport's own zone (not the runner's UTC) is the point:
// a SFO flight on the 14th is still "today" until Pacific midnight, hours after
// UTC has rolled to the 15th. A target whose zone or date can't be resolved is
// treated as due (fail open — never stop monitoring early).
func AnyDue(c Config, now time.Time, zoneOf ZoneFunc) bool {
	for _, t := range c.Targets {
		zone, err := zoneOf(t.From)
		if err != nil {
			return true
		}
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return true
		}
		if now.In(loc).Format("2006-01-02") <= t.Date {
			return true
		}
	}
	return false
}

// Validate checks the config is usable before any network work.
func (c Config) Validate() error {
	if c.SnapshotDir == "" {
		return errors.New("monitor.snapshotDir is required")
	}
	if len(c.Targets) == 0 {
		return errors.New("monitor.targets is empty")
	}
	for i, t := range c.Targets {
		if t.From == "" || t.To == "" || t.Date == "" {
			return fmt.Errorf("monitor.targets[%d] needs from, to and date", i)
		}
	}
	return nil
}
