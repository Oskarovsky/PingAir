use chrono::{DateTime, Utc};

#[derive(Debug, Clone)]
pub struct Quote {
    pub route_key: String,
    pub route_name: String,
    pub price: f64,
    pub currency: String,
    pub carrier: String,
    pub deep_link: String,
    pub fetched_at: DateTime<Utc>,
}

#[derive(Debug, Clone)]
pub struct RouteStats {
    pub samples: usize,
    pub mean: f64,
    pub min: f64,
    pub max: f64,
    #[allow(dead_code)]
    pub latest: f64,
}

#[derive(Debug, Clone)]
pub struct Breakout {
    pub quote: Quote,
    pub stats: RouteStats,
    pub drop_pct_vs_mean: f64,
}
