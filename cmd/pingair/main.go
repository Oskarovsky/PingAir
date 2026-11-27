package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Oskarovsky/PingAir/internal/config"
	"github.com/Oskarovsky/PingAir/internal/db"
	"github.com/Oskarovsky/PingAir/internal/eventlog"
	"github.com/Oskarovsky/PingAir/internal/provider"
	"github.com/Oskarovsky/PingAir/internal/runner"
	"github.com/Oskarovsky/PingAir/internal/ui"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("pingair ")

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cfgPath := "config.toml"
	args := os.Args[1:]
	// parse global --config
	var cmdArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" || args[i] == "-c" {
			if i+1 >= len(args) {
				log.Fatal("--config wymaga ścieżki")
			}
			cfgPath = args[i+1]
			i++
			continue
		}
		cmdArgs = append(cmdArgs, args[i])
	}
	if len(cmdArgs) == 0 {
		usage()
		os.Exit(2)
	}
	cmd := cmdArgs[0]

	switch cmd {
	case "init":
		if _, err := os.Stat(cfgPath); err == nil {
			fmt.Println("już jest:", cfgPath)
			return
		}
		if err := copyFile("config.example.toml", cfgPath); err != nil {
			log.Fatal(err)
		}
		fmt.Println("utworzono", cfgPath, "— edytuj trasy i alerty")
	case "stats":
		cfg, err := config.Load(cfgPath)
		must(err)
		database, err := db.Open(cfg.DBPath)
		must(err)
		defer database.Close()
		rows, err := database.RecentSamples(20)
		must(err)
		for _, r := range rows {
			fmt.Printf("%s  %-28s  %8.2f %s\n", r.FetchedAt, r.RouteName, r.Price, r.Currency)
		}
	case "once":
		cfg, err := config.Load(cfgPath)
		must(err)
		database, err := db.Open(cfg.DBPath)
		must(err)
		defer database.Close()
		must(database.SeedWatchesFromConfig(cfg.Routes))
		prov, err := provider.New(cfg)
		must(err)
		events := eventlog.New()
		must(runner.PollOnce(cfg, database, prov, events))
	case "run":
		cfg, err := config.Load(cfgPath)
		must(err)
		database, err := db.Open(cfg.DBPath)
		must(err)
		defer database.Close()
		must(database.SeedWatchesFromConfig(cfg.Routes))
		prov, err := provider.New(cfg)
		must(err)
		events := eventlog.New()
		started := time.Now().UTC()
		if cfg.UI.Enabled {
			go func() {
				st := ui.State{DBPath: cfg.DBPath, Cfg: cfg, Events: events, StartedAt: started}
				if err := ui.Serve(st); err != nil {
					log.Printf("UI stopped: %v", err)
				}
			}()
		}
		must(runner.RunLoop(cfg, database, prov, events))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `PingAir — flight price watcher (Go)

Usage:
  pingair [--config config.toml] <command>

Commands:
  init    skopiuj config.example.toml → config.toml
  once    jedna runda pomiarów
  run     pętla + UI
  stats   ostatnie próbki
`)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
