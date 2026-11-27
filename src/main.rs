mod alert;
mod config;
mod db;
mod models;
mod provider;
mod runner;
mod stats;

use anyhow::{Context, Result};
use clap::{Parser, Subcommand};
use std::path::PathBuf;
use tracing_subscriber::EnvFilter;

#[derive(Parser, Debug)]
#[command(name = "pingair", about = "Flight price watcher — dump vs średnia = alarm")]
struct Cli {
    /// Ścieżka do config.toml
    #[arg(short, long, default_value = "config.toml", global = true)]
    config: PathBuf,

    #[command(subcommand)]
    cmd: Commands,
}

#[derive(Subcommand, Debug)]
enum Commands {
    /// Jedna runda pomiarów (cron-friendly)
    Once,
    /// Pętla: strzał co poll_interval_secs
    Run,
    /// Ostatnie próbki z bazy
    Stats {
        #[arg(long, default_value_t = 20)]
        limit: usize,
    },
    /// Skopiuj config.example.toml → config.toml jeśli brak
    Init,
}

#[tokio::main]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_env_filter(EnvFilter::from_default_env().add_directive("pingair=info".parse()?))
        .init();

    let cli = Cli::parse();

    match cli.cmd {
        Commands::Init => {
            let dest = &cli.config;
            if dest.exists() {
                println!("już jest: {}", dest.display());
                return Ok(());
            }
            let example = PathBuf::from("config.example.toml");
            std::fs::copy(&example, dest)
                .with_context(|| format!("copy {} → {}", example.display(), dest.display()))?;
            println!("utworzono {} — edytuj trasy i alerty", dest.display());
            Ok(())
        }
        Commands::Stats { limit } => {
            let cfg = config::Config::load(&cli.config)?;
            let database = db::Db::open(&cfg.db_path)?;
            for (name, price, cur, at) in database.recent_samples(limit)? {
                println!("{at}  {name:<28}  {price:>8.2} {cur}");
            }
            Ok(())
        }
        Commands::Once => {
            let cfg = config::Config::load(&cli.config)?;
            let database = db::Db::open(&cfg.db_path)?;
            let prov = provider::Provider::from_config(&cfg)?;
            runner::poll_once(&cfg, &database, &prov).await
        }
        Commands::Run => {
            let cfg = config::Config::load(&cli.config)?;
            let database = db::Db::open(&cfg.db_path)?;
            let prov = provider::Provider::from_config(&cfg)?;
            runner::run_loop(cfg, database, prov).await
        }
    }
}
