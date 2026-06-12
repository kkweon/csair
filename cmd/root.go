// Package cmd wires the Cobra commands and Viper config for csair.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/kkweon/csair/internal/clierr"
	"github.com/kkweon/csair/internal/render"
	"github.com/kkweon/csair/internal/transport"
)

var (
	flagJSON     bool
	flagTable    bool
	flagCSV      bool
	flagCurrency string
	flagVerbose  bool
	flagReauth   bool
	cfgFile      string
)

var rootCmd = &cobra.Command{
	Use:   "csair",
	Short: "Search China Southern Airlines flights and seat availability",
	Long: `csair searches China Southern Airlines (CZ) flights and shows the number of
seats available per cabin for a route and date.

Quickstart:
  csair auth                          # one-time: mint the anti-bot token
  csair search SFO CAN 2026-06-14     # search a route+date

Run 'csair <command> --help' for command-specific flags.`,
	Example: "  csair search SFO CAN 2026-06-14\n" +
		"  csair search --from SFO --to CAN --date 2026-06-14 --cabin business --direct\n" +
		"  csair search SFO CAN 2026-06-14 --json | jq",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command and maps errors to documented exit codes.
func Execute() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}
	fmt.Fprintln(os.Stderr, "Error:", err)

	usage := errors.Is(err, clierr.ErrUsage) ||
		strings.HasPrefix(err.Error(), "unknown command") ||
		strings.HasPrefix(err.Error(), "unknown flag") ||
		strings.HasPrefix(err.Error(), "unknown shorthand")
	if usage {
		fmt.Fprintf(os.Stderr, "\nRun '%s --help' or '%s <command> --help' for usage.\n", rootCmd.Name(), rootCmd.Name())
	}

	code := clierr.ExitCode(err)
	if usage && code == 1 {
		code = 2
	}
	os.Exit(code)
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.BoolVar(&flagJSON, "json", false, "output JSON")
	pf.BoolVar(&flagTable, "table", false, "output a table")
	pf.BoolVar(&flagCSV, "csv", false, "output CSV")
	pf.StringVar(&flagCurrency, "currency", "", "preferred display currency")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "verbose logging")
	pf.BoolVar(&flagReauth, "reauth", true, "on an anti-bot block, re-run the browser auth and retry once")
	pf.StringVar(&cfgFile, "config", "", "config file (default ~/.config/csair/config.toml)")

	// Turn flag-parse errors into typed usage errors with a help hint.
	rootCmd.SetFlagErrorFunc(func(c *cobra.Command, ferr error) error {
		return fmt.Errorf("%w: %v", clierr.ErrUsage, ferr)
	})

	cobra.OnInitialize(initConfig)
	_ = viper.BindPFlag("currency", pf.Lookup("currency"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else if dir, err := os.UserConfigDir(); err == nil {
		viper.AddConfigPath(filepath.Join(dir, "csair"))
		viper.SetConfigName("config")
		viper.SetConfigType("toml")
	}
	viper.SetEnvPrefix("CSAIR")
	viper.AutomaticEnv()
	_ = viper.ReadInConfig()
}

// verboseTransportOpts returns the transport options that enable per-request
// logging when --verbose (-v) is set, so interactive `search -v` narrates its
// HTTP calls to stdout. With the flag off it returns nothing (the default).
func verboseTransportOpts() []transport.Option {
	if flagVerbose {
		return []transport.Option{transport.WithVerbose(os.Stdout)}
	}
	return nil
}

// outputMode resolves the --json/--table/--csv flags into a render.Mode.
func outputMode() render.Mode {
	switch {
	case flagJSON:
		return render.JSON
	case flagCSV:
		return render.CSV
	case flagTable:
		return render.Table
	default:
		return render.Auto
	}
}
