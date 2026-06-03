#!/usr/bin/env bash
#
# lib-seats.sh — shared rendering helpers for business-class seat reports
# built from a `csair search ... --json` snapshot. Sourced by diff-seats.sh
# (change report) and status-seats.sh (current-status digest); it is not
# meant to be executed on its own.
#
# Keeping the seat-map reduction and the booking-URL format here means there
# is a single source of truth for both reports (the URL otherwise mirrors
# internal/auth/browser.go flightsURL()).

# jq program: reduce a snapshot to { "<flight key>": <business seats> }.
# - flight key: itinerary.flights joined with "+" (e.g. "CZ658"; "CZ660+CZ3368"
#   for a connection, though we run --direct so it's a single segment).
# - business seats: cabin-level .seats for the cabin named "business"
#   (case-insensitive; the JSON emits lowercase). // 0 so a flight that loses
#   its business cabin reads as N -> 0 rather than a dropped key.
SEAT_MAP_JQ='reduce .itineraries[]? as $it ({};
  . + { ($it.flights | join("+")):
        (($it.cabins[]? | select((.cabin | ascii_downcase) == "business") | .seats) // 0) })'

# jq column-pad helper (guards a negative width) shared by the report lists.
PAD_JQ='def pad($w): . + (if ($w - length) > 0 then " " * ($w - length) else "" end);'

# seat_map FILE -> compact, key-sorted JSON object { flight: business_seats }.
seat_map() { jq -cS "$SEAT_MAP_JQ" "$1"; }

# parse_route FILE: populate SEAT_ORIGIN / SEAT_DEST / SEAT_DATE from a snapshot.
parse_route() {
  read -r SEAT_ORIGIN SEAT_DEST SEAT_DATE \
    < <(jq -r '"\(.origin) \(.destination) \(.date)"' "$1")
}

# header_line -> "ORIG → DEST  ·  DATE  ·  Business" (needs parse_route first).
header_line() {
  printf '%s → %s  ·  %s  ·  Business\n' "$SEAT_ORIGIN" "$SEAT_DEST" "$SEAT_DATE"
}

# asof_line -> "As of YYYY-MM-DD HH:MM PDT" in America/Los_Angeles (auto PDT/PST)
# so the reader knows how fresh the status is.
asof_line() {
  printf 'As of %s\n' "$(TZ=America/Los_Angeles date '+%Y-%m-%d %H:%M %Z')"
}

# book_url_line -> "Book: <deep-link>" pinned to SEAT_DATE (needs parse_route).
book_url_line() {
  local d="${SEAT_DATE//-/}"
  printf 'Book: https://b2c.csair.com/ita/intl/zh/flights?flex=1&m=0&p=100&t=%s-%s-%s&egs=ITA,ITA&open=1\n' \
    "$SEAT_ORIGIN" "$SEAT_DEST" "$d"
}

# all_seats_now MAPJSON -> the "All business seats now" lines: every flight in
# the snapshot, sorted by seats desc then flight asc; zero-seat / no-business-
# cabin flights flagged so a sold-out flight stands out rather than reading as
# a low count.
all_seats_now() {
  jq -rn --argjson n "$1" "$PAD_JQ"'
    $n | to_entries
    | sort_by([-.value, .key])
    | .[]
    | "  \((.key + ":") | pad(15))"
      + (if .value > 0 then "\(.value) seats" else "⚠️ NO SEATS" end)'
}
