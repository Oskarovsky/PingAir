package provider

import (
	"fmt"
	"time"

	"github.com/Oskarovsky/PingAir/internal/config"
)

// FareOption — jedna oferta (cena + daty). UI trzyma do 3 różnych dat wylotu.
type FareOption struct {
	Price      float64
	Carrier    string
	DepartDate string // YYYY-MM-DD
	ReturnDate string // YYYY-MM-DD or ""
}

type Quote struct {
	RouteKey  string
	RouteName string
	Price     float64
	Currency  string
	Carrier   string
	DeepLink  string
	FetchedAt time.Time
	// Daty najtańszej oferty
	DepartDate string
	ReturnDate string
	// Do 3 najtańszych na różnych datach wylotu
	Options []FareOption
}

type Provider interface {
	Quote(route config.Route) (Quote, error)
}

func New(cfg *config.Config) (Provider, error) {
	switch cfg.Provider {
	case "mock":
		return NewMock(), nil
	case "travelpayouts", "aviasales":
		return NewTravelpayouts(cfg.Travelpayouts), nil
	case "serpapi", "google_flights", "google":
		return NewSerpapi(cfg.Serpapi), nil
	case "amadeus":
		return nil, fmt.Errorf("amadeus Self-Service martwy od 2026-07 — użyj travelpayouts lub serpapi")
	default:
		return nil, fmt.Errorf("nieznany provider: %s", cfg.Provider)
	}
}
