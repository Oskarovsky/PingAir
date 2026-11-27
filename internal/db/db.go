package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Oskarovsky/PingAir/internal/provider"
	"github.com/Oskarovsky/PingAir/internal/stats"
	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA busy_timeout=5000;
		CREATE TABLE IF NOT EXISTS samples (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			route_key TEXT NOT NULL,
			route_name TEXT NOT NULL,
			price REAL NOT NULL,
			currency TEXT NOT NULL,
			carrier TEXT NOT NULL DEFAULT '',
			deep_link TEXT NOT NULL DEFAULT '',
			fetched_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_samples_route_time ON samples(route_key, fetched_at);
		CREATE TABLE IF NOT EXISTS alerts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			route_key TEXT NOT NULL,
			price REAL NOT NULL,
			mean REAL NOT NULL,
			drop_pct REAL NOT NULL,
			message TEXT NOT NULL,
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_alerts_route_time ON alerts(route_key, created_at);
	`); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &DB{conn: conn}, nil
}

func (d *DB) Close() error { return d.conn.Close() }

func (d *DB) InsertSample(q provider.Quote) error {
	_, err := d.conn.Exec(
		`INSERT INTO samples (route_key, route_name, price, currency, carrier, deep_link, fetched_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		q.RouteKey, q.RouteName, q.Price, q.Currency, q.Carrier, q.DeepLink, q.FetchedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) PricesForRoute(routeKey string, windowHours uint64) ([]float64, error) {
	q := `SELECT price FROM samples WHERE route_key = ?`
	args := []any{routeKey}
	if windowHours > 0 {
		since := time.Now().UTC().Add(-time.Duration(windowHours) * time.Hour).Format(time.RFC3339)
		q += ` AND fetched_at >= ?`
		args = append(args, since)
	}
	q += ` ORDER BY fetched_at ASC`
	rows, err := d.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var p float64
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) LastAlertAt(routeKey string) (*time.Time, error) {
	var s string
	err := d.conn.QueryRow(
		`SELECT created_at FROM alerts WHERE route_key = ? ORDER BY id DESC LIMIT 1`, routeKey,
	).Scan(&s)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, nil
	}
	t = t.UTC()
	return &t, nil
}

func (d *DB) InsertAlert(routeKey string, price, mean, dropPct float64, message string) error {
	_, err := d.conn.Exec(
		`INSERT INTO alerts (route_key, price, mean, drop_pct, message, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		routeKey, price, mean, dropPct, message, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) RecentSamples(limit int) ([]SampleRow, error) {
	rows, err := d.conn.Query(
		`SELECT route_name, price, currency, fetched_at FROM samples ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SampleRow
	for rows.Next() {
		var r SampleRow
		if err := rows.Scan(&r.RouteName, &r.Price, &r.Currency, &r.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type SampleRow struct {
	RouteName string
	Price     float64
	Currency  string
	FetchedAt string
}

type AlertRow struct {
	RouteKey  string
	Price     float64
	Mean      float64
	DropPct   float64
	Message   string
	CreatedAt string
}

func (d *DB) RecentAlerts(limit int) ([]AlertRow, error) {
	rows, err := d.conn.Query(
		`SELECT route_key, price, mean, drop_pct, message, created_at FROM alerts ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlertRow
	for rows.Next() {
		var r AlertRow
		if err := rows.Scan(&r.RouteKey, &r.Price, &r.Mean, &r.DropPct, &r.Message, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) RouteSnapshot(routeKey string, windowHours uint64) (*stats.RouteStats, error) {
	prices, err := d.PricesForRoute(routeKey, windowHours)
	if err != nil {
		return nil, err
	}
	if len(prices) == 0 {
		return nil, nil
	}
	return stats.Summarize(prices, prices[len(prices)-1]), nil
}

func (d *DB) SampleCount() (int64, error) {
	var n int64
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM samples`).Scan(&n)
	return n, err
}

func (d *DB) AlertCount() (int64, error) {
	var n int64
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM alerts`).Scan(&n)
	return n, err
}

func (d *DB) String() string { return fmt.Sprintf("db(%p)", d) }
