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
# Outputs (when $GITHUB_OUTPUT is set, for the Actions workflow):
#   email=true|false   an email should be sent
#   subject=<text>     the subject line
#   body_file=<path>   the rendered email body
#
# Local email (optional): CSAIR_LOCAL_EMAIL=1 + CSAIR_MAIL_TO (+ CSAIR_MAIL_CC)
#   with a working msmtp/sendmail — see lib-email.sh.
#
# Exit non-zero on a token/block failure so CI skips the email/commit steps and
# surfaces a plain "workflow failed" notification (no bogus email).
set -uo pipefail

MODE="${1:-}"
case "$MODE" in
  diff|status) ;;
  *) echo "usage: report-mail.sh diff|status" >&2; exit 2 ;;
esac

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

out() { [[ -n "${GITHUB_OUTPUT:-}" ]] && echo "$1=$2" >>"$GITHUB_OUTPUT"; }

# --- 0. Auto-retire once every monitored date is past. ----------------------
# `report due` prints "true" while any date is still upcoming in its departure
# airport's timezone, "false" once they all have. Skip everything (no token, no
# email) when there is nothing left to watch. A non-zero/odd result fails open
# (keeps running) so a transient error never silences the monitor.
due="$("${CSAIR[@]}" report due --config "$CONFIG")"
if [[ "$due" == "false" ]]; then
  echo "report-mail: all monitored dates have passed (departure-airport TZ) — nothing to do." >&2
  exit 0
fi

# --- 1. Mint a token once for the whole run. --------------------------------
echo "report-mail: minting token…" >&2
if ! "${CSAIR[@]}" auth >&2; then
  echo "report-mail: auth failed — token could not be minted (likely WAF block)." >&2
  exit 5
fi

# --- 2. Render the body via the binary (diff also persists snapshots). -------
set +e
if [[ "$MODE" == "diff" ]]; then
  "${CSAIR[@]}" report diff --write --config "$CONFIG" >"$BODY_FILE"
else
  "${CSAIR[@]}" report status --config "$CONFIG" >"$BODY_FILE"
fi
rc=$?
if [[ $rc -ne 0 ]]; then
  echo "report-mail: 'csair report $MODE' failed (exit $rc). Not emailing." >&2
  exit "$rc"
fi

# diff prints nothing when nothing changed → no email (snapshots may still have
# been written for a baseline; the workflow's commit step picks those up).
if [[ ! -s "$BODY_FILE" ]]; then
  echo "report-mail: no $MODE output — nothing to email." >&2
  exit 0
fi

# --- 3. Announce / send. -----------------------------------------------------
if [[ "$MODE" == "diff" ]]; then
  SUBJ="[csair] business seats changed"
else
  SUBJ="[csair] business seats — daily status"
fi
out email true
out subject "$SUBJ"
out body_file "$BODY_FILE"
send_local_email "$SUBJ" "$BODY_FILE"
exit 0
