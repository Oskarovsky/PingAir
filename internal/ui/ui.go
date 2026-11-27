package ui

import (
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Oskarovsky/PingAir/internal/config"
	"github.com/Oskarovsky/PingAir/internal/db"
	"github.com/Oskarovsky/PingAir/internal/discover"
	"github.com/Oskarovsky/PingAir/internal/eventlog"
	"github.com/Oskarovsky/PingAir/internal/provider"
	"github.com/Oskarovsky/PingAir/internal/runner"
)

type State struct {
	DBPath    string
	Cfg       *config.Config
	Events    *eventlog.EventLog
	StartedAt time.Time
	PollMu    sync.Mutex
}

func Serve(st *State) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/", st.dashboard)
	mux.HandleFunc("/watch/add", st.addWatch)
	mux.HandleFunc("/watch/delete", st.deleteWatch)
	mux.HandleFunc("/watch/discover", st.discoverWatch)
	mux.HandleFunc("/watch/poll", st.pollNow)

	log.Printf("PingAir UI on %s", st.Cfg.UI.Bind)
	return http.ListenAndServe(st.Cfg.UI.Bind, st.auth(mux))
}

func (st *State) auth(next http.Handler) http.Handler {
	token := st.Cfg.UI.Token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ok := r.Header.Get("X-PingAir-Token") == token ||
			r.URL.Query().Get("token") == token ||
			r.FormValue("token") == token ||
			strings.Contains(r.URL.RawQuery, "token="+token)
		if !ok && r.Method == http.MethodPost {
			_ = r.ParseForm()
			ok = r.FormValue("token") == token
		}
		if !ok {
			http.Error(w, "PingAir: podaj ?token=… albo nagłówek X-PingAir-Token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (st *State) qToken(r *http.Request) string {
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	return st.Cfg.UI.Token
}

func (st *State) redirectHome(w http.ResponseWriter, r *http.Request, flash string) {
	tok := st.qToken(r)
	u := "/?token=" + tok
	if flash != "" {
		u += "&msg=" + strings.ReplaceAll(flash, " ", "+")
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func (st *State) addWatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	_ = r.ParseForm()
	route := config.Route{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Origin:      strings.ToUpper(strings.TrimSpace(r.FormValue("origin"))),
		Destination: strings.ToUpper(strings.TrimSpace(r.FormValue("destination"))),
		DepartDate:  strings.TrimSpace(r.FormValue("depart_date")),
		ReturnDate:  strings.TrimSpace(r.FormValue("return_date")),
		Currency:    strings.ToUpper(strings.TrimSpace(r.FormValue("currency"))),
		Adults:      1,
	}
	if route.Origin == "" || route.Destination == "" {
		st.redirectHome(w, r, "podaj+origin+i+destination")
		return
	}
	if route.Currency == "" {
		route.Currency = "PLN"
	}
	if route.Name == "" {
		route.Name = route.Origin + " → " + route.Destination
	}
	database, err := db.Open(st.DBPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer database.Close()
	if err := database.UpsertWatch(route); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	st.Events.Push("watch + " + route.Name)
	st.redirectHome(w, r, "dodano+"+route.Origin+"→"+route.Destination)
}

func (st *State) deleteWatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	_ = r.ParseForm()
	key := r.FormValue("route_key")
	database, err := db.Open(st.DBPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer database.Close()
	_ = database.DeleteWatch(key)
	st.Events.Push("watch - " + key)
	st.redirectHome(w, r, "usunieto")
}

func (st *State) discoverWatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	_ = r.ParseForm()
	mode := r.FormValue("mode") // city | country
	origin := strings.ToUpper(strings.TrimSpace(r.FormValue("origin")))
	country := strings.ToUpper(strings.TrimSpace(r.FormValue("country")))
	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	if currency == "" {
		currency = "PLN"
	}
	limit, _ := strconv.Atoi(r.FormValue("limit"))
	if limit <= 0 {
		limit = 25
	}
	token := st.Cfg.Travelpayouts.Token
	if token == "" {
		st.redirectHome(w, r, "brak+travelpayouts.token")
		return
	}
	market := st.Cfg.Travelpayouts.Market

	var dirs []discover.Direction
	var err error
	if mode == "country" {
		if country == "" {
			country = "PL"
		}
		dirs, err = discover.PopularFromCountry(token, country, currency, market, 12, limit)
	} else {
		if origin == "" {
			origin = "WAW"
		}
		dirs, err = discover.PopularFromOrigin(token, origin, currency, market, limit)
	}
	if err != nil {
		st.redirectHome(w, r, "discover+error")
		st.Events.Push("discover error: " + err.Error())
		return
	}

	database, err := db.Open(st.DBPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer database.Close()

	added := 0
	for _, d := range dirs {
		route := discover.ToRoute(d, currency)
		if err := database.UpsertWatch(route); err != nil {
			continue
		}
		added++
	}
	msg := fmt.Sprintf("dodano %d tras", added)
	st.Events.Push("discover: " + msg)
	st.redirectHome(w, r, strings.ReplaceAll(msg, " ", "+"))
}

func (st *State) pollNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if !st.PollMu.TryLock() {
		st.redirectHome(w, r, "poll+już+trwa")
		return
	}
	defer st.PollMu.Unlock()

	st.Events.Push("manual poll…")
	database, err := db.Open(st.DBPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer database.Close()
	prov, err := provider.New(st.Cfg)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := runner.PollOnce(st.Cfg, database, prov, st.Events); err != nil {
		st.Events.Push("manual poll failed: " + err.Error())
		st.redirectHome(w, r, "poll+błąd")
		return
	}
	n, _ := database.WatchCount()
	st.Events.Push(fmt.Sprintf("manual poll ok (%d tras)", n))
	st.redirectHome(w, r, fmt.Sprintf("pobrano+%d+tras", n))
}

func (st *State) dashboard(w http.ResponseWriter, r *http.Request) {
	database, err := db.Open(st.DBPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer database.Close()

	watches, _ := database.ListWatches()
	samplesN, _ := database.SampleCount()
	alertsN, _ := database.AlertCount()
	uptime := time.Since(st.StartedAt).Truncate(time.Second)
	tok := st.qToken(r)
	flash := r.URL.Query().Get("msg")

	var routes strings.Builder
	for _, route := range watches {
		snap, _ := database.RouteSnapshot(route.Key(), st.Cfg.Alert.WindowHours)
		fares, _, _ := database.RouteFares(route.Key())
		watchHint := route.DepartDate
		if watchHint == "" {
			watchHint = "flex"
		}
		faresHTML := formatFaresHTML(fares)
		del := fmt.Sprintf(
			`<form method="post" action="/watch/delete" style="display:inline" onsubmit="return confirm('Usunąć?')">
			<input type="hidden" name="token" value="%s">
			<input type="hidden" name="route_key" value="%s">
			<button type="submit" class="linkish">usuń</button></form>`,
			html.EscapeString(tok), html.EscapeString(route.Key()),
		)
		if snap == nil {
			routes.WriteString(fmt.Sprintf(
				`<tr><td><strong>%s</strong><div class="muted">%s → %s · watch %s</div></td>
				<td colspan="2" class="muted">brak próbek</td><td colspan="3">%s</td><td>%s</td></tr>`,
				html.EscapeString(route.Name), html.EscapeString(route.Origin), html.EscapeString(route.Destination),
				html.EscapeString(watchHint), faresHTML, del,
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
			`<tr class="%s"><td><strong>%s</strong><div class="muted">%s → %s · watch %s</div></td>
			<td class="num">%.2f</td><td class="num">%.2f</td><td class="num">%+.1f%%</td>
			<td>%s</td><td class="num">%d</td><td>%s</td></tr>`,
			cls, html.EscapeString(route.Name), html.EscapeString(route.Origin), html.EscapeString(route.Destination),
			html.EscapeString(watchHint),
			snap.Latest, snap.Mean, vs,
			faresHTML, snap.Samples, del,
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

	flashHTML := ""
	if flash != "" {
		flashHTML = `<div class="flash">` + html.EscapeString(flash) + `</div>`
	}

	page := fmt.Sprintf(`<!doctype html>
<html lang="pl"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>PingAir</title>
<style>
:root{--bg:#0f1419;--card:#1a2332;--text:#e7ecf3;--muted:#8b9bb4;--hot:#ff6b6b;--down:#f0c14b;--up:#6bcb77;--line:#2a3548;--accent:#4da3ff}
*{box-sizing:border-box}body{margin:0;font:15px/1.45 system-ui,sans-serif;background:var(--bg);color:var(--text)}
header{padding:1.25rem 1.5rem;border-bottom:1px solid var(--line);display:flex;justify-content:space-between;gap:1rem;flex-wrap:wrap}
h1{margin:0;font-size:1.25rem} h2{margin:0 0 .75rem;font-size:1rem}
.muted{color:var(--muted);font-size:.85rem}
main{padding:1.25rem 1.5rem;display:grid;gap:1.25rem}
.stats{display:flex;gap:1rem;flex-wrap:wrap}
.pill{background:var(--card);padding:.6rem 1rem;border-radius:8px;border:1px solid var(--line)}
table{width:100%%;border-collapse:collapse;background:var(--card);border-radius:10px;overflow:hidden;border:1px solid var(--line)}
th,td{padding:.65rem .75rem;text-align:left;border-bottom:1px solid var(--line);vertical-align:middle}
th{font-size:.72rem;text-transform:uppercase;letter-spacing:.04em;color:var(--muted)}
.num{text-align:right;font-variant-numeric:tabular-nums}
tr.hot td:nth-child(4){color:var(--hot);font-weight:600}
tr.down td:nth-child(4){color:var(--down)} tr.up td:nth-child(4){color:var(--up)}
pre{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:1rem;overflow:auto;max-height:280px;font-size:12px}
.alerts{list-style:none;padding:0;margin:0}.alerts li{padding:.5rem 0;border-bottom:1px solid var(--line)}
.panel{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:1rem}
.grid{display:grid;gap:1rem;grid-template-columns:repeat(auto-fit,minmax(260px,1fr))}
label{display:block;font-size:.75rem;color:var(--muted);margin:.4rem 0 .15rem}
input,select,button{font:inherit;border-radius:6px;border:1px solid var(--line);background:#0f1419;color:var(--text);padding:.45rem .6rem}
button{background:var(--accent);border-color:transparent;color:#041018;font-weight:600;cursor:pointer}
button.linkish{background:transparent;color:var(--muted);border:0;padding:0;font-weight:500;text-decoration:underline}
.row{display:flex;gap:.5rem;flex-wrap:wrap;align-items:end}
.flash{background:#1e3a2f;border:1px solid #2f6b4f;padding:.6rem 1rem;border-radius:8px}
.toolbar{display:flex;gap:.75rem;flex-wrap:wrap;align-items:center;justify-content:space-between}
.btn-now{background:#6bcb77}
input#routeFilter{min-width:12rem;flex:1}
tr.hidden{display:none}
.fares{list-style:none;margin:0;padding:0;font-size:.85rem;line-height:1.35}
.fares li{padding:.1rem 0}
.fares .p{font-variant-numeric:tabular-nums;font-weight:600}
.fares .d{color:var(--muted)}
</style>
</head><body>
<header>
  <div><h1>PingAir</h1><div class="muted">provider=%s · poll=%ds · uptime %s</div></div>
  <div class="muted">próbki %d · alarmy %d</div>
</header>
<main>
%s
<section><div class="stats">
  <div class="pill">Obserwowane: <strong>%d</strong></div>
  <div class="pill">Próg dump: <strong>−%.0f%%</strong></div>
  <div class="pill">Min próbek: <strong>%d</strong></div>
  <form method="post" action="/watch/poll" style="margin:0">
    <input type="hidden" name="token" value="%s">
    <button type="submit" class="btn-now">Pobierz teraz</button>
  </form>
</div></section>

<section class="grid">
  <div class="panel">
    <h2>Dodaj trasę</h2>
    <form method="post" action="/watch/add">
      <input type="hidden" name="token" value="%s">
      <div class="row">
        <div><label>Origin</label><input name="origin" placeholder="WAW" required maxlength="3" style="width:5rem"></div>
        <div><label>Destination</label><input name="destination" placeholder="BCN" required maxlength="3" style="width:5rem"></div>
        <div><label>Waluta</label><input name="currency" value="PLN" maxlength="3" style="width:5rem"></div>
      </div>
      <div class="row">
        <div><label>Wylot (opcjonalnie)</label><input type="date" name="depart_date"></div>
        <div><label>Powrót</label><input type="date" name="return_date"></div>
      </div>
      <p class="muted">Bez dat = najtańszy bilet na trasie z cache Aviasales.</p>
      <button type="submit">Dodaj do obserwacji</button>
    </form>
  </div>
  <div class="panel">
    <h2>Popularne z miasta</h2>
    <form method="post" action="/watch/discover">
      <input type="hidden" name="token" value="%s">
      <input type="hidden" name="mode" value="city">
      <div class="row">
        <div><label>Miasto (IATA)</label><input name="origin" value="WAW" maxlength="3" style="width:5rem"></div>
        <div><label>Limit</label><input name="limit" type="number" value="25" min="5" max="80" style="width:5rem"></div>
        <div><label>Waluta</label><input name="currency" value="PLN" maxlength="3" style="width:5rem"></div>
      </div>
      <p class="muted">Travelpayouts: popularne kierunki z jednego lotniska/miasta.</p>
      <button type="submit">Załaduj popularne</button>
    </form>
  </div>
  <div class="panel">
    <h2>Popularne z kraju (huby)</h2>
    <form method="post" action="/watch/discover">
      <input type="hidden" name="token" value="%s">
      <input type="hidden" name="mode" value="country">
      <div class="row">
        <div><label>Kraj</label>
          <select name="country">
            <option value="PL">PL — Polska</option>
            <option value="DE">DE — Niemcy</option>
            <option value="ES">ES — Hiszpania</option>
            <option value="IT">IT — Włochy</option>
            <option value="FR">FR — Francja</option>
            <option value="GB">GB — UK</option>
          </select>
        </div>
        <div><label>Max tras</label><input name="limit" type="number" value="60" min="10" max="120" style="width:5rem"></div>
        <div><label>Waluta</label><input name="currency" value="PLN" maxlength="3" style="width:5rem"></div>
      </div>
      <p class="muted">API nie ma „wszystkich tras z kraju” — bierzemy popularne z głównych hubów (WAW, KRK, GDN…).</p>
      <button type="submit">Załaduj z hubów</button>
    </form>
  </div>
</section>

<section>
  <div class="toolbar">
    <h2 style="margin:0">Trasy <span class="muted" id="filterCount"></span></h2>
    <input id="routeFilter" type="search" placeholder="Filtr: WAW, BCN, dump…" autocomplete="off">
  </div>
  <table id="routesTable"><thead><tr><th>Trasa</th><th class="num">Ostatnia</th><th class="num">Średnia</th><th class="num">vs mean</th><th>Najtańsze daty (do 3)</th><th class="num">n</th><th></th></tr></thead>
<tbody>%s</tbody></table>
</section>
<section><h2>Alarmy</h2>%s</section>
<section><h2>Log</h2><pre>%s</pre></section>
</main>
<script>
(function(){
  const input = document.getElementById('routeFilter');
  const rows = [...document.querySelectorAll('#routesTable tbody tr')];
  const countEl = document.getElementById('filterCount');
  function apply(){
    const q = (input.value || '').trim().toLowerCase();
    let shown = 0;
    for (const tr of rows) {
      const hay = tr.innerText.toLowerCase();
      const ok = !q || hay.includes(q);
      tr.classList.toggle('hidden', !ok);
      if (ok) shown++;
    }
    countEl.textContent = q ? '(' + shown + '/' + rows.length + ')' : '(' + rows.length + ')';
  }
  input.addEventListener('input', apply);
  apply();
})();
</script>
</body></html>`,
		html.EscapeString(st.Cfg.Provider), st.Cfg.PollIntervalSecs, uptime,
		samplesN, alertsN,
		flashHTML,
		len(watches), st.Cfg.Alert.DropPct, st.Cfg.Alert.MinSamples,
		html.EscapeString(tok),
		html.EscapeString(tok), html.EscapeString(tok), html.EscapeString(tok),
		routes.String(), alertsHTML.String(), logHTML.String(),
	)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(page))
}

func formatFaresHTML(fares []db.StoredFare) string {
	if len(fares) == 0 {
		return `<span class="muted">—</span>`
	}
	var b strings.Builder
	b.WriteString(`<ul class="fares">`)
	for _, f := range fares {
		dep := f.DepartDate
		if dep == "" {
			dep = "?"
		}
		ret := ""
		if f.ReturnDate != "" {
			ret = " → " + f.ReturnDate
		}
		car := f.Carrier
		if car == "" {
			car = "—"
		}
		b.WriteString(fmt.Sprintf(
			`<li><span class="p">%.0f</span> <span class="d">%s%s</span> · %s</li>`,
			f.Price, html.EscapeString(dep), html.EscapeString(ret), html.EscapeString(car),
		))
	}
	b.WriteString(`</ul>`)
	return b.String()
}
