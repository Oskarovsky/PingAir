package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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
	// Flex OD: kilka najtańszych na różnych datach
	if q, err := t.pricesLatest(route); err == nil {
		return q, nil
	}
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
	q.Set("limit", "30")
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
			Price       float64 `json:"price"`
			Airline     string  `json:"airline"`
			Link        string  `json:"link"`
			Currency    string  `json:"currency"`
			DepartureAt string  `json:"departure_at"`
			ReturnAt    string  `json:"return_at"`
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
	opts := make([]FareOption, 0, len(parsed.Data))
	for _, o := range parsed.Data {
		dep, ret := dateOnly(o.DepartureAt), dateOnly(o.ReturnAt)
		link := o.Link
		if link != "" && !strings.HasPrefix(link, "http") {
			link = "https://www.aviasales.pl" + link
		}
		if link == "" {
			link = searchLink(route.Origin, route.Destination, dep, ret)
		}
		opts = append(opts, FareOption{
			Price: o.Price, Carrier: o.Airline, DepartDate: dep, ReturnDate: ret, Link: link,
		})
	}
	top := attachSearchLinks(route, topDistinctDepart(opts, 3))
	best := top[0]
	cur := parsed.Data[0].Currency
	qte := t.makeQuote(route, best.Price, best.Carrier, cur, best.Link)
	qte.DepartDate = best.DepartDate
	qte.ReturnDate = best.ReturnDate
	qte.Options = top
	return qte, nil
}

func (t *Travelpayouts) pricesLatest(route config.Route) (Quote, error) {
	q := url.Values{}
	q.Set("origin", strings.ToUpper(route.Origin))
	q.Set("destination", strings.ToUpper(route.Destination))
	q.Set("currency", strings.ToLower(route.Currency))
	q.Set("period_type", "year")
	q.Set("page", "1")
	q.Set("limit", "30")
	q.Set("show_to_affiliates", "true")
	q.Set("sorting", "price")
	q.Set("token", t.cfg.Token)
	if t.cfg.Market != "" {
		q.Set("market", t.cfg.Market)
	}

	body, err := t.get("https://api.travelpayouts.com/v2/prices/latest?" + q.Encode())
	if err != nil {
		return Quote{}, err
	}
	var parsed struct {
		Success bool `json:"success"`
		Data    []struct {
			Value         float64 `json:"value"`
			DepartDate    string  `json:"depart_date"`
			ReturnDate    string  `json:"return_date"`
			NumberOfStops int     `json:"number_of_changes"`
			Airline       string  `json:"airline"`
			Gate          string  `json:"gate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Quote{}, fmt.Errorf("latest json: %w", err)
	}
	if len(parsed.Data) == 0 {
		return Quote{}, fmt.Errorf("empty latest")
	}
	opts := make([]FareOption, 0, len(parsed.Data))
	for _, o := range parsed.Data {
		dep, ret := dateOnly(o.DepartDate), dateOnly(o.ReturnDate)
		opts = append(opts, FareOption{
			Price:      o.Value,
			Carrier:    o.Airline,
			Gate:       o.Gate,
			DepartDate: dep,
			ReturnDate: ret,
			Link:       searchLink(route.Origin, route.Destination, dep, ret),
		})
	}
	top := attachSearchLinks(route, topDistinctDepart(opts, 3))
	best := top[0]
	seller := best.Carrier
	if seller == "" {
		seller = best.Gate
	}
	qte := t.makeQuote(route, best.Price, seller, route.Currency, best.Link)
	qte.DepartDate = best.DepartDate
	qte.ReturnDate = best.ReturnDate
	qte.Options = top
	qte.DeepLink = best.Link
	return qte, nil
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
		for _, v := range parsed.Data {
			raw = v
			ok = true
			break
		}
	}
	if !ok || len(raw) == 0 {
		return Quote{}, fmt.Errorf("brak ofert Travelpayouts dla %s", route.Name)
	}

	var byIdx map[string]struct {
		Price       float64 `json:"price"`
		Airline     string  `json:"airline"`
		DepartureAt string  `json:"departure_at"`
		ReturnAt    string  `json:"return_at"`
	}
	if err := json.Unmarshal(raw, &byIdx); err != nil {
		return Quote{}, fmt.Errorf("brak ofert Travelpayouts dla %s", route.Name)
	}
	opts := make([]FareOption, 0, len(byIdx))
	for _, o := range byIdx {
		dep, ret := dateOnly(o.DepartureAt), dateOnly(o.ReturnAt)
		opts = append(opts, FareOption{
			Price: o.Price, Carrier: o.Airline, DepartDate: dep, ReturnDate: ret,
			Link: searchLink(route.Origin, route.Destination, dep, ret),
		})
	}
	if len(opts) == 0 {
		return Quote{}, fmt.Errorf("brak ofert Travelpayouts dla %s", route.Name)
	}
	top := attachSearchLinks(route, topDistinctDepart(opts, 3))
	best := top[0]
	cur := parsed.Currency
	qte := t.makeQuote(route, best.Price, best.Carrier, cur, best.Link)
	qte.DepartDate = best.DepartDate
	qte.ReturnDate = best.ReturnDate
	qte.Options = top
	qte.DeepLink = best.Link
	return qte, nil
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

func dateOnly(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// searchLink — Aviasales (DDMM) + fallback Google Flights.
func searchLink(origin, dest, depart, ret string) string {
	o, d := strings.ToUpper(origin), strings.ToUpper(dest)
	ddmm := func(iso string) string {
		if len(iso) < 10 {
			return ""
		}
		return iso[8:10] + iso[5:7] // DDMM
	}
	if dep := ddmm(depart); dep != "" {
		if r := ddmm(ret); r != "" {
			return fmt.Sprintf("https://www.aviasales.pl/search/%s%s%s%s", o, dep, d, r)
		}
		return fmt.Sprintf("https://www.aviasales.pl/search/%s%s%s1", o, dep, d)
	}
	return fmt.Sprintf("https://www.google.com/travel/flights?q=Flights%%20%s%%20to%%20%s", o, d)
}

func attachSearchLinks(route config.Route, opts []FareOption) []FareOption {
	for i := range opts {
		if opts[i].Link == "" {
			opts[i].Link = searchLink(route.Origin, route.Destination, opts[i].DepartDate, opts[i].ReturnDate)
		}
	}
	return opts
}

// topDistinctDepart — najtańsze oferty, max N różnych dat wylotu.
func topDistinctDepart(opts []FareOption, n int) []FareOption {
	sort.Slice(opts, func(i, j int) bool {
		if opts[i].Price == opts[j].Price {
			return opts[i].DepartDate < opts[j].DepartDate
		}
		return opts[i].Price < opts[j].Price
	})
	seen := map[string]bool{}
	out := make([]FareOption, 0, n)
	for _, o := range opts {
		key := o.DepartDate
		if key == "" {
			key = fmt.Sprintf("p:%.2f:%s", o.Price, o.Carrier)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, o)
		if len(out) >= n {
			break
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
