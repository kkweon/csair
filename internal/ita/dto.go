package ita

// Transport DTOs: the wire shape of b2c.csair.com/ita responses. Unexported on
// purpose — only ParseService touches them; the rest of the app uses domain.*.

type queryResponse struct {
	Success bool      `json:"success"`
	ErrMsg  string    `json:"errMsg"`
	Data    queryData `json:"data"`
}

type queryData struct {
	Data queryInner `json:"data"`
}

type queryInner struct {
	DateFlights []dtoDateFlight `json:"dateFlights"`
}

type dtoDateFlight struct {
	StopNumber int          `json:"stopNumber"`
	FlyTime    string       `json:"flyTime"`  // e.g. "14h45m"
	Duration   int          `json:"duration"` // minutes
	OverDays   int          `json:"overDays"`
	Origin     string       `json:"origin"`
	Destinatn  string       `json:"destination"`
	Segments   []dtoSegment `json:"segments"`
	Prices     []dtoPrice   `json:"prices"`
}

type dtoSegment struct {
	FlightNo    string    `json:"flightNo"`
	Carrier     string    `json:"carrier"`
	DepPort     string    `json:"depPort"`
	ArrPort     string    `json:"arrPort"`
	DepTime     string    `json:"depTime"` // "00:35"
	ArrTime     string    `json:"arrTime"`
	DepDate     string    `json:"depDate"` // "2026-06-14"
	ArrDate     string    `json:"arrDate"`
	Plane       string    `json:"plane"`
	DepTerm     string    `json:"depTerm"`
	ArrTerm     string    `json:"arrTerm"`
	CodeShare   bool      `json:"codeShare"`
	Legs        []dtoLeg  `json:"legs"`
}

type dtoLeg struct {
	DepTimeZone string `json:"depTimeZone"` // "2026-06-14T00:35-07:00"
	ArrTimeZone string `json:"arrTimeZone"` // "2026-06-15T06:20+08:00"
}

type dtoPrice struct {
	DisplayPrice    float64    `json:"displayPrice"`
	DisplayCurrency string     `json:"displayCurrency"`
	Cabins          []dtoCabin `json:"cabins"`
}

type dtoCabin struct {
	Name              string `json:"name"`              // RBD letter, "I"
	Type              string `json:"type"`              // "Business", "Economy", ...
	BookingClassAvail string `json:"bookingClassAvails"` // "6", "9"
	BrandCode         string `json:"brandCode"`         // "JFFA"
}
