package discover

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

// Popular hubs for country → city discovery (Travelpayouts nie bierze origin=PL).
var CountryHubs = map[string][]string{
	"PL": {"WAW", "KRK", "GDN", "WRO", "KTW", "POZ", "RZE", "WMI", "LUZ", "BZG", "SZZ", "LCJ"},
	"DE": {"BER", "MUC", "FRA", "HAM", "CGN", "DUS", "STR"},
	"ES": {"MAD", "BCN", "AGP", "PMI", "ALC", "VLC"},
	"IT": {"ROM", "MIL", "NAP", "VCE", "BLQ", "PSA"},
	"FR": {"PAR", "NCE", "LYS", "MRS", "TLS"},
	"GB": {"LON", "MAN", "EDI", "BHX", "GLA"},
}

type Direction struct {
	Origin      string
	Destination string
	Price       float64
	Airline     string
	DepartDate  string
	ReturnDate  string
}

func PopularFromOrigin(token, origin, currency, market string, limit int) ([]Direction, error) {
	if limit <= 0 {
		limit = 30
	}
	origin = strings.ToUpper(strings.TrimSpace(origin))
	currency = strings.ToLower(currency)
	if currency == "" {
		currency = "pln"
	}

	// Prefer v3 unique routes; fallback city-directions.
	dirs, err := pricesForDatesUnique(token, origin, currency, market, limit)
	if err == nil && len(dirs) > 0 {
		return dirs, nil
	}
	return cityDirections(token, origin, currency, market, limit)
}

func PopularFromCountry(token, country, currency, market string, perHub, maxTotal int) ([]Direction, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	hubs, ok := CountryHubs[country]
	if !ok {
		return nil, fmt.Errorf("brak hubów dla kraju %s (obsługiwane: PL, DE, ES, IT, FR, GB)", country)
	}
	if perHub <= 0 {
		perHub = 15
	}
	if maxTotal <= 0 {
		maxTotal = 80
	}
	seen := map[string]bool{}
	var out []Direction
	for _, hub := range hubs {
		dirs, err := PopularFromOrigin(token, hub, currency, market, perHub)
		if err != nil {
			continue
		}
		for _, d := range dirs {
			key := d.Origin + "|" + d.Destination
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, d)
			if len(out) >= maxTotal {
				sort.Slice(out, func(i, j int) bool { return out[i].Price < out[j].Price })
				return out, nil
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Price < out[j].Price })
	return out, nil
}

func ToRoute(d Direction, currency string) config.Route {
	// Puste daty = obserwuj OD (najtańszy z cache), nie konkretny wyjazd.
	return config.Route{
		Name:        fmt.Sprintf("%s → %s", d.Origin, d.Destination),
		Origin:      d.Origin,
		Destination: d.Destination,
		DepartDate:  "",
		ReturnDate:  "",
		Adults:      1,
		Currency:    strings.ToUpper(currency),
	}
}

func pricesForDatesUnique(token, origin, currency, market string, limit int) ([]Direction, error) {
	q := url.Values{}
	q.Set("origin", origin)
	q.Set("currency", currency)
	q.Set("sorting", "route")
	q.Set("unique", "true")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("one_way", "true")
	q.Set("token", token)
	if market != "" {
		q.Set("market", market)
	}
	body, err := get("https://api.travelpayouts.com/aviasales/v3/prices_for_dates?" + q.Encode())
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Success bool `json:"success"`
		Data    []struct {
			Origin      string  `json:"origin"`
			Destination string  `json:"destination"`
			Price       float64 `json:"price"`
			Airline     string  `json:"airline"`
			DepartureAt string  `json:"departure_at"`
			ReturnAt    string  `json:"return_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || !parsed.Success {
		return nil, fmt.Errorf("prices_for_dates unique failed")
	}
	out := make([]Direction, 0, len(parsed.Data))
	for _, row := range parsed.Data {
		o := row.Origin
		if o == "" {
			o = origin
		}
		out = append(out, Direction{
			Origin: o, Destination: row.Destination, Price: row.Price,
			Airline: row.Airline, DepartDate: row.DepartureAt, ReturnDate: row.ReturnAt,
		})
	}
	return out, nil
}

func cityDirections(token, origin, currency, market string, limit int) ([]Direction, error) {
	q := url.Values{}
	q.Set("origin", origin)
	q.Set("currency", currency)
	q.Set("token", token)
	if market != "" {
		q.Set("market", market)
	}
	body, err := get("https://api.travelpayouts.com/v1/city-directions?" + q.Encode())
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Success bool `json:"success"`
		Data    map[string]struct {
			Origin      string  `json:"origin"`
			Destination string  `json:"destination"`
			Price       float64 `json:"price"`
			Airline     string  `json:"airline"`
			DepartureAt string  `json:"departure_at"`
			ReturnAt    string  `json:"return_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || !parsed.Success {
		return nil, fmt.Errorf("city-directions failed")
	}
	out := make([]Direction, 0, len(parsed.Data))
	for dest, row := range parsed.Data {
		o := row.Origin
		if o == "" {
			o = origin
		}
		d := row.Destination
		if d == "" {
			d = dest
		}
		out = append(out, Direction{
			Origin: o, Destination: d, Price: row.Price,
			Airline: row.Airline, DepartDate: row.DepartureAt, ReturnDate: row.ReturnAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Price < out[j].Price })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func get(rawURL string) ([]byte, error) {
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}
