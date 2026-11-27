package config

import (
	"fmt"
	"os"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

type Config struct {
	PollIntervalSecs uint64              `toml:"poll_interval_secs"`
	DBPath           string              `toml:"db_path"`
	Provider         string              `toml:"provider"`
	Amadeus          AmadeusConfig       `toml:"amadeus"`
	Travelpayouts    TravelpayoutsConfig `toml:"travelpayouts"`
	Serpapi          SerpapiConfig       `toml:"serpapi"`
	Alert            AlertConfig         `toml:"alert"`
	UI               UIConfig            `toml:"ui"`
	Routes           []Route             `toml:"routes"`
}

type UIConfig struct {
	Enabled bool   `toml:"enabled"`
	Bind    string `toml:"bind"`
	Token   string `toml:"token"`
}

type AmadeusConfig struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	Hostname     string `toml:"hostname"`
}

type TravelpayoutsConfig struct {
	Token  string `toml:"token"`
	Market string `toml:"market"`
}

type SerpapiConfig struct {
	APIKey string `toml:"api_key"`
	GL     string `toml:"gl"`
	HL     string `toml:"hl"`
}

type AlertConfig struct {
	DropPct       float64        `toml:"drop_pct"`
	MinSamples    int            `toml:"min_samples"`
	WindowHours   uint64         `toml:"window_hours"`
	CooldownHours uint64         `toml:"cooldown_hours"`
	Ntfy          NtfyConfig     `toml:"ntfy"`
	Telegram      TelegramConfig `toml:"telegram"`
}

type NtfyConfig struct {
	Enabled bool   `toml:"enabled"`
	Server  string `toml:"server"`
	Topic   string `toml:"topic"`
}

type TelegramConfig struct {
	Enabled  bool   `toml:"enabled"`
	BotToken string `toml:"bot_token"`
	ChatID   string `toml:"chat_id"`
}

type Route struct {
	Name        string `toml:"name"`
	Origin      string `toml:"origin"`
	Destination string `toml:"destination"`
	DepartDate  string `toml:"depart_date"`
	ReturnDate  string `toml:"return_date"`
	Adults      uint32 `toml:"adults"`
	Currency    string `toml:"currency"`
}

func (r Route) Key() string {
	return fmt.Sprintf("%s|%s|%s|%s|%s",
		strings.ToUpper(r.Origin),
		strings.ToUpper(r.Destination),
		r.DepartDate,
		r.ReturnDate,
		strings.ToUpper(r.Currency),
	)
}

// RoundTripKey — seria próbek sumy (tam + powrót ±10 dni).
func (r Route) RoundTripKey() string {
	return fmt.Sprintf("%s|%s|RT|%s",
		strings.ToUpper(r.Origin),
		strings.ToUpper(r.Destination),
		strings.ToUpper(r.Currency),
	)
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("toml: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.PollIntervalSecs == 0 {
		c.PollIntervalSecs = 900
	}
	if c.DBPath == "" {
		c.DBPath = "data/pingair.db"
	}
	if c.Provider == "" {
		c.Provider = "mock"
	}
	if c.UI.Bind == "" {
		c.UI.Bind = "127.0.0.1:3850"
	}
	if !c.UI.Enabled && c.UI.Bind != "" {
		// leave as decoded; default enabled true if section missing is tricky — set if zero-value whole UI
	}
	if c.Alert.DropPct == 0 {
		c.Alert.DropPct = 12
	}
	if c.Alert.MinSamples == 0 {
		c.Alert.MinSamples = 8
	}
	if c.Alert.WindowHours == 0 {
		c.Alert.WindowHours = 168
	}
	if c.Alert.CooldownHours == 0 {
		c.Alert.CooldownHours = 12
	}
	if c.Alert.Ntfy.Server == "" {
		c.Alert.Ntfy.Server = "https://ntfy.sh"
	}
	if c.Travelpayouts.Market == "" {
		c.Travelpayouts.Market = "pl"
	}
	if c.Serpapi.GL == "" {
		c.Serpapi.GL = "pl"
	}
	if c.Serpapi.HL == "" {
		c.Serpapi.HL = "pl"
	}
	for i := range c.Routes {
		if c.Routes[i].Adults == 0 {
			c.Routes[i].Adults = 1
		}
		if c.Routes[i].Currency == "" {
			c.Routes[i].Currency = "PLN"
		}
	}
	// UI enabled default: if [ui] present with enabled omitted, pelletier gives false.
	// Config examples set enabled=true explicitly.
}

func (c *Config) validate() error {
	if len(c.Routes) == 0 {
		return fmt.Errorf("brak [[routes]]")
	}
	if c.PollIntervalSecs < 60 {
		return fmt.Errorf("poll_interval_secs minimum 60")
	}
	switch c.Provider {
	case "mock", "amadeus", "travelpayouts", "aviasales", "serpapi", "google_flights", "google":
	default:
		return fmt.Errorf("nieznany provider: %s", c.Provider)
	}
	if c.Provider == "travelpayouts" || c.Provider == "aviasales" {
		if c.Travelpayouts.Token == "" {
			return fmt.Errorf("provider=travelpayouts wymaga travelpayouts.token")
		}
	}
	if c.Provider == "serpapi" || c.Provider == "google_flights" || c.Provider == "google" {
		if c.Serpapi.APIKey == "" {
			return fmt.Errorf("provider=serpapi wymaga serpapi.api_key")
		}
	}
	return nil
}
