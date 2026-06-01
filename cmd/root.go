// Package cmd wires the Cobra commands and Viper config for csair.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/kkweon/csair/internal/clierr"
	"github.com/kkweon/csair/internal/render"
)

var (
	flagJSON     bool
	flagTable    bool
	flagCSV      bool
	flagCurrency string
	flagVerbose  bool
	cfgFile      string
)

var rootCmd = &cobra.Command{
	Use:           "csair",
	Short:         "Search China Southern Airlines flights and seat availability",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command and maps errors to documented exit codes.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(clierr.ExitCode(err))
	}
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.BoolVar(&flagJSON, "json", false, "output JSON")
	pf.BoolVar(&flagTable, "table", false, "output a table")
	pf.BoolVar(&flagCSV, "csv", false, "output CSV")
	pf.StringVar(&flagCurrency, "currency", "", "preferred display currency")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "verbose logging")
	pf.StringVar(&cfgFile, "config", "", "config file (default ~/.config/csair/config.toml)")

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
