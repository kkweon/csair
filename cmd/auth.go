package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Bootstrap/refresh the acw_sc__v2 anti-bot token",
	RunE: func(cmd *cobra.Command, args []string) error {
		// TODO(bootstrap): drive headed Chrome (chromedp) to harvest acw_sc__v2,
		// then cache it under ~/.config/csair/cookies.json. Until then, harvest
		// the cookie manually and export it.
		fmt.Println(`csair auth — headed-Chrome bootstrap is not implemented yet.

For now, harvest the token manually:
  1. Open https://b2c.csair.com/ita/book/zh/booking in Chrome.
  2. DevTools → Application → Cookies → copy the "acw_sc__v2" value.
  3. export CSAIR_ACW='<that value>'   (optionally CSAIR_ACW_TC for acw_tc)

Then run:  csair search SFO CAN 2026-06-14`)
		return nil
	},
}

func init() { rootCmd.AddCommand(authCmd) }
