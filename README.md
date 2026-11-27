# PingAir

Bot w **Go** — strzela po cenach lotów (co N minut), buduje średnią i **alarmuje**, gdy cena wyłamie się w dół vs rolling mean.

Repo: [github.com/Oskarovsky/PingAir](https://github.com/Oskarovsky/PingAir)

> Przepisane z Rusta na Go — deploy to `GOOS=linux go build` (sekundy), nie LTO na VPS.

## Szybki start (Mac)

```bash
cd ~/Developer/Projects/PingAir
cp config.example.toml config.toml
# edytuj [[routes]] + travelpayouts.token
go run ./cmd/pingair once
go run ./cmd/pingair run
```

## Produkcja — źródła cen (darmowe)

Amadeus Self-Service padł w lipcu 2026. Google nie ma oficjalnego Flights API.

### Travelpayouts / Aviasales (zalecane, 100% free)

1. Konto: https://app.travelpayouts.com/auth/sign_up/
2. Token: https://app.travelpayouts.com/profile/api-token

```toml
provider = "travelpayouts"

[travelpayouts]
token = "…"
market = "pl"
```

Najpierw `prices_for_dates` (dokładna data), jak cache pusty → `v1/prices/cheap` (najtańszy na trasie).

### SerpApi = Google Flights (live, 250 free/mies.)

```toml
provider = "serpapi"
poll_interval_secs = 21600

[serpapi]
api_key = "…"
gl = "pl"
hl = "pl"
```

## Build / deploy

```bash
# lokalnie
go build -o pingair ./cmd/pingair

# VPS (z Maca, bez kompilacji na serwerze):
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags='-s -w' -o pingair-linux ./cmd/pingair
scp pingair-linux root@VPS:/opt/pingair/pingair
ssh root@VPS systemctl restart pingair
```

## UI

`https://pingair.slyk.pl/?token=…` (nginx → `127.0.0.1:3850`)

## Alerty

- **ntfy.sh** — `alert.ntfy`
- **Telegram** — bot + `chat_id`
