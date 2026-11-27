mod amadeus;
mod mock;
mod travelpayouts;

use crate::config::{Config, Route};
use crate::models::Quote;
use anyhow::Result;

pub enum Provider {
    Mock(mock::MockProvider),
    Amadeus(amadeus::AmadeusProvider),
    Travelpayouts(travelpayouts::TravelpayoutsProvider),
}

impl Provider {
    pub fn from_config(cfg: &Config) -> Result<Self> {
        match cfg.provider.as_str() {
            "mock" => Ok(Self::Mock(mock::MockProvider::new())),
            "amadeus" => Ok(Self::Amadeus(amadeus::AmadeusProvider::new(
                cfg.amadeus.clone(),
            )?)),
            "travelpayouts" | "aviasales" => Ok(Self::Travelpayouts(
                travelpayouts::TravelpayoutsProvider::new(cfg.travelpayouts.clone())?,
            )),
            other => anyhow::bail!("nieznany provider: {other}"),
        }
    }

    pub async fn quote(&self, route: &Route) -> Result<Quote> {
        match self {
            Self::Mock(p) => p.quote(route).await,
            Self::Amadeus(p) => p.quote(route).await,
            Self::Travelpayouts(p) => p.quote(route).await,
        }
    }
}
