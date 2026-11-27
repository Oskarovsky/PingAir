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

type Travelpayouts struct {
	cfg    config.TravelpayoutsConfig
	client *http.Client
}

func NewTravelpayouts(cfg config.TravelpayoutsConfig) *Travelpayouts {
	return &Travelpayouts{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *Travelpayouts) Quote(route config.Route) (Quote, error) {
	if route.DepartDate != "" {
		if q, err := t.pricesForDates(route); err == nil {
			return q, nil
		}
	}
	// Bez daty / pusty cache daty → najtańszy dostępny na trasie OD.
	return t.pricesCheap(route)
}

func (t *Travelpayouts) pricesForDates(route config.Route) (Quote, error) {
	oneWay := route.ReturnDate == ""
	q := url.Values{}
	q.Set("origin", strings.ToUpper(route.Origin))
	q.Set("destination", strings.ToUpper(route.Destination))
	q.Set("departure_at", route.DepartDate)
	q.Set("currency", strings.ToLower(route.Currency))
	q.Set("sorting", "price")
	q.Set("limit", "10")
	q.Set("page", "1")
	q.Set("direct", "false")
	q.Set("unique", "false")
	q.Set("token", t.cfg.Token)
	if oneWay {
		q.Set("one_way", "true")
	} else {
		q.Set("one_way", "false")
		q.Set("return_at", route.ReturnDate)
	}
	if t.cfg.Market != "" {
		q.Set("market", t.cfg.Market)
	}

	body, err := t.get("https://api.travelpayouts.com/aviasales/v3/prices_for_dates?" + q.Encode())
	if err != nil {
		return Quote{}, err
	}
	var parsed struct {
		Success bool `json:"success"`
		Error   any  `json:"error"`
		Data    []struct {
			Price    float64 `json:"price"`
			Airline  string  `json:"airline"`
			Link     string  `json:"link"`
			Currency string  `json:"currency"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Quote{}, fmt.Errorf("travelpayouts json: %w", err)
	}
	if !parsed.Success {
		return Quote{}, fmt.Errorf("travelpayouts error: %v", parsed.Error)
	}
	if len(parsed.Data) == 0 {
		return Quote{}, fmt.Errorf("empty prices_for_dates")
	}
	best := parsed.Data[0]
	for _, o := range parsed.Data[1:] {
		if o.Price < best.Price {
			best = o
		}
	}
	return t.makeQuote(route, best.Price, best.Airline, best.Currency, best.Link), nil
}

func (t *Travelpayouts) pricesCheap(route config.Route) (Quote, error) {
	q := url.Values{}
	q.Set("origin", strings.ToUpper(route.Origin))
	q.Set("destination", strings.ToUpper(route.Destination))
	q.Set("currency", strings.ToLower(route.Currency))
	q.Set("token", t.cfg.Token)

	body, err := t.get("https://api.travelpayouts.com/v1/prices/cheap?" + q.Encode())
	if err != nil {
		return Quote{}, err
	}
	var parsed struct {
		Success  bool                       `json:"success"`
		Currency string                     `json:"currency"`
		Data     map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Quote{}, fmt.Errorf("cheap json: %w", err)
	}
	if !parsed.Success {
		return Quote{}, fmt.Errorf("travelpayouts cheap failed")
	}
	dest := strings.ToUpper(route.Destination)
	raw, ok := parsed.Data[dest]
	if !ok {
		// sometimes nested differently — take first dest
		for _, v := range parsed.Data {
			raw = v
			ok = true
			break
		}
	}
	if !ok || len(raw) == 0 {
		return Quote{}, fmt.Errorf("brak ofert Travelpayouts dla %s", route.Name)
	}

	// data.BCN is map "0"/"1"/… → offer, or sometimes a single object
	var byIdx map[string]struct {
		Price       float64 `json:"price"`
		Airline     string  `json:"airline"`
		DepartureAt string  `json:"departure_at"`
	}
	if err := json.Unmarshal(raw, &byIdx); err != nil {
		return Quote{}, fmt.Errorf("brak ofert Travelpayouts dla %s", route.Name)
	}
	var bestPrice float64
	var bestAir string
	found := false
	for _, o := range byIdx {
		if !found || o.Price < bestPrice {
			bestPrice, bestAir, found = o.Price, o.Airline, true
		}
	}
	if !found {
		return Quote{}, fmt.Errorf("brak ofert Travelpayouts dla %s", route.Name)
	}
	cur := parsed.Currency
	if cur == "" {
		cur = strings.ToUpper(route.Currency)
	}
	return t.makeQuote(route, bestPrice, bestAir, cur, ""), nil
}

func (t *Travelpayouts) makeQuote(route config.Route, price float64, airline, currency, link string) Quote {
	deep := link
	if link != "" && !strings.HasPrefix(link, "http") {
		deep = "https://www.aviasales.pl" + link
	} else if link == "" {
		deep = fmt.Sprintf("https://www.aviasales.pl/search/%s%s%s",
			strings.ToUpper(route.Origin),
			strings.ReplaceAll(route.DepartDate, "-", ""),
			strings.ToUpper(route.Destination),
		)
	}
	if currency == "" {
		currency = strings.ToUpper(route.Currency)
	} else {
		currency = strings.ToUpper(currency)
	}
	return Quote{
		RouteKey:  route.Key(),
		RouteName: route.Name,
		Price:     price,
		Currency:  currency,
		Carrier:   airline,
		DeepLink:  deep,
		FetchedAt: time.Now().UTC(),
	}
}

func (t *Travelpayouts) get(rawURL string) ([]byte, error) {
	resp, err := t.client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("travelpayouts: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("travelpayouts HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
