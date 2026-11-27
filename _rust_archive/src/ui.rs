use crate::config::Config;
use crate::db::Db;
use crate::eventlog::EventLog;
use anyhow::{Context, Result};
use axum::body::Body;
use axum::extract::{Query, State};
use axum::http::{header, Request, StatusCode};
use axum::middleware::{self, Next};
use axum::response::{Html, IntoResponse, Response};
use axum::routing::get;
use axum::Router;
use serde::Deserialize;
use std::path::PathBuf;
use std::sync::Arc;
use tracing::info;

#[derive(Clone)]
pub struct AppState {
    pub db_path: PathBuf,
    pub cfg: Config,
    pub events: EventLog,
    pub started_at: chrono::DateTime<chrono::Utc>,
}

#[derive(Deserialize)]
struct TokenQ {
    #[allow(dead_code)]
    token: Option<String>,
}

pub async fn serve(state: AppState) -> Result<()> {
    let bind = state.cfg.ui.bind.clone();
    let token = state.cfg.ui.token.clone();
    let app = Router::new()
        .route("/", get(dashboard))
        .route("/healthz", get(|| async { "ok" }))
        .layer(middleware::from_fn(move |req, next| {
            let token = token.clone();
            async move { auth_middleware(token, req, next).await }
        }))
        .with_state(Arc::new(state));

    let listener = tokio::net::TcpListener::bind(&bind)
        .await
        .with_context(|| format!("bind UI {bind}"))?;
    info!(%bind, "PingAir UI");
    axum::serve(listener, app).await?;
    Ok(())
}

async fn auth_middleware(token: String, req: Request<Body>, next: Next) -> Response {
    if token.is_empty() || req.uri().path() == "/healthz" {
        return next.run(req).await;
    }
    let hdr = req
        .headers()
        .get("x-pingair-token")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("");
    let q = req.uri().query().unwrap_or("");
    let q_ok = q.split('&').any(|p| p == format!("token={token}") || p.starts_with(&format!("token={token}&")));
    // also accept token= at end
    let q_ok = q_ok || q.contains(&format!("token={token}"));
    if hdr == token || q_ok {
        return next.run(req).await;
    }
    (
        StatusCode::UNAUTHORIZED,
        [(header::CONTENT_TYPE, "text/plain; charset=utf-8")],
        "PingAir: podaj ?token=… albo nagłówek X-PingAir-Token",
    )
        .into_response()
}

async fn dashboard(
    State(st): State<Arc<AppState>>,
    Query(_q): Query<TokenQ>,
) -> impl IntoResponse {
    match render(&st) {
        Ok(html) => Html(html).into_response(),
        Err(e) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            format!("błąd UI: {e:#}"),
        )
            .into_response(),
    }
}

