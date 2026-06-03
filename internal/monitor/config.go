package monitor

import (
	"errors"
	"fmt"
	"path/filepath"
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
