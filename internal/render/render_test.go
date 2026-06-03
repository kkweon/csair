package render

import (
	"testing"

	"github.com/kkweon/csair/internal/domain"
)

func TestRoutingAndVias(t *testing.T) {
	nonstop := domain.Itinerary{Segments: []domain.Segment{
		{Carrier: "CZ", FlightNo: "658", Origin: "SFO", Destination: "CAN"},
	}}
	if got := routing(nonstop); got != "SFO–CAN" {
		t.Errorf("nonstop routing = %q, want SFO–CAN", got)
	}
	if v := nonstop.Vias(); v != nil {
		t.Errorf("nonstop vias = %v, want nil", v)
	}

	// Single-segment through-flight: the Wuhan stop is an intra-segment via.
	through := domain.Itinerary{Segments: []domain.Segment{
		{Carrier: "CZ", FlightNo: "660", Origin: "SFO", Destination: "CAN", Vias: []string{"WUH"}},
	}}
	if got := routing(through); got != "SFO–WUH–CAN" {
		t.Errorf("through routing = %q, want SFO–WUH–CAN", got)
	}
	if v := through.Vias(); len(v) != 1 || v[0] != "WUH" {
		t.Errorf("through vias = %v, want [WUH]", v)
	}

	// Two-segment connection: Wuhan is the inter-segment connection point.
	conn := domain.Itinerary{Segments: []domain.Segment{
		{Carrier: "CZ", FlightNo: "660", Origin: "SFO", Destination: "WUH"},
		{Carrier: "CZ", FlightNo: "8004", Origin: "WUH", Destination: "CAN"},
	}}
	if got := routing(conn); got != "SFO–WUH–CAN" {
		t.Errorf("connection routing = %q, want SFO–WUH–CAN", got)
	}
	if v := conn.Vias(); len(v) != 1 || v[0] != "WUH" {
		t.Errorf("connection vias = %v, want [WUH]", v)
	}
}
