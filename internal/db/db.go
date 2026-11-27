package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Oskarovsky/PingAir/internal/config"
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
		CREATE TABLE IF NOT EXISTS watches (
			route_key TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			origin TEXT NOT NULL,
			destination TEXT NOT NULL,
			depart_date TEXT NOT NULL DEFAULT '',
			return_date TEXT NOT NULL DEFAULT '',
			adults INTEGER NOT NULL DEFAULT 1,
			currency TEXT NOT NULL DEFAULT 'PLN',
			created_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS route_fares (
			route_key TEXT PRIMARY KEY,
			currency TEXT NOT NULL DEFAULT 'PLN',
			payload TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS route_rt (
			route_key TEXT PRIMARY KEY,
			total REAL NOT NULL,
			out_price REAL NOT NULL,
			ret_price REAL NOT NULL,
			out_depart TEXT NOT NULL DEFAULT '',
			ret_depart TEXT NOT NULL DEFAULT '',
			out_return TEXT NOT NULL DEFAULT '',
			ret_return TEXT NOT NULL DEFAULT '',
			out_link TEXT NOT NULL DEFAULT '',
			ret_link TEXT NOT NULL DEFAULT '',
			out_who TEXT NOT NULL DEFAULT '',
			ret_who TEXT NOT NULL DEFAULT '',
			currency TEXT NOT NULL DEFAULT 'PLN',
			updated_at TEXT NOT NULL
		);
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

func (d *DB) UpsertRouteFares(q provider.Quote) error {
	opts := q.Options
	if len(opts) == 0 {
		opts = []provider.FareOption{{
			Price: q.Price, Carrier: q.Carrier, DepartDate: q.DepartDate, ReturnDate: q.ReturnDate, Link: q.DeepLink,
		}}
	}
	type row struct {
		Price      float64 `json:"price"`
		Carrier    string  `json:"carrier"`
		Gate       string  `json:"gate"`
		DepartDate string  `json:"depart"`
		ReturnDate string  `json:"return"`
		Link       string  `json:"link"`
	}
	payload := make([]row, 0, len(opts))
	for _, o := range opts {
		payload = append(payload, row{o.Price, o.Carrier, o.Gate, o.DepartDate, o.ReturnDate, o.Link})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = d.conn.Exec(
		`INSERT INTO route_fares (route_key, currency, payload, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(route_key) DO UPDATE SET currency=excluded.currency, payload=excluded.payload, updated_at=excluded.updated_at`,
		q.RouteKey, q.Currency, string(raw), time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

type StoredFare struct {
	Price      float64 `json:"price"`
	Carrier    string  `json:"carrier"`
	Gate       string  `json:"gate"`
	DepartDate string  `json:"depart"`
	ReturnDate string  `json:"return"`
	Link       string  `json:"link"`
}

type RoundTripInfo struct {
	Total     float64
	OutPrice  float64
	RetPrice  float64
	OutDepart string
	RetDepart string
	OutReturn string
	RetReturn string
	OutLink   string
	RetLink   string
	OutWho    string
	RetWho    string
	Currency  string
}

func (d *DB) UpsertRoundTrip(watchKey string, info RoundTripInfo) error {
	_, err := d.conn.Exec(
		`INSERT INTO route_rt (
			route_key, total, out_price, ret_price, out_depart, ret_depart, out_return, ret_return,
			out_link, ret_link, out_who, ret_who, currency, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(route_key) DO UPDATE SET
			total=excluded.total, out_price=excluded.out_price, ret_price=excluded.ret_price,
			out_depart=excluded.out_depart, ret_depart=excluded.ret_depart,
			out_return=excluded.out_return, ret_return=excluded.ret_return,
			out_link=excluded.out_link, ret_link=excluded.ret_link,
			out_who=excluded.out_who, ret_who=excluded.ret_who,
			currency=excluded.currency, updated_at=excluded.updated_at`,
		watchKey, info.Total, info.OutPrice, info.RetPrice,
		info.OutDepart, info.RetDepart, info.OutReturn, info.RetReturn,
		info.OutLink, info.RetLink, info.OutWho, info.RetWho, info.Currency,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) RoundTrip(watchKey string) (*RoundTripInfo, error) {
	var info RoundTripInfo
	err := d.conn.QueryRow(
		`SELECT total, out_price, ret_price, out_depart, ret_depart, out_return, ret_return,
		        out_link, ret_link, out_who, ret_who, currency
		 FROM route_rt WHERE route_key = ?`, watchKey,
	).Scan(
		&info.Total, &info.OutPrice, &info.RetPrice, &info.OutDepart, &info.RetDepart,
		&info.OutReturn, &info.RetReturn, &info.OutLink, &info.RetLink, &info.OutWho, &info.RetWho, &info.Currency,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &info, nil
}

func (d *DB) RouteFares(routeKey string) ([]StoredFare, string, error) {
	var payload, currency string
	err := d.conn.QueryRow(
		`SELECT payload, currency FROM route_fares WHERE route_key = ?`, routeKey,
	).Scan(&payload, &currency)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var out []StoredFare
	if err := json.Unmarshal([]byte(payload), &out); err != nil {
		return nil, currency, err
	}
	return out, currency, nil
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

// SeedWatchesFromConfig kopiuje [[routes]] z TOML gdy tabela watches jest pusta.
func (d *DB) SeedWatchesFromConfig(routes []config.Route) error {
	n, err := d.WatchCount()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, r := range routes {
		if err := d.UpsertWatch(r); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) WatchCount() (int64, error) {
	var n int64
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM watches`).Scan(&n)
	return n, err
}

func (d *DB) ListWatches() ([]config.Route, error) {
	rows, err := d.conn.Query(
		`SELECT name, origin, destination, depart_date, return_date, adults, currency FROM watches ORDER BY origin, destination`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []config.Route
	for rows.Next() {
		var r config.Route
		var adults int
		if err := rows.Scan(&r.Name, &r.Origin, &r.Destination, &r.DepartDate, &r.ReturnDate, &adults, &r.Currency); err != nil {
			return nil, err
		}
		r.Adults = uint32(adults)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) UpsertWatch(r config.Route) error {
	if r.Adults == 0 {
		r.Adults = 1
	}
	if r.Currency == "" {
		r.Currency = "PLN"
	}
	if r.Name == "" {
		r.Name = fmt.Sprintf("%s → %s", r.Origin, r.Destination)
	}
	_, err := d.conn.Exec(
		`INSERT INTO watches (route_key, name, origin, destination, depart_date, return_date, adults, currency, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(route_key) DO UPDATE SET
		   name=excluded.name, origin=excluded.origin, destination=excluded.destination,
		   depart_date=excluded.depart_date, return_date=excluded.return_date,
		   adults=excluded.adults, currency=excluded.currency`,
		r.Key(), r.Name, strings.ToUpper(r.Origin), strings.ToUpper(r.Destination),
		r.DepartDate, r.ReturnDate, r.Adults, strings.ToUpper(r.Currency),
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (d *DB) DeleteWatch(routeKey string) error {
	_, err := d.conn.Exec(`DELETE FROM watches WHERE route_key = ?`, routeKey)
	return err
}
