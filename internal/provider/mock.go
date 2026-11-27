package provider

import (
	"math/rand"
	"time"

	"github.com/Oskarovsky/PingAir/internal/config"
)

type Mock struct{}

func NewMock() *Mock { return &Mock{} }

func (m *Mock) Quote(route config.Route) (Quote, error) {
	base := 420.0 + rand.Float64()*180
	if rand.Float64() < 0.08 {
		base *= 0.75 // sztuczny dump
	}
	return Quote{
		RouteKey:  route.Key(),
		RouteName: route.Name,
		Price:     base,
		Currency:  route.Currency,
		Carrier:   "XX",
		DeepLink:  "https://www.google.com/travel/flights",
		FetchedAt: time.Now().UTC(),
	}, nil
}
