package ui

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Oskarovsky/PingAir/internal/config"
	"github.com/Oskarovsky/PingAir/internal/db"
	"github.com/Oskarovsky/PingAir/internal/eventlog"
)

type State struct {
	DBPath    string
	Cfg       *config.Config
	Events    *eventlog.EventLog
	StartedAt time.Time
}

func Serve(st State) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", st.dashboard)

	log.Printf("PingAir UI on %s", st.Cfg.UI.Bind)
	return http.ListenAndServe(st.Cfg.UI.Bind, st.auth(mux))
}

func (st State) auth(next http.Handler) http.Handler {
	token := st.Cfg.UI.Token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ok := r.Header.Get("X-PingAir-Token") == token ||
			r.URL.Query().Get("token") == token ||
			strings.Contains(r.URL.RawQuery, "token="+token)
		if !ok {
			http.Error(w, "PingAir: podaj ?token=… albo nagłówek X-PingAir-Token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (st State) dashboard(w http.ResponseWriter, _ *http.Request) {
	database, err := db.Open(st.DBPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer database.Close()

	samplesN, _ := database.SampleCount()
	alertsN, _ := database.AlertCount()
	uptime := time.Since(st.StartedAt).Truncate(time.Second)

	var routes strings.Builder
	for _, r := range st.Cfg.Routes {
		snap, _ := database.RouteSnapshot(r.Key(), st.Cfg.Alert.WindowHours)
		if snap == nil {
			routes.WriteString(fmt.Sprintf(
				`<tr><td><strong>%s</strong><div class="muted">%s → %s · %s</div></td><td colspan="5" class="muted">brak próbek</td></tr>`,
				html.EscapeString(r.Name), html.EscapeString(r.Origin), html.EscapeString(r.Destination), html.EscapeString(r.DepartDate),
			))
			continue
		}
		vs := 0.0
		if snap.Mean > 0 {
			vs = (snap.Latest - snap.Mean) / snap.Mean * 100
		}
		cls := "up"
		if vs <= -st.Cfg.Alert.DropPct {
			cls = "hot"
		} else if vs < 0 {
			cls = "down"
		}
		routes.WriteString(fmt.Sprintf(
			`<tr class="%s"><td><strong>%s</strong><div class="muted">%s → %s · %s</div></td>
			<td class="num">%.2f</td><td class="num">%.2f</td><td class="num">%+.1f%%</td>
			<td class="num">%.2f – %.2f</td><td class="num">%d</td></tr>`,
			cls, html.EscapeString(r.Name), html.EscapeString(r.Origin), html.EscapeString(r.Destination), html.EscapeString(r.DepartDate),
			snap.Latest, snap.Mean, vs, snap.Min, snap.Max, snap.Samples,
		))
	}

	alerts, _ := database.RecentAlerts(15)
	var alertsHTML strings.Builder
	if len(alerts) == 0 {
		alertsHTML.WriteString(`<p class="muted">Brak alarmów.</p>`)
	} else {
		alertsHTML.WriteString(`<ul class="alerts">`)
		for _, a := range alerts {
			alertsHTML.WriteString(fmt.Sprintf(`<li><span class="muted">%s</span> %s</li>`,
				html.EscapeString(a.CreatedAt), html.EscapeString(a.Message)))
		}
		alertsHTML.WriteString(`</ul>`)
	}

	logs := st.Events.Snapshot()
	var logHTML strings.Builder
	for i := len(logs) - 1; i >= 0; i-- {
		logHTML.WriteString(html.EscapeString(logs[i]))
		logHTML.WriteByte('\n')
	}

	page := fmt.Sprintf(`<!doctype html>
<html lang="pl"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>PingAir</title>
<style>
:root{--bg:#0f1419;--card:#1a2332;--text:#e7ecf3;--muted:#8b9bb4;--hot:#ff6b6b;--down:#f0c14b;--up:#6bcb77;--line:#2a3548}
*{box-sizing:border-box}body{margin:0;font:15px/1.45 system-ui,sans-serif;background:var(--bg);color:var(--text)}
header{padding:1.25rem 1.5rem;border-bottom:1px solid var(--line);display:flex;justify-content:space-between;gap:1rem;flex-wrap:wrap}
h1{margin:0;font-size:1.25rem;letter-spacing:.02em} .muted{color:var(--muted);font-size:.85rem}
main{padding:1.25rem 1.5rem;display:grid;gap:1.25rem}
.stats{display:flex;gap:1rem;flex-wrap:wrap}
.pill{background:var(--card);padding:.6rem 1rem;border-radius:8px;border:1px solid var(--line)}
table{width:100%%;border-collapse:collapse;background:var(--card);border-radius:10px;overflow:hidden;border:1px solid var(--line)}
th,td{padding:.7rem .85rem;text-align:left;border-bottom:1px solid var(--line)} th{font-size:.75rem;text-transform:uppercase;letter-spacing:.04em;color:var(--muted)}
.num{text-align:right;font-variant-numeric:tabular-nums} tr.hot td:nth-child(4){color:var(--hot);font-weight:600}
tr.down td:nth-child(4){color:var(--down)} tr.up td:nth-child(4){color:var(--up)}
pre{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:1rem;overflow:auto;max-height:320px;font-size:12px}
.alerts{list-style:none;padding:0;margin:0} .alerts li{padding:.5rem 0;border-bottom:1px solid var(--line)}
</style>
<meta http-equiv="refresh" content="30">
</head><body>
<header>
  <div><h1>PingAir</h1><div class="muted">provider=%s · poll=%ds · uptime %s</div></div>
  <div class="muted">próbki %d · alarmy %d</div>
</header>
<main>
<section><div class="stats">
  <div class="pill">Trasy: <strong>%d</strong></div>
  <div class="pill">Próg dump: <strong>−%.0f%%</strong></div>
  <div class="pill">Min próbek: <strong>%d</strong></div>
</div></section>
<section><h2>Trasy</h2>
<table><thead><tr><th>Trasa</th><th class="num">Ostatnia</th><th class="num">Średnia</th><th class="num">vs mean</th><th class="num">min–max</th><th class="num">n</th></tr></thead>
<tbody>%s</tbody></table></section>
<section><h2>Alarmy</h2>%s</section>
<section><h2>Log</h2><pre>%s</pre></section>
</main></body></html>`,
		html.EscapeString(st.Cfg.Provider), st.Cfg.PollIntervalSecs, uptime,
		samplesN, alertsN,
		len(st.Cfg.Routes), st.Cfg.Alert.DropPct, st.Cfg.Alert.MinSamples,
		routes.String(), alertsHTML.String(), logHTML.String(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(page))
}
