package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/kkweon/csair/internal/auth"
)

var (
	authHeaded bool
	authStatus bool
	authClear  bool
	authRoute  string
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Bootstrap/refresh the acw_sc__v2 anti-bot token (drives Chrome)",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := auth.NewBrowserProvider()
		p.Headless = !authHeaded
		if authRoute != "" {
			p.Route = authRoute
		}

		switch {
		case authClear:
			if err := p.Clear(); err != nil {
				return err
			}
			fmt.Println("cleared cached token:", p.CachePath)
			return nil
		case authStatus:
			t, ok := p.Load()
			if !ok {
				fmt.Println("no cached token at", p.CachePath)
				return nil
			}
			fmt.Printf("acw_sc__v2: %s…\nexpires:    %s (valid=%v)\ncache:      %s\n",
				head(t.AcwScV2, 12), t.Expires.Format(time.RFC3339), t.Valid(), p.CachePath)
			return nil
		}

		fmt.Fprintln(cmd.ErrOrStderr(), "bootstrapping acw token via Chrome…")
		t, err := p.Refresh(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Printf("ok — harvested acw_sc__v2 (%s…), expires %s\ncached to %s\n",
			head(t.AcwScV2, 12), t.Expires.Format(time.RFC3339), p.CachePath)
		return nil
	},
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func init() {
	f := authCmd.Flags()
	f.BoolVar(&authHeaded, "headed", false, "show the browser window (use if headless is challenged)")
	f.BoolVar(&authStatus, "status", false, "show the cached token + expiry")
	f.BoolVar(&authClear, "clear", false, "forget the cached token")
	f.StringVar(&authRoute, "route", "", "route to trigger the bootstrap search, e.g. SFO-CAN")
	rootCmd.AddCommand(authCmd)
}
