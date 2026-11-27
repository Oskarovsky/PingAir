package runner

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Oskarovsky/PingAir/internal/alert"
	"github.com/Oskarovsky/PingAir/internal/config"
	"github.com/Oskarovsky/PingAir/internal/db"
	"github.com/Oskarovsky/PingAir/internal/eventlog"
	"github.com/Oskarovsky/PingAir/internal/provider"
	"github.com/Oskarovsky/PingAir/internal/stats"
)

func PollOnce(cfg *config.Config, database *db.DB, prov provider.Provider, events *eventlog.EventLog) error {
	routes, err := database.ListWatches()
	if err != nil {
		return err
	}
	if len(routes) == 0 {
		routes = cfg.Routes
	}
	for _, route := range routes {
		quote, err := prov.Quote(route)
		if err != nil {
			log.Printf("quote failed %s: %v", route.Name, err)
			events.Push(fmt.Sprintf("error %s: %v", route.Name, err))
			continue
		}
		if err := database.InsertSample(quote); err != nil {
			return err
		}
		_ = database.UpsertRouteFares(quote)

		dateInfo := quote.DepartDate
		if quote.ReturnDate != "" {
			dateInfo += "→" + quote.ReturnDate
		}
		if dateInfo == "" {
			dateInfo = "?"
		}
		events.Push(fmt.Sprintf("sample %s = %.2f %s (%s) %s", quote.RouteName, quote.Price, quote.Currency, quote.Carrier, dateInfo))
		log.Printf("sample %s = %.2f %s (%s) %s", quote.RouteName, quote.Price, quote.Currency, quote.Carrier, dateInfo)

		if err := maybeAlert(cfg, database, events, quote); err != nil {
			return err
		}

		// Suma tam + powrót (±10 dni) — osobna seria próbek + UI.
		if err := pollRoundTrip(cfg, database, events, route, quote); err != nil {
			log.Printf("roundtrip %s: %v", route.Name, err)
			events.Push(fmt.Sprintf("rt skip %s: %v", route.Name, err))
		}
	}
	return nil
}

func pollRoundTrip(cfg *config.Config, database *db.DB, events *eventlog.EventLog, route config.Route, out provider.Quote) error {
	if cfg.Travelpayouts.Token == "" {
		return fmt.Errorf("brak travelpayouts")
	}
	around := out.ReturnDate
	if around == "" {
		around = out.DepartDate
	}
	if around == "" {
		around = time.Now().UTC().Add(7 * 24 * time.Hour).Format("2006-01-02")
	}

	rev := config.Route{
		Name:        route.Destination + " → " + route.Origin,
		Origin:      route.Destination,
		Destination: route.Origin,
		Currency:    route.Currency,
		Adults:      1,
	}
	all, err := provider.ListFares(cfg.Travelpayouts, rev, 40)
	if err != nil {
		return err
	}
	near := provider.NearDate(all, around, 10, 5)
	if len(near) == 0 {
		return fmt.Errorf("brak powrotu ok. %s", around)
	}
	ret := near[0]
	total := out.Price + ret.Price
	outWho := out.Carrier
	if outWho == "" && len(out.Options) > 0 {
		outWho = out.Options[0].Gate
		if outWho == "" {
			outWho = out.Options[0].Carrier
		}
	}
	retWho := ret.Carrier
	if retWho == "" {
		retWho = ret.Gate
	}

	info := db.RoundTripInfo{
		Total: total, OutPrice: out.Price, RetPrice: ret.Price,
		OutDepart: out.DepartDate, RetDepart: ret.DepartDate,
		OutReturn: out.ReturnDate, RetReturn: ret.ReturnDate,
		OutLink: out.DeepLink, RetLink: ret.Link,
		OutWho: outWho, RetWho: retWho, Currency: out.Currency,
	}
	if info.OutLink == "" && len(out.Options) > 0 {
		info.OutLink = out.Options[0].Link
	}
	if err := database.UpsertRoundTrip(route.Key(), info); err != nil {
		return err
	}

	rtQuote := provider.Quote{
		RouteKey:  route.RoundTripKey(),
		RouteName: route.Name + " (tam+powrót)",
		Price:     total,
		Currency:  out.Currency,
		Carrier:   strings.TrimSpace(outWho + "+" + retWho),
		DeepLink:  out.DeepLink,
		FetchedAt: time.Now().UTC(),
		DepartDate: out.DepartDate,
		ReturnDate: ret.DepartDate,
	}
	if err := database.InsertSample(rtQuote); err != nil {
		return err
	}
	events.Push(fmt.Sprintf("rt %s = %.0f (%.0f+%.0f) ok. %s / %s",
		route.Name, total, out.Price, ret.Price, out.DepartDate, ret.DepartDate))
	log.Printf("rt %s = %.0f (out %.0f + ret %.0f)", route.Name, total, out.Price, ret.Price)

	return maybeAlert(cfg, database, events, rtQuote)
}

