#!/usr/bin/env bash
#
# status-digest.sh DATE [DATE ...]
#
# Sends ONE combined business-class current-status email covering all the given
# departure dates (the daily 9am / manual-dispatch digest). Mints a token once,
# searches each date against the warm cache, and renders a single body via
# `csair report status`. Read-only: it never touches the committed snapshots.
#
# A date whose fetch fails is omitted from the digest rather than faking data;
# if every fetch fails the script exits non-zero so no bogus email is sent.
#
# Outputs (when $GITHUB_OUTPUT is set, for the Actions workflow):
#   email=true|false   an email should be sent (at least one date succeeded)
#   subject=<text>     the subject line for that email
#   body_file=<path>   the combined email body
#   blocked=true|false every fetch failed (nothing sent)
#
# Local email (optional): same CSAIR_LOCAL_EMAIL / CSAIR_MAIL_TO / CSAIR_MAIL_CC
#   contract as monitor.sh.
set -uo pipefail

ROUTE_FROM="SFO"
ROUTE_TO="CAN"

if [[ $# -lt 1 ]]; then
  echo "usage: status-digest.sh DATE [DATE ...]" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/lib-email.sh"

if [[ -x "$REPO_ROOT/csair" ]]; then
  CSAIR=("$REPO_ROOT/csair")
else
  CSAIR=(go run "$REPO_ROOT/main.go")
fi

BODY_FILE="$(mktemp)"
TMPS=()
trap 'rm -f "${TMPS[@]}"' EXIT

out() { [[ -n "${GITHUB_OUTPUT:-}" ]] && echo "$1=$2" >>"$GITHUB_OUTPUT"; }

# --- 1. Mint a token once for the whole digest. -----------------------------
echo "status-digest: minting token for ${ROUTE_FROM}→${ROUTE_TO}…" >&2
if ! "${CSAIR[@]}" auth >&2; then
  echo "status-digest: auth failed — token could not be minted (likely WAF block)." >&2
  out blocked true
  exit 5
fi

# --- 2. Search each date against the warm cache; collect the good snapshots. -
ok_files=()
ok_dates=()
for d in "$@"; do
  j="$(mktemp)"
  TMPS+=("$j")
  if "${CSAIR[@]}" search "$ROUTE_FROM" "$ROUTE_TO" "$d" \
       --cabin business --direct --json --no-bootstrap >"$j" \
     && jq -e . "$j" >/dev/null 2>&1; then
    ok_files+=("$j")
    ok_dates+=("$d")
  else
    echo "status-digest: search failed for $d — omitting from digest." >&2
  fi
done

if [[ ${#ok_files[@]} -eq 0 ]]; then
  echo "status-digest: every search failed; nothing to send." >&2
  out blocked true
  exit 5
fi

# --- 3. Render one combined body and (optionally) send it. -------------------
"${CSAIR[@]}" report status "${ok_files[@]}" | tee "$BODY_FILE" >&2

dates_csv="$(printf '%s, ' "${ok_dates[@]}")"; dates_csv="${dates_csv%, }"
SUBJ="[csair] ${ROUTE_FROM}→${ROUTE_TO} business seats — daily status (${dates_csv})"

out email true
out subject "$SUBJ"
out body_file "$BODY_FILE"
send_local_email "$SUBJ" "$BODY_FILE"
exit 0
