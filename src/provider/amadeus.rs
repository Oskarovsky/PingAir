use crate::config::{AmadeusConfig, Route};
use crate::models::Quote;
use anyhow::{bail, Context, Result};
use chrono::Utc;
use serde::Deserialize;
use std::sync::Mutex;
use std::time::{Duration, Instant};

pub struct AmadeusProvider {
    cfg: AmadeusConfig,
    client: reqwest::Client,
    token: Mutex<Option<CachedToken>>,
}

struct CachedToken {
    access_token: String,
    expires_at: Instant,
}

impl AmadeusProvider {
    pub fn new(cfg: AmadeusConfig) -> Result<Self> {
        Ok(Self {
            cfg,
            client: reqwest::Client::builder()
                .timeout(Duration::from_secs(30))
                .build()?,
            token: Mutex::new(None),
        })
    }

    fn base_url(&self) -> &str {
        match self.cfg.hostname.as_str() {
            "production" | "prod" => "https://api.amadeus.com",
            _ => "https://test.api.amadeus.com",
        }
    }

    async fn access_token(&self) -> Result<String> {
        {
            let guard = self.token.lock().unwrap();
            if let Some(t) = guard.as_ref() {
                if t.expires_at > Instant::now() + Duration::from_secs(30) {
                    return Ok(t.access_token.clone());
                }
            }
        }

        #[derive(Deserialize)]
        struct TokenResp {
            access_token: String,
            expires_in: u64,
        }

        let url = format!("{}/v1/security/oauth2/token", self.base_url());
        let resp = self
            .client
            .post(&url)
            .form(&[
                ("grant_type", "client_credentials"),
                ("client_id", self.cfg.client_id.as_str()),
                ("client_secret", self.cfg.client_secret.as_str()),
            ])
            .send()
            .await
            .context("amadeus oauth request")?;

        if !resp.status().is_success() {
            let body = resp.text().await.unwrap_or_default();
            bail!("amadeus oauth failed: {body}");
        }
        let tok: TokenResp = resp.json().await.context("amadeus oauth json")?;
        let access = tok.access_token.clone();
        *self.token.lock().unwrap() = Some(CachedToken {
            access_token: tok.access_token,
            expires_at: Instant::now() + Duration::from_secs(tok.expires_in.saturating_sub(60)),
        });
        Ok(access)
    }

    pub async fn quote(&self, route: &Route) -> Result<Quote> {
        let token = self.access_token().await?;
        let url = format!("{}/v2/shopping/flight-offers", self.base_url());

        let mut q = vec![
            ("originLocationCode", route.origin.to_uppercase()),
            ("destinationLocationCode", route.destination.to_uppercase()),
            ("departureDate", route.depart_date.clone()),
            ("adults", route.adults.to_string()),
            ("currencyCode", route.currency.to_uppercase()),
            ("max", "5".into()),
        ];
        if !route.return_date.is_empty() {
            q.push(("returnDate", route.return_date.clone()));
        }

        let resp = self
            .client
            .get(&url)
            .bearer_auth(&token)
            .query(&q)
            .send()
            .await
            .context("amadeus flight-offers")?;

        if !resp.status().is_success() {
            let body = resp.text().await.unwrap_or_default();
            bail!("amadeus flight-offers {}: {body}", route.name);
        }

        let parsed: OffersResp = resp.json().await.context("amadeus offers json")?;
        let offers = parsed.data.unwrap_or_default();
        let best = offers
            .into_iter()
            .filter_map(|o| {
                let price: f64 = o.price.total.parse().ok()?;
                Some((price, o))
            })
            .min_by(|a, b| a.0.partial_cmp(&b.0).unwrap_or(std::cmp::Ordering::Equal));

        let Some((price, offer)) = best else {
            bail!("brak ofert Amadeus dla {}", route.name);
        };

        let carrier = offer
            .validating_airline_codes
            .first()
            .cloned()
            .unwrap_or_default();

        Ok(Quote {
            route_key: route.key(),
            route_name: route.name.clone(),
            price,
            currency: if offer.price.currency.is_empty() {
                route.currency.clone()
            } else {
                offer.price.currency
            },
            carrier,
            deep_link: format!(
                "https://www.google.com/travel/flights?q=Flights%20{}%20to%20{}%20on%20{}",
                route.origin, route.destination, route.depart_date
            ),
            fetched_at: Utc::now(),
        })
    }
}

#[derive(Deserialize)]
struct OffersResp {
    data: Option<Vec<Offer>>,
}

#[derive(Deserialize)]
struct Offer {
    price: OfferPrice,
    #[serde(default)]
    validating_airline_codes: Vec<String>,
}

#[derive(Deserialize)]
struct OfferPrice {
    total: String,
    #[serde(default)]
    currency: String,
}
