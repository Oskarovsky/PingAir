use crate::models::Quote;
use anyhow::{Context, Result};
use chrono::{DateTime, Duration, Utc};
use rusqlite::{params, Connection};
use std::path::Path;

pub struct Db {
    conn: Connection,
}

impl Db {
    pub fn open(path: &Path) -> Result<Self> {
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)
                .with_context(|| format!("mkdir {}", parent.display()))?;
        }
        let conn = Connection::open(path)
            .with_context(|| format!("open sqlite {}", path.display()))?;
        conn.execute_batch(
            "
            PRAGMA journal_mode=WAL;
            PRAGMA busy_timeout=5000;
            CREATE TABLE IF NOT EXISTS samples (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                route_key TEXT NOT NULL,
                route_name TEXT NOT NULL,
                price REAL NOT NULL,
                currency TEXT NOT NULL,
                carrier TEXT NOT NULL DEFAULT '',
                deep_link TEXT NOT NULL DEFAULT '',
                fetched_at TEXT NOT NULL
            );
            CREATE INDEX IF NOT EXISTS idx_samples_route_time
                ON samples(route_key, fetched_at);
            CREATE TABLE IF NOT EXISTS alerts (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                route_key TEXT NOT NULL,
                price REAL NOT NULL,
                mean REAL NOT NULL,
                drop_pct REAL NOT NULL,
                message TEXT NOT NULL,
                created_at TEXT NOT NULL
            );
            CREATE INDEX IF NOT EXISTS idx_alerts_route_time
                ON alerts(route_key, created_at);
            ",
        )?;
        Ok(Self { conn })
    }

    pub fn insert_sample(&self, q: &Quote) -> Result<()> {
        self.conn.execute(
            "INSERT INTO samples (route_key, route_name, price, currency, carrier, deep_link, fetched_at)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)",
            params![
                q.route_key,
                q.route_name,
                q.price,
                q.currency,
                q.carrier,
                q.deep_link,
                q.fetched_at.to_rfc3339(),
            ],
        )?;
        Ok(())
    }

    pub fn prices_for_route(
        &self,
        route_key: &str,
        window_hours: u64,
    ) -> Result<Vec<f64>> {
        let mut sql = String::from(
            "SELECT price FROM samples WHERE route_key = ?1",
        );
        let mut params_vec: Vec<Box<dyn rusqlite::types::ToSql>> = vec![Box::new(route_key.to_string())];
        if window_hours > 0 {
            let since = Utc::now() - Duration::hours(window_hours as i64);
            sql.push_str(" AND fetched_at >= ?2");
            params_vec.push(Box::new(since.to_rfc3339()));
        }
        sql.push_str(" ORDER BY fetched_at ASC");

        let mut stmt = self.conn.prepare(&sql)?;
        let param_refs: Vec<&dyn rusqlite::types::ToSql> =
            params_vec.iter().map(|p| p.as_ref()).collect();
        let rows = stmt.query_map(param_refs.as_slice(), |row| row.get::<_, f64>(0))?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r?);
        }
        Ok(out)
    }

    pub fn last_alert_at(&self, route_key: &str) -> Result<Option<DateTime<Utc>>> {
        let mut stmt = self.conn.prepare(
            "SELECT created_at FROM alerts WHERE route_key = ?1 ORDER BY id DESC LIMIT 1",
        )?;
        let mut rows = stmt.query(params![route_key])?;
        if let Some(row) = rows.next()? {
            let s: String = row.get(0)?;
            let dt = DateTime::parse_from_rfc3339(&s)
                .map(|d| d.with_timezone(&Utc))
                .ok();
            Ok(dt)
        } else {
            Ok(None)
        }
    }

    pub fn insert_alert(
        &self,
        route_key: &str,
        price: f64,
        mean: f64,
        drop_pct: f64,
        message: &str,
    ) -> Result<()> {
        self.conn.execute(
            "INSERT INTO alerts (route_key, price, mean, drop_pct, message, created_at)
             VALUES (?1, ?2, ?3, ?4, ?5, ?6)",
            params![
                route_key,
                price,
                mean,
                drop_pct,
                message,
                Utc::now().to_rfc3339()
            ],
        )?;
        Ok(())
    }

    pub fn recent_samples(&self, limit: usize) -> Result<Vec<(String, f64, String, String)>> {
        let mut stmt = self.conn.prepare(
            "SELECT route_name, price, currency, fetched_at FROM samples
             ORDER BY id DESC LIMIT ?1",
        )?;
        let rows = stmt.query_map(params![limit as i64], |row| {
            Ok((
                row.get::<_, String>(0)?,
                row.get::<_, f64>(1)?,
                row.get::<_, String>(2)?,
                row.get::<_, String>(3)?,
            ))
        })?;
        let mut out = Vec::new();
        for r in rows {
            out.push(r?);
        }
        Ok(out)
    }
}
