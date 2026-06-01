# csair

A CLI to search China Southern Airlines (CZ) flights and show **seats available per
cabin** for a route and date.

## Install

Requires **Go 1.26+** and **Google Chrome / Chromium** (used once to mint the
anti-bot token; see [Auth](#auth--token)).

```sh
# install the binary to $(go env GOPATH)/bin
go install github.com/kkweon/csair@latest

# …or build from source
git clone https://github.com/kkweon/csair && cd csair
go build -o csair .
```

Ensure `$(go env GOPATH)/bin` is on your `PATH`.

## Usage

```sh
# 1) one-time: mint the anti-bot token (launches headless Chrome, caches it)
csair auth

# 2) search a route + date
csair search SFO CAN 2026-06-14
```

```
SFO → CAN  ·  2026-06-14  ·  1 adult(s)

FLIGHT        ROUTING      DEP    ARR    DUR     STOPS   SEATS                     FROM
CZ658         SFO–CAN      00:35  06:20  14h45m  direct  Business 8 · Economy 9+   USD 1284
AS1602+CZ328  SFO–LAX–CAN  10:15  06:05  28h50m  1-stop  Economy 9+                USD 1662
…
```

The cabin number is the **true seats available** (the highest fare tier; `9+` means
"9 or more") — see [Availability semantics](#availability-semantics).

Route and date are accepted positionally **or** via flags:

```sh
csair search --from SFO --to CAN --date 2026-06-14
```

### search flags

| Flag | Default | Meaning |
|---|---|---|
| `-a, --adults` | `1` | adult passengers |
| `-c, --children` | `0` | children (2–11) |
| `-i, --infants` | `0` | lap infants (<2) |
| `--cabin` | `all` | `economy` \| `premium` \| `business` \| `first` \| `all` |
| `--carrier` | — | only this marketing carrier, e.g. `CZ` |
| `--direct` | `false` | nonstop only |
| `--min-seats` | `0` | only cabins with ≥ N seats |
| `--sort` | `price` | `price` \| `duration` \| `departure` |

### Output formats

Table on a terminal, **JSON when piped/redirected**:

```sh
csair search SFO CAN 2026-06-14 --cabin business --json | jq
```

Force a format with `--json`, `--csv`, or `--table`. JSON/CSV include the per-RBD
fare-tier breakdown under each cabin.

### Auth / token

The `/ita` engine is gated only by the Aliyun `acw_sc__v2` cookie. `csair auth`
drives Chrome to mint it and caches it at `~/.config/csair/cookies.json`.

```sh
csair auth            # bootstrap + cache (headless)
csair auth --headed   # show the browser (use if headless is challenged)
csair auth --status   # show the cached token + expiry
csair auth --clear    # forget it
```

`search` auto-bootstraps when the token is missing/expired (and re-bootstraps once
if a request gets blocked), unless you pass `--no-bootstrap`. To supply a token
yourself instead, use `--acw <token>` or the `CSAIR_ACW` env var.

### Environment

| Var | Purpose |
|---|---|
| `CSAIR_ACW` | `acw_sc__v2` token (skips the browser bootstrap) |
| `CSAIR_ACW_TC` | `acw_tc` cookie (optional companion) |
| `CSAIR_CHROME` | path to a Chrome/Chromium binary |
| `CSAIR_CURRENCY` | preferred display currency |

Run `csair --help` or `csair <command> --help` for the full reference.

## Why this exists

China Southern has no public flight-search API. Reverse-engineering the consumer
booking sites turned up three engines with very different ergonomics:

| Engine | Host | Protection | Verdict |
|---|---|---|---|
| Overseas NDC | `oversea.csair.com/tka/...` | AWS WAF + interactive captcha on rate-limit; grid is single-use and arrives via a skeleton→poll pattern | Hard to automate |
| Mainland | `www.csair.com/cn/zh` | Geo-blocked outside China | Unreachable from non-CN IPs |
| **Intl B2C (`/ita`)** | `b2c.csair.com/ita/...` | `openresty`, **no AWS WAF**, light Aliyun cookie (`acw_sc__v2`); full grid on first call | **Target** |

This project targets the **`/ita` engine**, which returns the full availability grid in
one JSON call, including the `bookingClassAvails` field (seats per RBD).

## API flow

```
# 1. Create a search session -> HTML containing  .../zh/shop/?execution=<EXEC>
POST https://b2c.csair.com/ita/intl/app            (application/x-www-form-urlencoded)
     language=zh&country=zh&m=0&flexible=1&adt=1&cnn=0&inf=0
     &dep[]=SFO&arr[]=CAN&depArea[]=US&arrArea[]=CN&date[]=YYYY-MM-DD

# 2. Query flights -> JSON
POST https://b2c.csair.com/ita/rest/intl/main/aoa/inter/queryInterFlight   (application/json)
     {"adults":1,"children":0,"infantsInLap":0,
      "slices":[{"date":"YYYY-MM-DD","origin":"SFO","destination":"CAN",
                 "depCityFlag":true,"arrCityFlag":true}],
      "sliceIndex":0,"lang":"zh","flightType":"singlePass",
      "execution":"<EXEC>","page":1}
```

### Response shape (the parts we use)

```
data.data.dateFlights[]            # one per itinerary option
  .segments[]                      # flightNo, carrier, depPort/arrPort, depTime/arrTime, plane, ...
  .stopNumber, .flyTime
  .prices[]
    .displayPrice / .displayCurrency
    .cabins[]
      .type            # First | Business | PremiumEconomy | Economy
      .name            # booking class / RBD letter (e.g. C, I, A, M)
      .bookingClassAvails   # seats available in that RBD ("9" = capped, "9 or more")
      .brandCode       # fare brand (e.g. JFFA, JSTA, YSTA)
```

### Availability semantics

A cabin is sold through several fare tiers (e.g. business 优惠/灵活/尊享), each its
own booking class (RBD) with its own `bookingClassAvails`. The **true seats
available for the cabin is the maximum across those tiers** — buying the top
(most expensive) fare covers the cheaper seats. So `CabinAvail.Seats = max(tiers)`
and `From = cheapest tier`; the per-RBD breakdown is kept as detail. Example:
business I=6 ($4,029) + C=8 ($6,660) → **Business: 8 seats, from $4,029**.

## Auth / anti-bot

No WAF and no interactive captcha. The only gate is the Aliyun cookie **`acw_sc__v2`**
(set by `g.alicdn.com/.../antidom.js`), plus `acw_tc`. Model:

- **Bootstrap once** from a real browser session to harvest `acw_sc__v2` (+ `acw_tc`),
  then call the API directly until it expires; re-harvest on expiry.
- Be polite: one search per session, throttle, cache. Hammering the *other* engine
  triggered an IP-scoped captcha (`CZWEB000010`); same courtesy applies here.

## Status

Working: `auth` (headless Chrome token bootstrap) and `search` — validated live
end-to-end for SFO→CAN. Output as table / JSON / CSV.

Not done yet: `calendar` (stub), and the airport→country table is a seed set
(`internal/airport`) rather than a full embedded dataset.

## Disclaimer

Unofficial, for personal use. Not affiliated with or endorsed by China Southern
Airlines. Respect the site's terms of service and rate limits.
