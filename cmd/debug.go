package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kkweon/csair/internal/auth"
)

var (
	captureWait time.Duration
	captureOut  string
)

var debugCmd = &cobra.Command{
	Use:    "debug",
	Short:  "Diagnostics for comparing the CLI against the live site",
	Hidden: true,
}

var debugCaptureCmd = &cobra.Command{
	Use:   "capture FROM TO YYYY-MM-DD",
	Short: "Open the booking page in Chrome and print the /ita/rest requests it sends",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to := strings.ToUpper(args[0]), strings.ToUpper(args[1])
		d, err := time.Parse("2006-01-02", args[2])
		if err != nil {
			return fmt.Errorf("date: %w", err)
		}
		xs, err := auth.NewBrowserProvider().Capture(cmd.Context(), from, to, d.Format("20060102"), captureWait)
		if captureOut != "" {
			if mkErr := os.MkdirAll(captureOut, 0o755); mkErr != nil {
				return mkErr
			}
		}
		for i, x := range xs {
			fmt.Printf("=== #%d %s %s -> %d (%d bytes)\n", i+1, x.Method, x.URL, x.Status, len(x.Body))
			fmt.Printf("request body:\n%s\n", x.PostData)
			if captureOut != "" {
				base := filepath.Join(captureOut, fmt.Sprintf("%02d-%s", i+1, filepath.Base(strings.SplitN(x.URL, "?", 2)[0])))
				_ = os.WriteFile(base+".request.txt", []byte(x.PostData), 0o644)
				_ = os.WriteFile(base+".response.json", []byte(x.Body), 0o644)
			}
			printResponseSummary(x.Body)
		}
		fmt.Printf("captured %d /ita/rest request(s)\n", len(xs))
		return err
	},
}

// printResponseSummary prints the fields that explain a search outcome. Small
// bodies print whole; large ones (full flight grids) are written to --out.
func printResponseSummary(body string) {
	if len(body) <= 4096 {
		fmt.Printf("response body:\n%s\n", body)
		return
	}
	var r struct {
		Success  bool   `json:"success"`
		ErrorMsg string `json:"errorMsg"`
		Data     struct {
			Data struct {
				DateFlights           []json.RawMessage `json:"dateFlights"`
				FlightStopCountConfig json.RawMessage   `json:"flightStopCountConfig"`
			} `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		fmt.Printf("response: %d bytes (not JSON: %v); full body in --out\n", len(body), err)
		return
	}
	fmt.Printf("response: success=%v errorMsg=%q dateFlights=%d flightStopCountConfig=%s (full body in --out)\n",
		r.Success, r.ErrorMsg, len(r.Data.Data.DateFlights), r.Data.Data.FlightStopCountConfig)
}

func init() {
	debugCaptureCmd.Flags().DurationVar(&captureWait, "wait", 45*time.Second, "how long to let the page run")
	debugCaptureCmd.Flags().StringVar(&captureOut, "out", "", "directory to write full request/response bodies")
	debugCmd.AddCommand(debugCaptureCmd)
	rootCmd.AddCommand(debugCmd)
}
