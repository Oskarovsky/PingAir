use crate::alert;
use crate::config::Config;
use crate::db::Db;
use crate::provider::Provider;
use crate::stats;
use anyhow::Result;
use chrono::{Duration, Utc};
use tracing::{error, info, warn};

pub async fn poll_once(cfg: &Config, db: &Db, provider: &Provider) -> Result<()> {
    for route in &cfg.routes {
        match provider.quote(route).await {
            Ok(quote) => {
                db.insert_sample(&quote)?;
                info!(
                    route = %quote.route_name,
                    price = quote.price,
                    currency = %quote.currency,
                    carrier = %quote.carrier,
                    "sample"
                );

                let prices = db.prices_for_route(&quote.route_key, cfg.alert.window_hours)?;
                if let Some(breakout) = stats::detect_breakout(
                    &quote,
                    &prices,
                    cfg.alert.drop_pct,
                    cfg.alert.min_samples,
                ) {
                    if in_cooldown(db, &quote.route_key, cfg.alert.cooldown_hours)? {
                        warn!(
                            route = %quote.route_name,
                            "breakout, ale cooldown — pomijam alert"
                        );
                        continue;
                    }
                    let msg = format!(
                        "{} @ {:.2} {} (−{:.1}% vs mean {:.2})",
                        breakout.quote.route_name,
                        breakout.quote.price,
                        breakout.quote.currency,
                        breakout.drop_pct_vs_mean,
                        breakout.stats.mean
                    );
                    db.insert_alert(
                        &quote.route_key,
                        quote.price,
                        breakout.stats.mean,
                        breakout.drop_pct_vs_mean,
                        &msg,
                    )?;
                    if let Err(e) = alert::notify(&cfg.alert, &breakout).await {
                        error!(error = %e, "alert failed");
                    }
                }
            }
            Err(e) => error!(route = %route.name, error = %e, "quote failed"),
        }
    }
    Ok(())
}

fn in_cooldown(db: &Db, route_key: &str, cooldown_hours: u64) -> Result<bool> {
    if cooldown_hours == 0 {
        return Ok(false);
    }
    match db.last_alert_at(route_key)? {
        Some(at) => Ok(Utc::now() < at + Duration::hours(cooldown_hours as i64)),
        None => Ok(false),
    }
}

pub async fn run_loop(cfg: Config, db: Db, provider: Provider) -> Result<()> {
    info!(
        interval_secs = cfg.poll_interval_secs,
        routes = cfg.routes.len(),
        provider = %cfg.provider,
        "PingAir start"
    );
    loop {
        if let Err(e) = poll_once(&cfg, &db, &provider).await {
            error!(error = %e, "poll cycle failed");
        }
        tokio::select! {
            _ = tokio::time::sleep(std::time::Duration::from_secs(cfg.poll_interval_secs)) => {}
            _ = tokio::signal::ctrl_c() => {
                info!("Ctrl-C — stop");
                break;
            }
        }
    }
    Ok(())
}
