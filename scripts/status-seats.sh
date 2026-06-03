#!/usr/bin/env bash
#
# status-seats.sh SNAPSHOT.json
#
# Renders a current-status digest of BUSINESS-class availability from a single
# `csair search ... --json` snapshot: every flight's seat count right now
# (sold-out flights flagged), an "as of" stamp, and a booking link. Unlike
# diff-seats.sh it does not compare against anything — it always prints the
# present state, for the daily 9am / manual-dispatch status email.
#
# Exit codes:
#   0  report printed to stdout
#   2  usage / bad input
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: status-seats.sh SNAPSHOT.json" >&2
  exit 2
fi

snap="$1"
if [[ ! -f "$snap" ]]; then
  echo "status-seats.sh: no such file: $snap" >&2
  exit 2
fi

# Shared seat-map / header / URL / "all seats now" rendering.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib-seats.sh"

parse_route "$snap"
map="$(seat_map "$snap")"

{
  header_line
  asof_line
  echo
  echo "All business seats now:"
  all_seats_now "$map"
  echo
  book_url_line
}
