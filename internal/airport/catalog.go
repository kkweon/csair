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

// Zone returns the IANA timezone for an airport (e.g. "America/Los_Angeles"),
// used to reason about local departure dates.
func (s Static) Zone(iata string) (string, error) {
	z, ok := zones[strings.ToUpper(strings.TrimSpace(iata))]
	if !ok {
		return "", fmt.Errorf("no timezone for airport %q: add it to the catalog", iata)
	}
	return z, nil
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

// zones maps an IATA airport code to its IANA timezone. Mainland China uses a
// single legal time (Asia/Shanghai) nationwide, including Ürümqi.
var zones = map[string]string{
	// China (mainland) — all Beijing time
	"CAN": "Asia/Shanghai", "PEK": "Asia/Shanghai", "PKX": "Asia/Shanghai",
	"PVG": "Asia/Shanghai", "SHA": "Asia/Shanghai", "SZX": "Asia/Shanghai",
	"WUH": "Asia/Shanghai", "CTU": "Asia/Shanghai", "TFU": "Asia/Shanghai",
	"CKG": "Asia/Shanghai", "XIY": "Asia/Shanghai", "KMG": "Asia/Shanghai",
	"HGH": "Asia/Shanghai", "NKG": "Asia/Shanghai", "CSX": "Asia/Shanghai",
	"SYX": "Asia/Shanghai", "HAK": "Asia/Shanghai", "URC": "Asia/Shanghai",
	"DLC": "Asia/Shanghai", "SHE": "Asia/Shanghai", "XMN": "Asia/Shanghai",
	"TAO": "Asia/Shanghai", "ZUH": "Asia/Shanghai",
	// Hong Kong / Macau / Taiwan
	"HKG": "Asia/Hong_Kong", "MFM": "Asia/Macau", "TPE": "Asia/Taipei", "KHH": "Asia/Taipei",
	// United States
	"SFO": "America/Los_Angeles", "LAX": "America/Los_Angeles", "SEA": "America/Los_Angeles",
	"JFK": "America/New_York", "EWR": "America/New_York", "IAD": "America/New_York",
	"BOS": "America/New_York", "ORD": "America/Chicago",
	// Canada
	"YVR": "America/Vancouver", "YYZ": "America/Toronto",
	// Asia-Pacific / others CZ commonly serves
	"NRT": "Asia/Tokyo", "HND": "Asia/Tokyo", "KIX": "Asia/Tokyo",
	"ICN": "Asia/Seoul", "GMP": "Asia/Seoul",
	"SIN": "Asia/Singapore", "BKK": "Asia/Bangkok", "KUL": "Asia/Kuala_Lumpur",
	"CGK": "Asia/Jakarta", "MNL": "Asia/Manila", "SGN": "Asia/Ho_Chi_Minh", "HAN": "Asia/Ho_Chi_Minh",
	"SYD": "Australia/Sydney", "MEL": "Australia/Sydney", "AKL": "Pacific/Auckland",
	"LHR": "Europe/London", "CDG": "Europe/Paris", "FRA": "Europe/Berlin",
	"AMS": "Europe/Amsterdam", "IST": "Europe/Istanbul",
}
