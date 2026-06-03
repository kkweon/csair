#!/usr/bin/env bash
#
# monitor.sh DATE
#
# Monitors SFO→CAN business-class seat availability for one departure DATE
# (YYYY-MM-DD). Mints a token, runs a direct-only business search, diffs the
# result against the committed snapshot, and reports whether the business
# seat count changed.
#
# Safety contract:
#   - A blocked/expired/no-flights fetch NEVER overwrites the last-good
#     snapshot and NEVER emails a bogus "seats changed" message; the script
#     exits non-zero so the caller (CI) skips the commit step and surfaces a
#     plain "workflow failed" notification (natural de-dupe, no spam).
#   - First run (no prior snapshot) writes a silent baseline, no email.
#   - Unchanged business seats → nothing written, nothing emailed.
#   - Price-only changes are ignored (diff-seats.sh only reads seat counts).
#
# Outputs (when $GITHUB_OUTPUT is set, for the Actions workflow):
#   changed=true|false   business seats changed vs the committed snapshot
#   baseline=true|false  this run established the first snapshot
#   blocked=true|false   fetch failed (blocked/expired/no-flights)
#   body_file=<path>     email body (only when changed=true)
#
# Local email (optional): set CSAIR_LOCAL_EMAIL=1 and provide
#   CSAIR_MAIL_TO and a working `msmtp`/`sendmail` to email directly from cron.
#   CSAIR_MAIL_CC (optional) adds a Cc recipient.
set -uo pipefail

ROUTE_FROM="SFO"
ROUTE_TO="CAN"

DATE="${1:-}"
if [[ -z "$DATE" ]]; then
  echo "usage: monitor.sh YYYY-MM-DD" >&2
  exit 2
fi

# Resolve paths relative to the repo root (parent of this script's dir).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# csair binary: prefer a built ./csair, else fall back to `go run`.
if [[ -x "$REPO_ROOT/csair" ]]; then
  CSAIR=("$REPO_ROOT/csair")
else
  CSAIR=(go run "$REPO_ROOT/main.go")
fi

SNAP_DIR="$REPO_ROOT/data/monitor"
SNAP="$SNAP_DIR/${ROUTE_FROM}-${ROUTE_TO}-${DATE}.json"
mkdir -p "$SNAP_DIR"

NEW_JSON="$(mktemp)"
BODY_FILE="$(mktemp)"
trap 'rm -f "$NEW_JSON"' EXIT

# emit KEY=VALUE to GITHUB_OUTPUT if running under Actions; harmless otherwise.
# Each exit path emits only the keys it needs; unset outputs read as "" in the
# workflow, which correctly fails the `== 'true'` guards (no defaults needed).
out() { [[ -n "${GITHUB_OUTPUT:-}" ]] && echo "$1=$2" >>"$GITHUB_OUTPUT"; }

# --- 1. Mint a token (its own observable step). -----------------------------
echo "monitor: minting token for ${ROUTE_FROM}→${ROUTE_TO} ${DATE}…" >&2
if ! "${CSAIR[@]}" auth >&2; then
  echo "monitor: auth failed — token could not be minted (likely WAF block). Not touching snapshot." >&2
  out blocked true
  exit 5
fi

# --- 2. Search against the warm cache (no implicit re-bootstrap). ------------
set +e
"${CSAIR[@]}" search "$ROUTE_FROM" "$ROUTE_TO" "$DATE" \
  --cabin business --direct --json --no-bootstrap >"$NEW_JSON"
rc=$?

if [[ $rc -ne 0 ]]; then
  echo "monitor: search exited $rc (3=token,4=no-flights,5=blocked). Not touching snapshot." >&2
  out blocked true
  exit "$rc"
fi

# Guard against an empty/garbage stdout that is technically exit 0.
if ! jq -e . "$NEW_JSON" >/dev/null 2>&1; then
  echo "monitor: search produced invalid JSON. Not touching snapshot." >&2
  out blocked true
  exit 1
fi

# --- 3. Baseline vs diff. ----------------------------------------------------
if [[ ! -f "$SNAP" ]]; then
  cp "$NEW_JSON" "$SNAP"
  echo "monitor: baseline established at $SNAP" >&2
  out baseline true
  exit 0
fi

set +e
report="$("$SCRIPT_DIR/diff-seats.sh" "$SNAP" "$NEW_JSON")"
drc=$?

if [[ $drc -eq 0 ]]; then
  echo "monitor: no business seat-count change for $DATE." >&2
  exit 0
elif [[ $drc -eq 10 ]]; then
  cp "$NEW_JSON" "$SNAP"
  printf '%s\n' "$report" | tee "$BODY_FILE" >&2
  out changed true
  out body_file "$BODY_FILE"

  if [[ "${CSAIR_LOCAL_EMAIL:-}" == "1" && -n "${CSAIR_MAIL_TO:-}" ]]; then
    subj="[csair] ${ROUTE_FROM}→${ROUTE_TO} ${DATE} business seats changed"
    {
      echo "To: ${CSAIR_MAIL_TO}"
      [[ -n "${CSAIR_MAIL_CC:-}" ]] && echo "Cc: ${CSAIR_MAIL_CC}"
      echo "Subject: ${subj}"
      echo
      cat "$BODY_FILE"
    } | (command -v msmtp >/dev/null && msmtp -t \
         || sendmail -t) \
      && echo "monitor: local email sent to ${CSAIR_MAIL_TO}" >&2
  fi
  exit 0
else
  echo "monitor: diff-seats.sh failed (exit $drc). Not touching snapshot." >&2
  exit "$drc"
fi
