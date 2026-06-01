// Package airport resolves IATA codes to the ISO country code that
// /ita/intl/app requires as the search "area". Only the country is needed
// (city names are optional, verified empirically).
package airport

import (
	"fmt"
	"strings"
)

// Catalog maps an IATA airport code to its country.
type Catalog interface {
	Country(iata string) (string, error)
}

// Static is an in-memory catalog. The seed set below covers common CZ
// origins/destinations; TODO: replace with a full embedded dataset
// (go:embed of an OpenFlights-derived IATA→country table) + a generator.
type Static struct{ m map[string]string }

// NewStatic returns the built-in seed catalog.
func NewStatic() Static { return Static{m: seed} }

func (s Static) Country(iata string) (string, error) {
	code, ok := s.m[strings.ToUpper(strings.TrimSpace(iata))]
	if !ok {
		return "", fmt.Errorf("unknown airport %q: add it to the catalog", iata)
	}
	return code, nil
}

var seed = map[string]string{
	// China (mainland)
	"CAN": "CN", "PEK": "CN", "PKX": "CN", "PVG": "CN", "SHA": "CN",
	"SZX": "CN", "WUH": "CN", "CTU": "CN", "TFU": "CN", "CKG": "CN",
	"XIY": "CN", "KMG": "CN", "HGH": "CN", "NKG": "CN", "CSX": "CN",
	"SYX": "CN", "HAK": "CN", "URC": "CN", "DLC": "CN", "SHE": "CN",
	"XMN": "CN", "TAO": "CN", "ZUH": "CN",
	// Hong Kong / Macau / Taiwan
	"HKG": "HK", "MFM": "MO", "TPE": "TW", "KHH": "TW",
	// United States
	"SFO": "US", "LAX": "US", "JFK": "US", "EWR": "US", "ORD": "US",
	"SEA": "US", "IAD": "US", "BOS": "US",
	// Canada
	"YVR": "CA", "YYZ": "CA",
	// Asia-Pacific / others CZ commonly serves
	"NRT": "JP", "HND": "JP", "KIX": "JP", "ICN": "KR", "GMP": "KR",
	"SIN": "SG", "BKK": "TH", "KUL": "MY", "CGK": "ID", "MNL": "PH",
	"SGN": "VN", "HAN": "VN", "SYD": "AU", "MEL": "AU", "AKL": "NZ",
	"LHR": "GB", "CDG": "FR", "FRA": "DE", "AMS": "NL", "IST": "TR",
}
