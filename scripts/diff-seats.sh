#!/usr/bin/env bash
#
# diff-seats.sh OLD.json NEW.json
#
# Compares two `csair search ... --json` snapshots and reports per-flight
# changes in BUSINESS-class seat count, keyed by flight number (e.g. CZ658).
# Price changes are ignored by construction (we only read cabin-level .seats).
#
# Exit codes:
#   0  no business seat-count change
#   10 at least one flight's business seats changed (report printed to stdout)
#   2  usage / bad input
#
# The stdout on a change is a human-readable report suitable for an email body.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: diff-seats.sh OLD.json NEW.json" >&2
  exit 2
fi

old="$1"
new="$2"

for f in "$old" "$new"; do
  if [[ ! -f "$f" ]]; then
    echo "diff-seats.sh: no such file: $f" >&2
    exit 2
  fi
done

# Reduce a snapshot to { "<flight key>": <business seats> }.
# - flight key: itinerary.flights joined with "+" (e.g. "CZ658"; "CZ660+CZ3368"
#   for a connection, though we run --direct so it's a single segment).
# - business seats: cabin-level .seats for the cabin named "business"
#   (case-insensitive; the JSON emits lowercase). // 0 so a flight that loses
#   its business cabin reads as N -> 0 rather than a dropped key.
seat_map='reduce .itineraries[]? as $it ({};
  . + { ($it.flights | join("+")):
        (($it.cabins[]? | select((.cabin | ascii_downcase) == "business") | .seats) // 0) })'

old_map="$(jq -cS "$seat_map" "$old")"
new_map="$(jq -cS "$seat_map" "$new")"

# Build the list of changed flights as objects {flight, old, new}.
changes="$(jq -n \
  --argjson o "$old_map" \
  --argjson n "$new_map" '
    ($o + $n | keys_unsorted | unique) as $flights
    | [ $flights[]
        | { flight: ., old: ($o[.] // null), new: ($n[.] // null) }
        | select(.old != .new) ]')"

count="$(jq 'length' <<<"$changes")"

if [[ "$count" -eq 0 ]]; then
  exit 0
fi

# Route/date and a snapshot freshness stamp in the traveler's local zone
# (America/Los_Angeles, auto PDT/PST) so the reader knows "as of when".
read -r origin destination date < <(jq -r '"\(.origin) \(.destination) \(.date)"' "$new")
header="${origin} → ${destination}  ·  ${date}  ·  Business"
asof="As of $(TZ=America/Los_Angeles date '+%Y-%m-%d %H:%M %Z')"

# Booking deep-link pinned to the monitored date (YYYYMMDD), mirroring the
# format built by internal/auth/browser.go flightsURL().
url_date="${date//-/}"
book_url="https://b2c.csair.com/ita/intl/zh/flights?flex=1&m=0&p=100&t=${origin}-${destination}-${url_date}&egs=ITA,ITA&open=1"

# jq column-pad helper shared by both lists (guards a negative width).
pad_def='def pad($w): . + (if ($w - length) > 0 then " " * ($w - length) else "" end);'

# The changed flights (old → new), with (new)/(gone) for appear/disappear.
changes_fmt="$(jq -r "$pad_def"'
  .[]
  | "  \((.flight + ":") | pad(15))"
    + "\(if .old == null then "(new)" else "\(.old)" end) → "
    + (if .new == null then "(gone)"
       elif .new == 0 then "⚠️ NO SEATS"
       else "\(.new) seats" end)' <<<"$changes")"

# The full current business-seat map (every flight in the NEW snapshot), sorted
# by seats desc then flight asc. Zero-seat flights are flagged so a sold-out /
# no-business-cabin flight stands out rather than reading as a low count.
all_seats="$(jq -rn --argjson n "$new_map" "$pad_def"'
  $n | to_entries
  | sort_by([-.value, .key])
  | .[]
  | "  \((.key + ":") | pad(15))"
    + (if .value > 0 then "\(.value) seats" else "⚠️ NO SEATS" end)')"

{
  echo "$header"
  echo "$asof"
  echo
  echo "Changed:"
  echo "$changes_fmt"
  echo
  echo "All business seats now:"
  echo "$all_seats"
  echo
  echo "Book: $book_url"
}

exit 10
