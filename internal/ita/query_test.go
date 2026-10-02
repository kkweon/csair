package ita

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/kkweon/csair/internal/airport"
	"github.com/kkweon/csair/internal/domain"
)

// fakeHTTP answers the session-create call and then each queryInterFlight call
// in order, recording the stop flag each query sent.
type fakeHTTP struct {
	grids [][]byte
	flags []bool
}

func (f *fakeHTTP) Get(context.Context, string) ([]byte, error) { return nil, nil }

func (f *fakeHTTP) PostForm(context.Context, string, url.Values) ([]byte, error) {
	return []byte(`{"data":{"execution":"abc123"}}`), nil
}

func (f *fakeHTTP) PostJSON(_ context.Context, _ string, body any) ([]byte, error) {
	f.flags = append(f.flags, body.(map[string]any)["useRuleConfigMaxStopCountIfParamTwo"].(bool))
	g := f.grids[0]
	f.grids = f.grids[1:]
	return g, nil
}

const (
	gridNoNonstop = `{"success":false,"errorMsg":"ITA查询异常，反馈信息：result.data为null",
	  "data":{"data":{"flightStopCountConfig":{"ruleConfigMaxStopCount":"2","currentMaxStopCount":"0"}}}}`
	gridNonstop = `{"success":true,"data":{"data":{
	  "flightStopCountConfig":{"ruleConfigMaxStopCount":"2","currentMaxStopCount":"0"},
	  "dateFlights":[{"stopNumber":0,"segments":[{"flightNo":"657","carrier":"CZ","depPort":"CAN","arrPort":"SFO"}],
	    "prices":[{"cabins":[{"name":"C","type":"Business","bookingClassAvails":"8"}]}]}]}}}`
	gridConnections = `{"success":true,"data":{"data":{
	  "flightStopCountConfig":{"ruleConfigMaxStopCount":"2","currentMaxStopCount":"2"},
	  "dateFlights":[{"stopNumber":1,"segments":[
	      {"flightNo":"659","carrier":"CZ","depPort":"CAN","arrPort":"WUH"},
	      {"flightNo":"659","carrier":"CZ","depPort":"WUH","arrPort":"SFO"}],
	    "prices":[{"cabins":[{"name":"C","type":"Business","bookingClassAvails":"9"}]}]}]}}}`
	gridAllStops = `{"success":true,"data":{"data":{
	  "flightStopCountConfig":{"ruleConfigMaxStopCount":"2","currentMaxStopCount":"2"},
	  "dateFlights":[{"stopNumber":0,"segments":[{"flightNo":"658","carrier":"CZ","depPort":"SFO","arrPort":"CAN"}],
	    "prices":[{"cabins":[{"name":"C","type":"Business","bookingClassAvails":"9"}]}]}]}}}`
)

func search(t *testing.T, h *fakeHTTP, from, to string) (*domain.SearchResult, error) {
	t.Helper()
	q := NewQueryService(h, NewParser(), airport.NewStatic())
	return q.Search(context.Background(), domain.SearchRequest{
		Origin: from, Destination: to, Date: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		Pax: domain.Pax{Adults: 1},
	})
}

// A day with only a 1-stop flight (CZ659 on 10/05): the first query fails with
// result.data为null, the connections pass carries the itinerary.
func TestSearch_ConnectionsPassWhenNoNonstop(t *testing.T) {
	h := &fakeHTTP{grids: [][]byte{[]byte(gridNoNonstop), []byte(gridConnections)}}
	res, err := search(t, h, "CAN", "SFO")
	if err != nil {
		t.Fatal(err)
	}
	if len(h.flags) != 2 || h.flags[0] || !h.flags[1] {
		t.Fatalf("stop flags sent = %v, want [false true]", h.flags)
	}
	if len(res.Itineraries) != 1 || res.Itineraries[0].Stops != 1 {
		t.Fatalf("itineraries = %+v, want the 1-stop", res.Itineraries)
	}
}

// Nonstops from the first pass come first, connections are appended.
func TestSearch_MergesBothPasses(t *testing.T) {
	h := &fakeHTTP{grids: [][]byte{[]byte(gridNonstop), []byte(gridConnections)}}
	res, err := search(t, h, "CAN", "SFO")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Itineraries) != 2 || res.Itineraries[0].Stops != 0 || res.Itineraries[1].Stops != 1 {
		t.Fatalf("itineraries = %+v, want nonstop then 1-stop", res.Itineraries)
	}
}

// When the engine already applied the 2-stop cap there is no second query.
func TestSearch_SinglePassWhenStopsAlreadyAllowed(t *testing.T) {
	h := &fakeHTTP{grids: [][]byte{[]byte(gridAllStops)}}
	if _, err := search(t, h, "SFO", "CAN"); err != nil {
		t.Fatal(err)
	}
	if len(h.flags) != 1 {
		t.Fatalf("queries = %d, want 1", len(h.flags))
	}
}

func TestNeedsStopQuery(t *testing.T) {
	cases := map[string]bool{
		gridNoNonstop:               true,
		gridNonstop:                 true,
		gridConnections:             false,
		`{"success":true}`:          false, // older responses carry no config
		`<html>interstitial</html>`: false,
	}
	for grid, want := range cases {
		if got := NewParser().NeedsStopQuery([]byte(grid)); got != want {
			t.Errorf("NeedsStopQuery(%.40q) = %v, want %v", grid, got, want)
		}
	}
}