fn render(st: &AppState) -> Result<String> {
    let db = Db::open(&st.db_path)?;
    let samples_n = db.sample_count().unwrap_or(0);
    let alerts_n = db.alert_count().unwrap_or(0);
    let uptime = chrono::Utc::now() - st.started_at;

    let mut routes_html = String::new();
    for r in &st.cfg.routes {
        let key = r.key();
        let snap = db.route_snapshot(&key, st.cfg.alert.window_hours)?;
        let row = match snap {
            Some(s) => {
                let vs = if s.mean > 0.0 {
                    (s.latest - s.mean) / s.mean * 100.0
                } else {
                    0.0
                };
                let cls = if vs <= -st.cfg.alert.drop_pct {
                    "hot"
                } else if vs < 0.0 {
                    "down"
                } else {
                    "up"
                };
                format!(
                    r#"<tr class="{cls}">
                    <td><strong>{}</strong><div class="muted">{} → {} · {}</div></td>
                    <td class="num">{:.2}</td>
                    <td class="num">{:.2}</td>
                    <td class="num">{:+.1}%</td>
                    <td class="num">{:.2} – {:.2}</td>
                    <td class="num">{}</td>
                    <td class="muted">{}</td>
                    </tr>"#,
                    esc(&r.name),
                    esc(&r.origin),
                    esc(&r.destination),
                    esc(&r.depart_date),
                    s.latest,
                    s.mean,
                    vs,
                    s.min,
                    s.max,
                    s.samples,
                    esc(&r.currency),
                )
            }
            None => format!(
                r#"<tr><td><strong>{}</strong><div class="muted">{} → {}</div></td>
                <td colspan="6" class="muted">brak próbek jeszcze</td></tr>"#,
                esc(&r.name),
                esc(&r.origin),
                esc(&r.destination),
            ),
        };
        routes_html.push_str(&row);
    }

    let mut samples_html = String::new();
    for (name, price, cur, at) in db.recent_samples(40)? {
        samples_html.push_str(&format!(
            "<tr><td class=\"muted\">{at}</td><td>{name}</td><td class=\"num\">{price:.2} {cur}</td></tr>",
            at = esc(&at),
            name = esc(&name),
            price = price,
            cur = esc(&cur),
        ));
    }

    let mut alerts_html = String::new();
    for (_key, price, mean, drop, msg, at) in db.recent_alerts(20)? {
        alerts_html.push_str(&format!(
            "<tr class=\"hot\"><td class=\"muted\">{at}</td><td>{msg}</td><td class=\"num\">{price:.2} / mean {mean:.2} (−{drop:.1}%)</td></tr>",
            at = esc(&at),
            msg = esc(&msg),
            price = price,
            mean = mean,
            drop = drop,
        ));
    }
    if alerts_html.is_empty() {
        alerts_html = "<tr><td colspan=\"3\" class=\"muted\">Brak alarmów</td></tr>".into();
    }

    let mut log_html = String::new();
    for (at, msg) in st.events.snapshot().into_iter().take(80) {
        log_html.push_str(&format!(
            "<div class=\"log-line\"><span class=\"muted\">{}</span> {}</div>",
            at.format("%H:%M:%S"),
            esc(&msg)
        ));
    }
    if log_html.is_empty() {
        log_html = "<div class=\"muted\">Brak zdarzeń w pamięci (od startu procesu).</div>".into();
    }

    Ok(format!(
        r#"<!DOCTYPE html>
<html lang="pl">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="30">
<title>PingAir</title>
<style>
  :root {{ --bg:#0c1f2e; --card:#14263a; --ink:#e9eff5; --muted:#8aa0b2; --sea:#3d9b8f; --hot:#e85d4c; --down:#f0c27a; }}
  * {{ box-sizing: border-box; }}
  body {{ margin:0; font:15px/1.45 -apple-system,system-ui,sans-serif; background:var(--bg); color:var(--ink); }}
  header {{ padding:1rem 1.25rem; border-bottom:1px solid rgba(255,255,255,.08); display:flex; flex-wrap:wrap; gap:1rem; align-items:baseline; justify-content:space-between; }}
  h1 {{ margin:0; font-size:1.25rem; letter-spacing:.02em; }}
  main {{ max-width:1100px; margin:0 auto; padding:1rem; display:grid; gap:1rem; }}
  .cards {{ display:grid; grid-template-columns:repeat(auto-fit,minmax(140px,1fr)); gap:.75rem; }}
  .card {{ background:var(--card); border-radius:14px; padding:.9rem 1rem; }}
  .card .v {{ font-size:1.35rem; font-weight:700; }}
  .muted {{ color:var(--muted); font-size:.85rem; }}
  section {{ background:var(--card); border-radius:14px; padding:1rem; overflow:auto; }}
  h2 {{ margin:0 0 .75rem; font-size:1rem; }}
  table {{ width:100%; border-collapse:collapse; font-size:.92rem; }}
  th, td {{ text-align:left; padding:.45rem .35rem; border-bottom:1px solid rgba(255,255,255,.06); vertical-align:top; }}
  th {{ color:var(--muted); font-weight:600; font-size:.75rem; text-transform:uppercase; letter-spacing:.04em; }}
  .num {{ text-align:right; font-variant-numeric:tabular-nums; white-space:nowrap; }}
  tr.hot td {{ color:#ffb4a8; }}
  tr.down td.num:nth-child(4) {{ color:var(--down); }}
  tr.up td.num:nth-child(4) {{ color:var(--sea); }}
  .log {{ font-family:ui-monospace,Menlo,monospace; font-size:.8rem; max-height:280px; overflow:auto; }}
  .log-line {{ padding:.2rem 0; border-bottom:1px solid rgba(255,255,255,.04); }}
  a {{ color:var(--sea); }}
</style>
</head>
<body>
<header>
  <div>
    <h1>✈️ PingAir</h1>
    <div class="muted">provider={provider} · poll co {poll}s · auto-refresh 30s</div>
  </div>
  <div class="muted">uptime {uptime_h}h {uptime_m}m · drop ≤ −{drop}%</div>
</header>
<main>
  <div class="cards">
    <div class="card"><div class="muted">Trasy</div><div class="v">{routes}</div></div>
    <div class="card"><div class="muted">Próbki</div><div class="v">{samples}</div></div>
    <div class="card"><div class="muted">Alarmy</div><div class="v">{alerts}</div></div>
    <div class="card"><div class="muted">Start</div><div class="v" style="font-size:1rem">{started}</div></div>
  </div>

  <section>
    <h2>Statystyki tras</h2>
    <table>
      <thead><tr><th>Trasa</th><th class="num">Ostatnia</th><th class="num">Średnia</th><th class="num">vs średnia</th><th class="num">Min–max</th><th class="num">n</th><th></th></tr></thead>
      <tbody>{routes_html}</tbody>
    </table>
  </section>

  <section>
    <h2>Ostatnie alarmy</h2>
    <table>
      <thead><tr><th>Czas</th><th>Komunikat</th><th class="num">Cena / mean</th></tr></thead>
      <tbody>{alerts_html}</tbody>
    </table>
  </section>

  <section>
    <h2>Ostatnie próbki</h2>
    <table>
      <thead><tr><th>Czas</th><th>Trasa</th><th class="num">Cena</th></tr></thead>
      <tbody>{samples_html}</tbody>
    </table>
  </section>

  <section>
    <h2>Log procesu (od startu)</h2>
    <div class="log">{log_html}</div>
  </section>
</main>
</body>
</html>"#,
        provider = esc(&st.cfg.provider),
        poll = st.cfg.poll_interval_secs,
        drop = st.cfg.alert.drop_pct,
        routes = st.cfg.routes.len(),
        samples = samples_n,
        alerts = alerts_n,
        started = st.started_at.format("%Y-%m-%d %H:%M UTC"),
        uptime_h = uptime.num_hours(),
        uptime_m = uptime.num_minutes() % 60,
        routes_html = routes_html,
        alerts_html = alerts_html,
        samples_html = samples_html,
        log_html = log_html,
    ))
}

fn esc(s: &str) -> String {
    s.replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
}
