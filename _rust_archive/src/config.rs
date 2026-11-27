use anyhow::{bail, Context, Result};
use serde::Deserialize;
use std::fs;
use std::path::{Path, PathBuf};

#[derive(Debug, Clone, Deserialize)]
pub struct Config {
    #[serde(default = "default_poll")]
    pub poll_interval_secs: u64,
    #[serde(default = "default_db")]
    pub db_path: PathBuf,
    #[serde(default = "default_provider")]
    pub provider: String,
    #[serde(default)]
    pub amadeus: AmadeusConfig,
    #[serde(default)]
    pub travelpayouts: TravelpayoutsConfig,
    #[serde(default)]
    pub serpapi: SerpapiConfig,
    pub alert: AlertConfig,
    #[serde(default)]
    pub ui: UiConfig,
    pub routes: Vec<Route>,
}

#[derive(Debug, Clone, Deserialize)]
pub struct UiConfig {
    #[serde(default = "default_ui_enabled")]
    pub enabled: bool,
    /// np. 127.0.0.1:3850 (za nginx) albo 0.0.0.0:3850
    #[serde(default = "default_ui_bind")]
    pub bind: String,
    /// Opcjonalny token (?token=… albo nagłówek X-PingAir-Token)
    #[serde(default)]
    pub token: String,
}

fn default_ui_enabled() -> bool {
    true
}
fn default_ui_bind() -> String {
    "127.0.0.1:3850".into()
}

impl Default for UiConfig {
    fn default() -> Self {
        Self {
            enabled: true,
            bind: default_ui_bind(),
            token: String::new(),
        }
    }
}

fn default_poll() -> u64 {
    900
}
fn default_db() -> PathBuf {
    PathBuf::from("data/pingair.db")
}
fn default_provider() -> String {
    "mock".into()
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct AmadeusConfig {
    #[serde(default)]
    pub client_id: String,
    #[serde(default)]
    pub client_secret: String,
    /// "test" | "production"
    #[serde(default = "default_amadeus_host")]
    pub hostname: String,
}

fn default_amadeus_host() -> String {
    "test".into()
}

/// Aviasales Data API via Travelpayouts (darmowy token po rejestracji).
/// Amadeus Self-Service został wyłączony w lipcu 2026.
#[derive(Debug, Clone, Deserialize, Default)]
pub struct TravelpayoutsConfig {
    #[serde(default)]
    pub token: String,
    /// np. "pl" — rynek cache’u Aviasales (domyślnie z origin)
    #[serde(default = "default_tp_market")]
    pub market: String,
}

fn default_tp_market() -> String {
    "pl".into()
}

/// Google Flights przez SerpApi (brak oficjalnego API Google).
#[derive(Debug, Clone, Deserialize, Default)]
pub struct SerpapiConfig {
    #[serde(default)]
    pub api_key: String,
    #[serde(default = "default_serp_gl")]
    pub gl: String,
    #[serde(default = "default_serp_hl")]
    pub hl: String,
}

fn default_serp_gl() -> String {
    "pl".into()
}
fn default_serp_hl() -> String {
    "pl".into()
}

#[derive(Debug, Clone, Deserialize)]
pub struct AlertConfig {
    #[serde(default = "default_drop")]
    pub drop_pct: f64,
    #[serde(default = "default_min_samples")]
    pub min_samples: usize,
    #[serde(default = "default_window")]
    pub window_hours: u64,
    #[serde(default = "default_cooldown")]
    pub cooldown_hours: u64,
    #[serde(default)]
    pub ntfy: NtfyConfig,
    #[serde(default)]
    pub telegram: TelegramConfig,
}

fn default_drop() -> f64 {
    12.0
}
fn default_min_samples() -> usize {
    8
}
fn default_window() -> u64 {
    168
}
fn default_cooldown() -> u64 {
    12
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct NtfyConfig {
    #[serde(default)]
    pub enabled: bool,
    #[serde(default = "default_ntfy_server")]
    pub server: String,
    #[serde(default)]
    pub topic: String,
}

fn default_ntfy_server() -> String {
    "https://ntfy.sh".into()
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct TelegramConfig {
    #[serde(default)]
    pub enabled: bool,
    #[serde(default)]
    pub bot_token: String,
    #[serde(default)]
    pub chat_id: String,
}

#[derive(Debug, Clone, Deserialize)]
pub struct Route {
    pub name: String,
    pub origin: String,
    pub destination: String,
    pub depart_date: String,
    #[serde(default)]
    pub return_date: String,
    #[serde(default = "default_adults")]
    pub adults: u32,
    #[serde(default = "default_currency")]
    pub currency: String,
}

fn default_adults() -> u32 {
    1
}
fn default_currency() -> String {
    "PLN".into()
}

impl Route {
    pub fn key(&self) -> String {
        format!(
            "{}|{}|{}|{}|{}",
            self.origin.to_uppercase(),
            self.destination.to_uppercase(),
            self.depart_date,
            self.return_date,
            self.currency.to_uppercase()
        )
    }
}

impl Config {
    pub fn load(path: &Path) -> Result<Self> {
        let raw = fs::read_to_string(path)
            .with_context(|| format!("nie mogę przeczytać configu: {}", path.display()))?;
        let cfg: Config = toml::from_str(&raw).context("zły TOML w configu")?;
        cfg.validate()?;
        Ok(cfg)
    }

    fn validate(&self) -> Result<()> {
        if self.routes.is_empty() {
            bail!("brak [[routes]] w configu");
        }
        if self.poll_interval_secs < 60 {
            bail!("poll_interval_secs minimum 60");
        }
        match self.provider.as_str() {
            "mock" | "amadeus" | "travelpayouts" | "aviasales" | "serpapi" | "google_flights"
            | "google" => {}
            other => {
                bail!("nieznany provider: {other} (mock|serpapi|travelpayouts|amadeus)")
            }
        }
        if self.provider == "amadeus"
            && (self.amadeus.client_id.is_empty() || self.amadeus.client_secret.is_empty())
        {
            bail!("provider=amadeus wymaga amadeus.client_id i client_secret (Self-Service martwy od 2026-07)");
        }
        if matches!(self.provider.as_str(), "travelpayouts" | "aviasales")
            && self.travelpayouts.token.is_empty()
        {
            bail!("provider=travelpayouts wymaga travelpayouts.token (Travelpayouts → Profile → API token)");
        }
        if matches!(
            self.provider.as_str(),
            "serpapi" | "google_flights" | "google"
        ) && self.serpapi.api_key.is_empty()
        {
            bail!("provider=serpapi wymaga serpapi.api_key (serpapi.com → API Key, Google Flights)");
        }
        Ok(())
    }
}
