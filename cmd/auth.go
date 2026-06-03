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
		p.Attach = flagAttach
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
			fmt.Printf("cookies:    %d\nacw_sc__v2: %s\nexpires:    %s (valid=%v)\ncache:      %s\n",
				len(t.Cookies), acwStr(t), t.Expires.Format(time.RFC3339), t.Valid(), p.CachePath)
			return nil
		}

		if flagAttach != "" {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"reading session from your Chrome on port %s…\n"+
					"→ first open https://b2c.csair.com in that Chrome and run one SFO→CAN search (solve any captcha).\n", flagAttach)
		} else {
			fmt.Fprintln(cmd.ErrOrStderr(), "bootstrapping session via Chrome…")
		}
		t, err := p.Refresh(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Printf("ok — session cached (%d cookies, acw_sc__v2: %s), expires %s\n%s\n",
			len(t.Cookies), acwStr(t), t.Expires.Format(time.RFC3339), p.CachePath)
		return nil
	},
}

func acwStr(t auth.Token) string {
	if t.AcwScV2 == "" {
		return "none (session-only)"
	}
	if len(t.AcwScV2) > 12 {
		return t.AcwScV2[:12] + "…"
	}
	return t.AcwScV2
}

func init() {
	f := authCmd.Flags()
	f.BoolVar(&authHeaded, "headed", false, "show the browser window (use if headless is challenged)")
	f.BoolVar(&authStatus, "status", false, "show the cached token + expiry")
	f.BoolVar(&authClear, "clear", false, "forget the cached token")
	f.StringVar(&authRoute, "route", "", "route to trigger the bootstrap search, e.g. SFO-CAN")
	rootCmd.AddCommand(authCmd)
}
