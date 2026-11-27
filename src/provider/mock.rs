use crate::config::Route;
use crate::models::Quote;
use anyhow::Result;
use chrono::Utc;
use rand::Rng;
use std::collections::HashMap;
use std::sync::Mutex;

/// Deterministyczna „gielda” per trasa — wokół baseline z rzadkim dumpem w dół.
pub struct MockProvider {
    baselines: Mutex<HashMap<String, f64>>,
}

impl MockProvider {
    pub fn new() -> Self {
        Self {
            baselines: Mutex::new(HashMap::new()),
        }
    }

    pub async fn quote(&self, route: &Route) -> Result<Quote> {
        let key = route.key();
        let mut map = self.baselines.lock().unwrap();
        let base = *map.entry(key.clone()).or_insert_with(|| {
            let dist = (route.origin.len() + route.destination.len()) as f64;
            280.0 + dist * 37.0
        });
        drop(map);

        let mut rng = rand::thread_rng();
        // ~8% szans na dump −15…−28% (żeby dało się zobaczyć alarm w testach)
        let price = if rng.gen_bool(0.08) {
            base * rng.gen_range(0.72..0.85)
        } else {
            base * rng.gen_range(0.94..1.06)
        };

        Ok(Quote {
            route_key: key,
            route_name: route.name.clone(),
            price: (price * 100.0).round() / 100.0,
            currency: route.currency.clone(),
            carrier: "MOCK".into(),
            deep_link: format!(
                "https://www.google.com/travel/flights?q=Flights%20{}%20to%20{}%20on%20{}",
                route.origin, route.destination, route.depart_date
            ),
            fetched_at: Utc::now(),
        })
    }
}
