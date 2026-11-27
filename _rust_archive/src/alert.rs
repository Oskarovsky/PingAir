use crate::config::AlertConfig;
use crate::models::Breakout;
use anyhow::{Context, Result};
use tracing::info;

pub async fn notify(cfg: &AlertConfig, breakout: &Breakout) -> Result<()> {
    let msg = format_message(breakout);
    info!(target: "pingair.alert", "{msg}");

    if cfg.ntfy.enabled && !cfg.ntfy.topic.is_empty() {
        send_ntfy(&cfg.ntfy.server, &cfg.ntfy.topic, &msg).await?;
    }
    if cfg.telegram.enabled
        && !cfg.telegram.bot_token.is_empty()
        && !cfg.telegram.chat_id.is_empty()
    {
        send_telegram(&cfg.telegram.bot_token, &cfg.telegram.chat_id, &msg).await?;
    }
    Ok(())
}

fn format_message(b: &Breakout) -> String {
    format!(
        "✈️ PingAir ALARM — {}\n\
         Cena: {:.2} {} (przewoźnik: {})\n\
         Średnia: {:.2} {} (n={})\n\
         Dump: −{:.1}% vs średnia (min {:.2} / max {:.2})\n\
         {}\n\
         Sygnał: warto sprawdzić zakup.",
        b.quote.route_name,
        b.quote.price,
        b.quote.currency,
        b.quote.carrier,
        b.stats.mean,
        b.quote.currency,
        b.stats.samples,
        b.drop_pct_vs_mean,
        b.stats.min,
        b.stats.max,
        b.quote.deep_link
    )
}

async fn send_ntfy(server: &str, topic: &str, message: &str) -> Result<()> {
    let base = server.trim_end_matches('/');
    let url = format!("{base}/{topic}");
    let client = reqwest::Client::new();
    let resp = client
        .post(&url)
        .header("Title", "PingAir — dump ceny lotu")
        .header("Priority", "high")
        .header("Tags", "airplane,warning")
        .body(message.to_string())
        .send()
        .await
        .context("ntfy request")?;
    if !resp.status().is_success() {
        let body = resp.text().await.unwrap_or_default();
        anyhow::bail!("ntfy error: {body}");
    }
    Ok(())
}

async fn send_telegram(token: &str, chat_id: &str, message: &str) -> Result<()> {
    let url = format!("https://api.telegram.org/bot{token}/sendMessage");
    let client = reqwest::Client::new();
    let resp = client
        .post(&url)
        .json(&serde_json::json!({
            "chat_id": chat_id,
            "text": message,
            "disable_web_page_preview": false,
        }))
        .send()
        .await
        .context("telegram request")?;
    if !resp.status().is_success() {
        let body = resp.text().await.unwrap_or_default();
        anyhow::bail!("telegram error: {body}");
    }
    Ok(())
}
