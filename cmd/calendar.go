package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var calendarCmd = &cobra.Command{
	Use:   "calendar [FROM TO]",
	Short: "Cheapest fare per date around a target (low-price calendar)",
	Args:  cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		// TODO: wire QueryService.Calendar over /ita/rest/intl/shop/lowPriceCalendarBySevenDay.
		return fmt.Errorf("calendar: not implemented yet")
	},
}

func init() { rootCmd.AddCommand(calendarCmd) }
