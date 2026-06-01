# csair

A CLI to search China Southern Airlines (CZ) flights and show **seats available per
booking class** for a route and date.

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

## Auth / anti-bot

No WAF and no interactive captcha. The only gate is the Aliyun cookie **`acw_sc__v2`**
(set by `g.alicdn.com/.../antidom.js`), plus `acw_tc`. Model:

- **Bootstrap once** from a real browser session to harvest `acw_sc__v2` (+ `acw_tc`),
  then call the API directly until it expires; re-harvest on expiry.
- Be polite: one search per session, throttle, cache. Hammering the *other* engine
  triggered an IP-scoped captcha (`CZWEB000010`); same courtesy applies here.

## Status

Early. Flow validated by hand (curl) for SFO→CAN. CLI implementation TBD.

## Disclaimer

Unofficial, for personal use. Not affiliated with or endorsed by China Southern
Airlines. Respect the site's terms of service and rate limits.
