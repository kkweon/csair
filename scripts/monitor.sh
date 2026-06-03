#!/usr/bin/env bash
#
# monitor.sh DATE
#
# Watches SFO→CAN business-class seat availability for one departure DATE
# (YYYY-MM-DD): mints a token, runs a direct-only business search, and diffs the
# result against the committed snapshot via `csair report diff`. Emails (and
# updates the snapshot) ONLY when the business seat count changed.
#
# This is the every-3h change watch. The daily/manual "current status" digest
# (one email across all dates) is a separate entry point: scripts/status-digest.sh.
#
# Safety contract:
#   - A blocked/expired/no-flights fetch NEVER overwrites the last-good snapshot
#     and NEVER emails a bogus "seats changed" message; the script exits non-zero
#     so the caller (CI) skips the email/commit steps and surfaces a plain
#     "workflow failed" notification (natural de-dupe, no spam).
#   - First run (no prior snapshot) writes a silent baseline, no email.
#   - Unchanged business seats → nothing written, nothing emailed.
#   - Price-only changes are ignored (the diff compares seat counts only).
#
# Outputs (when $GITHUB_OUTPUT is set, for the Actions workflow):
#   email=true|false     an email should be sent (i.e. seats changed)
#   subject=<text>       the subject line for that email
#   body_file=<path>     the email body (only when email=true)
#   changed=true|false   business seats changed vs the committed snapshot
#   baseline=true|false  this run established the first snapshot
#   blocked=true|false   fetch failed (blocked/expired/no-flights)
#
# Local email (optional): set CSAIR_LOCAL_EMAIL=1 and provide CSAIR_MAIL_TO and
#   a working `msmtp`/`sendmail` to email directly from cron. CSAIR_MAIL_CC
#   (optional) adds a Cc recipient.
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
source "$SCRIPT_DIR/lib-email.sh"

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
out() { [[ -n "${GITHUB_OUTPUT:-}" ]] && echo "$1=$2" >>"$GITHUB_OUTPUT"; }

CHANGED_SUBJ="[csair] ${ROUTE_FROM}→${ROUTE_TO} ${DATE} business seats changed"

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

# `csair report diff` prints the change body on a change and nothing when
# unchanged; a non-zero exit means the fetch/inputs were bad (don't touch state).
report="$("${CSAIR[@]}" report diff "$SNAP" "$NEW_JSON")"
drc=$?
if [[ $drc -ne 0 ]]; then
  echo "monitor: report diff failed (exit $drc). Not touching snapshot." >&2
  exit "$drc"
fi

if [[ -z "$report" ]]; then
  echo "monitor: no business seat-count change for $DATE." >&2
  exit 0
fi

cp "$NEW_JSON" "$SNAP"
printf '%s\n' "$report" | tee "$BODY_FILE" >&2
out changed true
out email true
out subject "$CHANGED_SUBJ"
out body_file "$BODY_FILE"
send_local_email "$CHANGED_SUBJ" "$BODY_FILE"
exit 0
