package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Oskarovsky/PingAir/internal/config"
)

type Serpapi struct {
	cfg    config.SerpapiConfig
	client *http.Client
}

func NewSerpapi(cfg config.SerpapiConfig) *Serpapi {
	return &Serpapi{
		cfg:    cfg,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (s *Serpapi) Quote(route config.Route) (Quote, error) {
	oneWay := route.ReturnDate == ""
	q := url.Values{}
	q.Set("engine", "google_flights")
	q.Set("api_key", s.cfg.APIKey)
	q.Set("departure_id", strings.ToUpper(route.Origin))
	q.Set("arrival_id", strings.ToUpper(route.Destination))
	q.Set("outbound_date", route.DepartDate)
	q.Set("currency", strings.ToUpper(route.Currency))
	q.Set("hl", s.cfg.HL)
	q.Set("gl", s.cfg.GL)
	q.Set("sort_by", "2")
	q.Set("adults", fmt.Sprintf("%d", route.Adults))
	if oneWay {
		q.Set("type", "2")
	} else {
		q.Set("type", "1")
		q.Set("return_date", route.ReturnDate)
	}

	reqURL := "https://serpapi.com/search.json?" + q.Encode()
	resp, err := s.client.Get(reqURL)
	if err != nil {
		return Quote{}, fmt.Errorf("serpapi: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Quote{}, err
	}
	if resp.StatusCode != 200 {
		return Quote{}, fmt.Errorf("serpapi HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var parsed struct {
		Error          string `json:"error"`
		PriceInsights  *struct {
			LowestPrice float64 `json:"lowest_price"`
		} `json:"price_insights"`
		BestFlights  []flightOpt `json:"best_flights"`
		OtherFlights []flightOpt `json:"other_flights"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Quote{}, fmt.Errorf("serpapi json: %w", err)
	}
	if parsed.Error != "" {
		return Quote{}, fmt.Errorf("serpapi: %s", parsed.Error)
	}

	price, carrier, ok := pickSerpBest(parsed.PriceInsights, parsed.BestFlights, parsed.OtherFlights)
	if !ok {
		return Quote{}, fmt.Errorf("brak ofert Google Flights dla %s", route.Name)
	}
	deep := fmt.Sprintf(
		"https://www.google.com/travel/flights?hl=%s&curr=%s&q=Flights%%20%s%%20to%%20%s%%20on%%20%s",
		s.cfg.HL, strings.ToUpper(route.Currency),
		strings.ToUpper(route.Origin), strings.ToUpper(route.Destination), route.DepartDate,
	)
	return Quote{
		RouteKey:  route.Key(),
		RouteName: route.Name,
		Price:     price,
		Currency:  strings.ToUpper(route.Currency),
		Carrier:   carrier,
		DeepLink:  deep,
		FetchedAt: time.Now().UTC(),
	}, nil
}

type flightOpt struct {
	Price   any `json:"price"`
	Flights []struct {
		Airline string `json:"airline"`
	} `json:"flights"`
}

func pickSerpBest(insights *struct {
	LowestPrice float64 `json:"lowest_price"`
}, best, other []flightOpt) (float64, string, bool) {
	carrier := firstAirline(best, other)
	if insights != nil && insights.LowestPrice > 0 {
		return insights.LowestPrice, carrier, true
	}
	var found bool
	var min float64
	var c string
	for _, list := range [][]flightOpt{best, other} {
		for _, item := range list {
			p, ok := asFloat(item.Price)
			if !ok {
				continue
			}
			air := ""
			if len(item.Flights) > 0 {
				air = item.Flights[0].Airline
			}
			if !found || p < min {
				min, c, found = p, air, true
			}
		}
	}
	return min, c, found
}

func firstAirline(best, other []flightOpt) string {
	for _, list := range [][]flightOpt{best, other} {
		for _, item := range list {
			if len(item.Flights) > 0 && item.Flights[0].Airline != "" {
				return item.Flights[0].Airline
			}
		}
	}
	return ""
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case string:
		var f float64
		_, err := fmt.Sscanf(strings.ReplaceAll(t, ",", ""), "%f", &f)
		return f, err == nil
	default:
		return 0, false
	}
}
