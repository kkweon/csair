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

# Shared seat-map / header / URL / "all seats now" rendering.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib-seats.sh"

old_map="$(seat_map "$old")"
new_map="$(seat_map "$new")"

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

# Route/date header from the NEW snapshot (also sets SEAT_* for the URL).
parse_route "$new"

# The changed flights (old → new), with (new)/(gone) for appear/disappear and
# the NO SEATS flag on a flight that has dropped to zero business availability.
changes_fmt="$(jq -r "$PAD_JQ"'
  .[]
  | "  \((.flight + ":") | pad(15))"
    + "\(if .old == null then "(new)" else "\(.old)" end) → "
    + (if .new == null then "(gone)"
       elif .new == 0 then "⚠️ NO SEATS"
       else "\(.new) seats" end)' <<<"$changes")"

{
  header_line
  asof_line
  echo
  echo "Changed:"
  echo "$changes_fmt"
  echo
  echo "All business seats now:"
  all_seats_now "$new_map"
  echo
  book_url_line
}

exit 10
