#!/usr/bin/env bash
#
# report-mail.sh diff|status
#
# Thin orchestration around `csair report` for the seat monitor. Mints the
# anti-bot token once, then:
#
#   diff    `csair report diff --write` — combined change report across all
#           configured targets; persists new/changed snapshots. Emails only when
#           something changed (empty report ⇒ no email).
#   status  `csair report status` — one combined current-status digest across
#           all targets. Always emails (read-only; snapshots untouched).
#
# Routes/dates/snapshot dir live in monitor.toml (the binary reads them via
# --config); this script holds no route knowledge.
#
# How it works: `csair report <mode> --out <file>` writes a structured JSON
# result (the explicit email decision + the full per-target story) to <file>
# while narrating its progress to stdout — so the CI log shows exactly what was
# fetched, compared, and decided, even on a "no change" run. This script reads
# the JSON with jq; it makes no email decision of its own.
#
# Outputs (when $GITHUB_OUTPUT is set, for the Actions workflow):
#   email=true|false   an email should be sent
#   subject=<text>     the subject line
#   body_file=<path>   the rendered email body
#
# Local email (optional): CSAIR_LOCAL_EMAIL=1 + CSAIR_MAIL_TO (+ CSAIR_MAIL_CC)
#   with a working msmtp/sendmail — see lib-email.sh.
#
# Requires jq (preinstalled on GitHub runners; install it for the local-cron path).
#
# Exit non-zero on a token/block failure so CI skips the email/commit steps and
# surfaces a plain "workflow failed" notification (no bogus email).
set -uo pipefail

MODE="${1:-}"
case "$MODE" in
  diff|status) ;;
  *) echo "usage: report-mail.sh diff|status" >&2; exit 2 ;;
esac

if ! command -v jq >/dev/null; then
  echo "report-mail: jq is required but not found on PATH." >&2
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

CONFIG="$REPO_ROOT/monitor.toml"
BODY_FILE="$(mktemp)"
RESULT_FILE="$(mktemp)"

echo "report-mail: mode=$MODE config=$CONFIG bin=${CSAIR[*]}" >&2

out() { [[ -n "${GITHUB_OUTPUT:-}" ]] && echo "$1=$2" >>"$GITHUB_OUTPUT"; }

# --- 0. Auto-retire once every monitored date is past. ----------------------
# `report due` prints "true" while any date is still upcoming in its departure
# airport's timezone, "false" once they all have. Skip everything (no token, no
# email) when there is nothing left to watch. A non-zero/odd result fails open
# (keeps running) so a transient error never silences the monitor.
due="$("${CSAIR[@]}" report due --config "$CONFIG")"
echo "report-mail: due check → ${due:-<error>}" >&2
if [[ "$due" == "false" ]]; then
  echo "report-mail: all monitored dates have passed (departure-airport TZ) — nothing to do." >&2
  exit 0
fi

# --- 1. Mint a token once for the whole run. --------------------------------
echo "report-mail: minting token…" >&2
if ! "${CSAIR[@]}" auth; then
  echo "report-mail: auth failed — token could not be minted (likely WAF block)." >&2
  exit 5
fi

# --- 2. Run the report. The binary narrates progress to stdout (shown in the
# CI log) and writes the structured result to $RESULT_FILE. diff --write also
# persists new/changed snapshots. ---------------------------------------------
set +e
if [[ "$MODE" == "diff" ]]; then
  "${CSAIR[@]}" report diff --write --out "$RESULT_FILE" --config "$CONFIG"
else
  "${CSAIR[@]}" report status --out "$RESULT_FILE" --config "$CONFIG"
fi
rc=$?
set -e
if [[ $rc -ne 0 ]]; then
  echo "report-mail: 'csair report $MODE' failed (exit $rc). Not emailing." >&2
  exit "$rc"
fi

# --- 3. Read the explicit decision from the JSON result (no guessing). -------
EMAIL="$(jq -r '.email' "$RESULT_FILE")"
SUBJ="$(jq -r '.subject' "$RESULT_FILE")"
jq -r '.body' "$RESULT_FILE" >"$BODY_FILE"
echo "report-mail: result — email=$EMAIL, $(jq -rc '.summary' "$RESULT_FILE")" >&2

if [[ "$EMAIL" != "true" ]]; then
  # diff with nothing changed → no email (a baseline may still have been written;
  # the workflow's commit step picks those up).
  echo "report-mail: no $MODE changes — nothing to email (see the 'report:' lines above for the per-target detail)." >&2
  exit 0
fi

# --- 4. Announce / send. -----------------------------------------------------
out email true
out subject "$SUBJ"
out body_file "$BODY_FILE"
send_local_email "$SUBJ" "$BODY_FILE"
exit 0
