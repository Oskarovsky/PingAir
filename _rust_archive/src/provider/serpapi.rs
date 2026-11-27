use crate::config::{Route, SerpapiConfig};
use crate::models::Quote;
use anyhow::{bail, Context, Result};
use chrono::Utc;
use serde::Deserialize;
use serde_json::Value;

/// Google Flights via SerpApi (`engine=google_flights`).
/// Oficjalnego API Google nie ma — to ustrukturyzowany scrape.
pub struct SerpapiProvider {
    cfg: SerpapiConfig,
    client: reqwest::Client,
}

impl SerpapiProvider {
    pub fn new(cfg: SerpapiConfig) -> Result<Self> {
        Ok(Self {
            cfg,
            client: reqwest::Client::builder()
                .timeout(std::time::Duration::from_secs(60))
                .gzip(true)
                .build()?,
        })
    }

    pub async fn quote(&self, route: &Route) -> Result<Quote> {
        let one_way = route.return_date.is_empty();
        let trip_type = if one_way { "2" } else { "1" };

        let mut q: Vec<(&str, String)> = vec![
            ("engine", "google_flights".into()),
            ("api_key", self.cfg.api_key.clone()),
            ("departure_id", route.origin.to_uppercase()),
            ("arrival_id", route.destination.to_uppercase()),
            ("outbound_date", route.depart_date.clone()),
            ("currency", route.currency.to_uppercase()),
            ("hl", self.cfg.hl.clone()),
            ("gl", self.cfg.gl.clone()),
            ("type", trip_type.into()),
            ("sort_by", "2".into()), // price
            ("adults", route.adults.to_string()),
        ];
        if !one_way {
            q.push(("return_date", route.return_date.clone()));
        }

        let resp = self
            .client
            .get("https://serpapi.com/search.json")
            .query(&q)
            .send()
            .await
            .context("serpapi request")?;

        let status = resp.status();
        let body = resp.text().await.context("serpapi body")?;
        if !status.is_success() {
            bail!("serpapi HTTP {status}: {body}");
        }

        let parsed: FlightsResp =
            serde_json::from_str(&body).with_context(|| format!("serpapi json: {body}"))?;

        if let Some(err) = parsed.error.as_ref().filter(|e| !e.is_empty()) {
            bail!("serpapi error: {err}");
        }

        let (price, carrier) = pick_best(&parsed)
            .ok_or_else(|| anyhow::anyhow!("brak ofert Google Flights dla {}", route.name))?;

        let deep_link = format!(
            "https://www.google.com/travel/flights?hl={}&curr={}&q=Flights%20{}%20to%20{}%20on%20{}",
            self.cfg.hl,
            route.currency.to_uppercase(),
            route.origin.to_uppercase(),
            route.destination.to_uppercase(),
            route.depart_date
        );

        Ok(Quote {
            route_key: route.key(),
            route_name: route.name.clone(),
            price,
            currency: route.currency.to_uppercase(),
            carrier,
            deep_link,
            fetched_at: Utc::now(),
        })
    }
}

fn pick_best(parsed: &FlightsResp) -> Option<(f64, String)> {
    if let Some(p) = parsed
        .price_insights
        .as_ref()
        .and_then(|i| i.lowest_price)
        .filter(|p| *p > 0.0)
    {
        let carrier = first_carrier(parsed).unwrap_or_default();
        return Some((p, carrier));
    }

    let mut best: Option<(f64, String)> = None;
    for list in [&parsed.best_flights, &parsed.other_flights] {
        for item in list {
            let Some(price) = price_from_value(&item.price) else {
                continue;
            };
            let carrier = item
                .flights
                .first()
                .and_then(|f| f.airline.clone())
                .unwrap_or_default();
            match &best {
                Some((b, _)) if price >= *b => {}
                _ => best = Some((price, carrier)),
            }
        }
    }
    best
}

fn first_carrier(parsed: &FlightsResp) -> Option<String> {
    parsed
        .best_flights
        .iter()
        .chain(parsed.other_flights.iter())
        .find_map(|item| item.flights.first().and_then(|f| f.airline.clone()))
}

fn price_from_value(v: &Value) -> Option<f64> {
    match v {
        Value::Number(n) => n.as_f64(),
        Value::String(s) => s.replace(',', "").parse().ok(),
        _ => None,
    }
}

#[derive(Deserialize)]
struct FlightsResp {
    #[serde(default)]
    error: Option<String>,
    #[serde(default)]
    price_insights: Option<PriceInsights>,
    #[serde(default)]
    best_flights: Vec<FlightOption>,
    #[serde(default)]
    other_flights: Vec<FlightOption>,
}

#[derive(Deserialize)]
struct PriceInsights {
    #[serde(default)]
    lowest_price: Option<f64>,
}

#[derive(Deserialize)]
struct FlightOption {
    #[serde(default)]
    price: Value,
    #[serde(default)]
    flights: Vec<FlightLeg>,
}

#[derive(Deserialize)]
struct FlightLeg {
    #[serde(default)]
    airline: Option<String>,
}
