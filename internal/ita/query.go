package ita

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/kkweon/csair/internal/airport"
	"github.com/kkweon/csair/internal/domain"
	"github.com/kkweon/csair/internal/transport"
)

const (
	baseURL  = "https://b2c.csair.com"
	appURL   = baseURL + "/ita/intl/app"
	queryURL = baseURL + "/ita/rest/intl/main/aoa/inter/queryInterFlight"
)

// QueryService runs the full app→execution→query flow and returns domain models.
type QueryService interface {
	Search(ctx context.Context, req domain.SearchRequest) (*domain.SearchResult, error)
}

type queryService struct {
	http  transport.HTTPRequester
	parse ParseService
	cat   airport.Catalog
}

// NewQueryService wires the transport, parser, and airport catalog.
func NewQueryService(h transport.HTTPRequester, p ParseService, c airport.Catalog) QueryService {
	return &queryService{http: h, parse: p, cat: c}
}

func (q *queryService) Search(ctx context.Context, req domain.SearchRequest) (*domain.SearchResult, error) {
	form, err := q.appForm(req)
	if err != nil {
		return nil, err
	}
	html, err := q.http.PostForm(ctx, appURL, form)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	exec, err := q.parse.Execution(html)
	if err != nil {
		return nil, err
	}
	grid, err := q.http.PostJSON(ctx, queryURL, queryBody(req, exec, 1))
	if err != nil {
		return nil, fmt.Errorf("query flights: %w", err)
	}
	its, err := q.parse.Flights(grid)
	if err != nil {
		return nil, err
	}
	return &domain.SearchResult{Request: req, Itineraries: its}, nil
}

// appForm builds the /ita/intl/app body. Only codes + area (country) are
// required — names are optional (verified empirically).
func (q *queryService) appForm(req domain.SearchRequest) (url.Values, error) {
	depArea, err := q.cat.Country(req.Origin)
	if err != nil {
		return nil, err
	}
	arrArea, err := q.cat.Country(req.Destination)
	if err != nil {
		return nil, err
	}
	return url.Values{
		"language":  {"zh"},
		"country":   {"zh"},
		"m":         {"0"},
		"flexible":  {"1"},
		"adt":       {strconv.Itoa(max(req.Pax.Adults, 1))},
		"cnn":       {strconv.Itoa(req.Pax.Children)},
		"inf":       {strconv.Itoa(req.Pax.Infants)},
		"dep[]":     {req.Origin},
		"depArea[]": {depArea},
		"arr[]":     {req.Destination},
		"arrArea[]": {arrArea},
		"date[]":    {req.Date.Format("2006-01-02")},
	}, nil
}

// queryBody builds the queryInterFlight JSON body.
func queryBody(req domain.SearchRequest, execution string, page int) any {
	return map[string]any{
		"adults":       max(req.Pax.Adults, 1),
		"children":     req.Pax.Children,
		"infantsInLap": req.Pax.Infants,
		"slices": []map[string]any{{
			"date":        req.Date.Format("2006-01-02"),
			"origin":      req.Origin,
			"destination": req.Destination,
			"depCityFlag": true,
			"arrCityFlag": true,
		}},
		"sliceIndex": 0,
		"lang":       "zh",
		"flightType": "singlePass",
		"execution":  execution,
		"page":       page,
	}
}
