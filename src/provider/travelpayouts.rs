use crate::config::{Route, TravelpayoutsConfig};
use crate::models::Quote;
use anyhow::{bail, Context, Result};
use chrono::Utc;
use serde::Deserialize;

pub struct TravelpayoutsProvider {
    cfg: TravelpayoutsConfig,
    client: reqwest::Client,
}

impl TravelpayoutsProvider {
    pub fn new(cfg: TravelpayoutsConfig) -> Result<Self> {
        Ok(Self {
            cfg,
            client: reqwest::Client::builder()
                .timeout(std::time::Duration::from_secs(30))
                .gzip(true)
                .build()?,
        })
    }

    pub async fn quote(&self, route: &Route) -> Result<Quote> {
        let currency = route.currency.to_lowercase();
        let one_way = route.return_date.is_empty();

        let mut req = self
            .client
            .get("https://api.travelpayouts.com/aviasales/v3/prices_for_dates")
            .header("Accept-Encoding", "gzip, deflate")
            .query(&[
                ("origin", route.origin.to_uppercase()),
                ("destination", route.destination.to_uppercase()),
                ("departure_at", route.depart_date.clone()),
                ("currency", currency.clone()),
                ("sorting", "price".into()),
                ("limit", "10".into()),
                ("page", "1".into()),
                ("one_way", if one_way { "true" } else { "false" }.into()),
                ("direct", "false".into()),
                ("unique", "false".into()),
                ("token", self.cfg.token.clone()),
            ]);

        if !one_way {
            req = req.query(&[("return_at", route.return_date.as_str())]);
        }
        if !self.cfg.market.is_empty() {
            req = req.query(&[("market", self.cfg.market.as_str())]);
        }

        let resp = req.send().await.context("travelpayouts request")?;
        let status = resp.status();
        let body = resp.text().await.context("travelpayouts body")?;
        if !status.is_success() {
            bail!("travelpayouts HTTP {status}: {body}");
        }

        let parsed: PricesResp = serde_json::from_str(&body)
            .with_context(|| format!("travelpayouts json: {body}"))?;
        if !parsed.success {
            bail!(
                "travelpayouts error: {}",
                parsed.error.unwrap_or_else(|| "unknown".into())
            );
        }

        let best = parsed
            .data
            .into_iter()
            .min_by(|a, b| a.price.partial_cmp(&b.price).unwrap_or(std::cmp::Ordering::Equal))
            .ok_or_else(|| anyhow::anyhow!("brak ofert Travelpayouts dla {}", route.name))?;

        let link = best.link.unwrap_or_default();
        let deep_link = if link.starts_with("http") {
            link
        } else if link.is_empty() {
            format!(
                "https://www.aviasales.pl/search/{}{}{}",
                route.origin.to_uppercase(),
                route.depart_date.replace('-', ""),
                route.destination.to_uppercase()
            )
        } else {
            format!("https://www.aviasales.pl{link}")
        };

        Ok(Quote {
            route_key: route.key(),
            route_name: route.name.clone(),
            price: best.price,
            currency: best
                .currency
                .filter(|c| !c.is_empty())
                .unwrap_or_else(|| route.currency.to_uppercase()),
            carrier: best.airline.unwrap_or_default(),
            deep_link,
            fetched_at: Utc::now(),
        })
    }
}

#[derive(Deserialize)]
struct PricesResp {
    success: bool,
    #[serde(default)]
    data: Vec<Offer>,
    #[serde(default)]
    error: Option<String>,
}

#[derive(Deserialize)]
struct Offer {
    price: f64,
    #[serde(default)]
    airline: Option<String>,
    #[serde(default)]
    link: Option<String>,
    #[serde(default)]
    currency: Option<String>,
}
