package runner

import (
	"fmt"
	"log"
	"os"
	"os/signal"
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
	for _, route := range cfg.Routes {
		quote, err := prov.Quote(route)
		if err != nil {
			log.Printf("quote failed %s: %v", route.Name, err)
			events.Push(fmt.Sprintf("error %s: %v", route.Name, err))
			continue
		}
		if err := database.InsertSample(quote); err != nil {
			return err
		}
		events.Push(fmt.Sprintf("sample %s = %.2f %s (%s)", quote.RouteName, quote.Price, quote.Currency, quote.Carrier))
		log.Printf("sample %s = %.2f %s (%s)", quote.RouteName, quote.Price, quote.Currency, quote.Carrier)

		prices, err := database.PricesForRoute(quote.RouteKey, cfg.Alert.WindowHours)
		if err != nil {
			return err
		}
		bo := stats.DetectBreakout(quote, prices, cfg.Alert.DropPct, cfg.Alert.MinSamples)
		if bo == nil {
			continue
		}
		cool, err := inCooldown(database, quote.RouteKey, cfg.Alert.CooldownHours)
		if err != nil {
			return err
		}
		if cool {
			log.Printf("breakout %s but cooldown", quote.RouteName)
			events.Push("cooldown: " + quote.RouteName)
			continue
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

func RunLoop(cfg *config.Config, database *db.DB, prov provider.Provider, events *eventlog.EventLog) error {
	events.Push(fmt.Sprintf("start provider=%s routes=%d interval=%ds", cfg.Provider, len(cfg.Routes), cfg.PollIntervalSecs))
	log.Printf("PingAir start provider=%s routes=%d interval=%ds", cfg.Provider, len(cfg.Routes), cfg.PollIntervalSecs)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

	for {
		events.Push("poll cycle…")
		if err := PollOnce(cfg, database, prov, events); err != nil {
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
