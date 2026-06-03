# csair — CLI interface

Status: **spec** (pre-implementation). Engine: `b2c.csair.com/ita` (see [../README.md](../README.md)).

## Synopsis

```
csair <command> [args] [flags]
```

| Command | Purpose |
|---|---|
| `search`   | Search a route+date; show seats available per booking class |
| `calendar` | Cheapest fare per date around a target |
| `auth`     | Bootstrap/refresh the `acw_sc__v2` token (headed Chrome) |
| `report`   | Render seat-monitor email bodies from `search --json` snapshots |

## `search`

Route/date accepted **positionally and via flags** (positional wins if both given):

```
csair search SFO CAN 2026-06-14
csair search --from SFO --to CAN --date 2026-06-14
```

Flags:

| Flag | Alias | Default | Meaning |
|---|---|---|---|
| `--from` |  | — | Origin IATA |
| `--to` |  | — | Destination IATA |
| `--date` |  | — | Departure date `YYYY-MM-DD` |
| `--adults` | `-a` | 1 | Adult pax |
| `--children` | `-c` | 0 | Child pax (2–11) |
| `--infants` | `-i` | 0 | Lap infants (<2) |
| `--cabin` |  | `all` | `economy\|premium\|business\|first\|all` |
| `--carrier` |  | — | Only this marketing carrier (e.g. `CZ` drops DL/AS/UA/BR interlines) |
| `--direct` |  | false | Nonstop only |
| `--min-seats` |  | 0 | Only show classes with ≥N seats |
| `--sort` |  | `price` | `price\|duration\|departure` |
| `--page` |  | 1 | Result page |

## `calendar`

```
csair calendar SFO CAN --month 2026-06
csair calendar SFO CAN --around 2026-06-14    # ± a few days
```

## `auth`

Built-in headed-Chrome bootstrap; caches token to the config dir.

```
csair auth            # launch Chrome once, harvest acw_sc__v2 (+acw_tc), cache it
csair auth --status   # show token + expiry
csair auth --clear    # forget cached token
```

`search`/`calendar` use the cached token automatically. If it is missing or expired they
**auto-launch the bootstrap once** (with a notice), unless:
- `--acw <token>` or `CSAIR_ACW` is set, or
- `--no-bootstrap` is passed (then exit code 3 with `run 'csair auth'`).

## `report`

Renders the seat-monitor email bodies for the routes/dates in the `[monitor]`
section of the config (pass `--config`). Both subcommands search every configured
target themselves and print **one combined body**. The rendering (business seat
map, diff, current-status digest, booking link) lives in `internal/monitor` and is
unit-tested.

```
csair report diff   --config monitor.toml [--write]   # combined change report
csair report status --config monitor.toml             # combined current-status digest
```

Config (`monitor.toml`):

```toml
[monitor]
snapshotDir = "data/monitor"
[[monitor.targets]]
from = "SFO"
to   = "CAN"
date = "2026-06-14"
[[monitor.targets]]
from = "SFO"
to   = "CAN"
date = "2026-06-16"
flights = ["CZ660"]   # optional: track exactly these flight keys
```

Each target watches **business class**. By default only **nonstop** itineraries
are tracked. The optional `flights` allowlist overrides that: it keeps only the
itineraries whose flight key matches (segment numbers joined by `+`, e.g.
`"CZ660"` for a 1-stop through-flight or `"CZ660+CZ8004"` for a connection) and
**ignores the nonstop filter**, so you can monitor a specific connecting
itinerary. Snapshots are keyed by route+date (`<FROM>-<TO>-<date>.json`), so a
given route+date should appear in at most one target.

`report diff` prints nothing (exit 0) when no business seat count moved, and the
combined change report otherwise — callers treat empty stdout as "no email".
`--write` persists the freshly-fetched snapshot for new (baseline) and changed
targets to `snapshotDir`, leaving unchanged ones alone. Both subcommands use the
cached token (run `csair auth` first); if every target fails to fetch they exit
non-zero so the monitor skips emailing.

## Output

Auto-detected by destination:
- **TTY → pretty table** (aligned, cabins as columns, cells = `class×seats`).
- **piped/redirected → JSON** (full grid).

Force with `--json`, `--table`, or `--csv` (flat one-row-per-class). `--no-color` / `NO_COLOR` disables ANSI.

Table sketch:

```
SFO → CAN  ·  2026-06-14  ·  1 adult  ·  USD

FLIGHT        ROUTING       DEP    ARR     DUR     STOPS   ECONOMY   BUSINESS   FROM
CZ658         SFO–CAN       00:35  06:20⁺¹ 14h45m  direct  A9 M9     I6 C8      $1,284
CZ660+CZ3368  SFO–WUH–CAN   00:35  10:20⁺¹ 18h45m  1 stop  E9 Q9     D1 C2      $1,226
```
(`9` = capped "9 or more".)

## Config & env

- Config file: `~/.config/csair/config.toml` — defaults for `currency`, `adults`, `carrier`.
- Token cache: `~/.config/csair/cookies.json`.
- Env: `CSAIR_ACW`, `CSAIR_CURRENCY`, `NO_COLOR`.
- Global flags: `--currency`, `--config <path>`, `-v/--verbose`, `--json/--table/--csv`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 2 | Usage error (bad flags/args) |
| 3 | Auth required / token expired (`--no-bootstrap`) |
| 4 | No flights found |
| 5 | Upstream blocked (anti-bot / rate limit) |
| 1 | Other error |