func maybeAlert(cfg *config.Config, database *db.DB, events *eventlog.EventLog, quote provider.Quote) error {
	prices, err := database.PricesForRoute(quote.RouteKey, cfg.Alert.WindowHours)
	if err != nil {
		return err
	}
	bo := stats.DetectBreakout(quote, prices, cfg.Alert.DropPct, cfg.Alert.MinSamples)
	if bo == nil {
		return nil
	}
	cool, err := inCooldown(database, quote.RouteKey, cfg.Alert.CooldownHours)
	if err != nil {
		return err
	}
	if cool {
		log.Printf("breakout %s but cooldown", quote.RouteName)
		events.Push("cooldown: " + quote.RouteName)
		return nil
	}
	msg := fmt.Sprintf("%s @ %.2f %s (−%.1f%% vs mean %.2f)",
		bo.Quote.RouteName, bo.Quote.Price, bo.Quote.Currency, bo.DropPctVsMean, bo.Stats.Mean)
	if err := database.InsertAlert(quote.RouteKey, quote.Price, bo.Stats.Mean, bo.DropPctVsMean, msg); err != nil {
		return err
	}
	events.Push("ALARM " + msg)
	if err := alert.Notify(cfg.Alert, bo); err != nil {
		log.Printf("alert failed: %v", err)
		events.Push(fmt.Sprintf("alert failed: %v", err))
	}
	return nil
}

func inCooldown(database *db.DB, routeKey string, hours uint64) (bool, error) {
	if hours == 0 {
		return false, nil
	}
	at, err := database.LastAlertAt(routeKey)
	if err != nil || at == nil {
		return false, err
	}
	return time.Now().UTC().Before(at.Add(time.Duration(hours) * time.Hour)), nil
}

func RunLoop(cfg *config.Config, database *db.DB, prov provider.Provider, events *eventlog.EventLog, pollMu *sync.Mutex) error {
	n, _ := database.WatchCount()
	events.Push(fmt.Sprintf("start provider=%s watches=%d interval=%ds", cfg.Provider, n, cfg.PollIntervalSecs))
	log.Printf("PingAir start provider=%s watches=%d interval=%ds", cfg.Provider, n, cfg.PollIntervalSecs)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for {
		events.Push("poll cycle…")
		if pollMu != nil {
			pollMu.Lock()
		}
		err := PollOnce(cfg, database, prov, events)
		if pollMu != nil {
			pollMu.Unlock()
		}
		if err != nil {
			log.Printf("poll cycle failed: %v", err)
			events.Push(fmt.Sprintf("poll cycle failed: %v", err))
		} else {
			events.Push("poll cycle ok")
		}
		select {
		case <-sig:
			events.Push("stop")
			log.Println("stop")
			return nil
		case <-time.After(time.Duration(cfg.PollIntervalSecs) * time.Second):
		}
	}
}
