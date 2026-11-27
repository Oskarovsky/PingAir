package alert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Oskarovsky/PingAir/internal/config"
	"github.com/Oskarovsky/PingAir/internal/stats"
)

func Notify(cfg config.AlertConfig, b *stats.Breakout) error {
	msg := formatMessage(b)
	fmt.Println(msg)

	if cfg.Ntfy.Enabled && cfg.Ntfy.Topic != "" {
		if err := sendNtfy(cfg.Ntfy.Server, cfg.Ntfy.Topic, msg); err != nil {
			return err
		}
	}
	if cfg.Telegram.Enabled && cfg.Telegram.BotToken != "" && cfg.Telegram.ChatID != "" {
		if err := sendTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID, msg); err != nil {
			return err
		}
	}
	return nil
}

func formatMessage(b *stats.Breakout) string {
	return fmt.Sprintf(
		"✈️ PingAir ALARM — %s\nCena: %.2f %s (przewoźnik: %s)\nŚrednia: %.2f %s (n=%d)\nDump: −%.1f%% vs średnia (min %.2f / max %.2f)\n%s\nSygnał: warto sprawdzić zakup.",
		b.Quote.RouteName,
		b.Quote.Price, b.Quote.Currency, b.Quote.Carrier,
		b.Stats.Mean, b.Quote.Currency, b.Stats.Samples,
		b.DropPctVsMean, b.Stats.Min, b.Stats.Max,
		b.Quote.DeepLink,
	)
}

func sendNtfy(server, topic, message string) error {
	base := strings.TrimRight(server, "/")
	req, err := http.NewRequest(http.MethodPost, base+"/"+topic, strings.NewReader(message))
	if err != nil {
		return err
	}
	req.Header.Set("Title", "PingAir — dump ceny lotu")
	req.Header.Set("Priority", "high")
	req.Header.Set("Tags", "airplane,warning")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ntfy: %s", b)
	}
	return nil
}

func sendTelegram(token, chatID, message string) error {
	payload, _ := json.Marshal(map[string]any{
		"chat_id":                  chatID,
		"text":                     message,
		"disable_web_page_preview": false,
	})
	url := "https://api.telegram.org/bot" + token + "/sendMessage"
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram: %s", b)
	}
	return nil
}
