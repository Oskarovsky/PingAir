use crate::models::{Breakout, Quote, RouteStats};

pub fn summarize(prices: &[f64], latest: f64) -> Option<RouteStats> {
    if prices.is_empty() {
        return None;
    }
    let n = prices.len();
    let sum: f64 = prices.iter().sum();
    let mean = sum / n as f64;
    let min = prices.iter().cloned().fold(f64::INFINITY, f64::min);
    let max = prices.iter().cloned().fold(f64::NEG_INFINITY, f64::max);
    Some(RouteStats {
        samples: n,
        mean,
        min,
        max,
        latest,
    })
}

/// Breakout w dół: latest ≤ mean * (1 - drop_pct/100).
pub fn detect_breakout(
    quote: &Quote,
    prices_including_latest: &[f64],
    drop_pct: f64,
    min_samples: usize,
) -> Option<Breakout> {
    if prices_including_latest.len() < min_samples {
        return None;
    }
    // Średnia z historii BEZ bieżącej próbki — żeby nowy dump nie zawyżał średniej.
    let hist = &prices_including_latest[..prices_including_latest.len() - 1];
    if hist.len() < min_samples.saturating_sub(1).max(1) {
        return None;
    }
    let stats = summarize(hist, quote.price)?;
    if stats.mean <= 0.0 {
        return None;
    }
    let threshold = stats.mean * (1.0 - drop_pct / 100.0);
    if quote.price > threshold {
        return None;
    }
    let drop_vs_mean = (stats.mean - quote.price) / stats.mean * 100.0;
    Some(Breakout {
        quote: quote.clone(),
        stats,
        drop_pct_vs_mean: drop_vs_mean,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use chrono::Utc;

    fn q(price: f64) -> Quote {
        Quote {
            route_key: "WAW|BCN|2026-04-10||PLN".into(),
            route_name: "test".into(),
            price,
            currency: "PLN".into(),
            carrier: "LO".into(),
            deep_link: String::new(),
            fetched_at: Utc::now(),
        }
    }

    #[test]
    fn breakout_when_clearly_below_mean() {
        let hist: Vec<f64> = (0..10).map(|_| 500.0).collect();
        let mut all = hist;
        all.push(400.0); // -20%
        let b = detect_breakout(&q(400.0), &all, 12.0, 8).expect("breakout");
        assert!(b.drop_pct_vs_mean > 15.0);
    }

    #[test]
    fn no_breakout_near_mean() {
        let mut all: Vec<f64> = (0..10).map(|_| 500.0).collect();
        all.push(480.0); // -4%
        assert!(detect_breakout(&q(480.0), &all, 12.0, 8).is_none());
    }
}
