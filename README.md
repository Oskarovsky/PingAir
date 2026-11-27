# PingAir

Bot / CLI w **Rust** — strzela po cenach lotów (co N minut), buduje średnią i **alarmuje**, gdy cena wyłamie się w dół vs rolling mean. Sygnał „warto kupić”.

Działa na macOS i Linux (ten sam binarka po cross-compile albo native build na serwerze).

Repo: [github.com/Oskarovsky/PingAir](https://github.com/Oskarovsky/PingAir)

## Szybki start (Mac)

```bash
cd ~/Developer/Projects/PingAir
cp config.example.toml config.toml
# edytuj [[routes]] + alert.ntfy.topic
cargo run -- init          # jeśli jeszcze nie ma config.toml
cargo run -- once          # jedna runda (provider=mock)
cargo run -- run           # pętla co poll_interval_secs
cargo run -- stats
```

Mock ma ~8% szans na sztuczny dump — po kilku `once` zobaczysz alarm w logu (i ntfy, jeśli włączone).

## Produkcja (Amadeus)

1. Konto: [developers.amadeus.com](https://developers.amadeus.com/) → aplikacja → **Flight Offers Search**
2. W `config.toml`:

```toml
provider = "amadeus"

[amadeus]
client_id = "..."
client_secret = "..."
hostname = "test"   # potem "production"
```

## Alerty

- **ntfy.sh** — włącz `alert.ntfy`, ustaw `topic`, zainstaluj apkę ntfy i subskrybuj topic
- **Telegram** — bot od @BotFather + `chat_id`

Próg: `drop_pct` (domyślnie 12% poniżej średniej), `min_samples`, `window_hours`, `cooldown_hours`.

## Build release

```bash
# Mac
cargo build --release
# → target/release/pingair

# Linux (na serwerze gowhere)
cargo build --release
# albo z Maca (jeśli masz target):
# rustup target add x86_64-unknown-linux-gnu
# cargo build --release --target x86_64-unknown-linux-gnu
```

### systemd (szkic)

```ini
[Unit]
Description=PingAir flight price watcher
After=network-online.target

[Service]
WorkingDirectory=/opt/pingair
ExecStart=/opt/pingair/pingair --config /opt/pingair/config.toml run
Restart=always
RestartSec=30
User=pingair

[Install]
WantedBy=multi-user.target
```

## UI (prymitywny dashboard)

Przy `pingair run` i `[ui] enabled = true` wstaje strona na `bind` (domyślnie `127.0.0.1:3850`):

- statystyki tras (ostatnia / średnia / vs mean / min–max)
- ostatnie alarmy i próbki
- log procesu od startu (auto-refresh 30s)

Opcjonalnie `ui.token` — dostęp przez `?token=…` lub nagłówek `X-PingAir-Token`.

Na serwerze: nginx → `pingair.slyk.pl` (patrz `deploy/`). Zarządzanie: `pingair-ctl status|logs|edit|stats`.

## Komendy

| Komenda | Opis |
|---------|------|
| `pingair init` | config.example → config.toml |
| `pingair once` | jeden pomiar (cron) |
| `pingair run` | pętla + UI + Ctrl-C |
| `pingair stats` | ostatnie próbki z SQLite |

## Stack

Rust · Tokio · SQLite (bundled) · reqwest · clap · tracing
