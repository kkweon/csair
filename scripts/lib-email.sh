#!/usr/bin/env bash
#
# lib-email.sh — shared local-cron email sender for the seat-monitor scripts.
# Sourced, not executed.
#
# send_local_email SUBJECT BODY_FILE
#   Delivers via msmtp/sendmail when the local-cron path is enabled
#   (CSAIR_LOCAL_EMAIL=1 and CSAIR_MAIL_TO set); a no-op otherwise — under
#   GitHub Actions the workflow does its own SMTP send from the body file.
#   CSAIR_MAIL_CC (optional) adds a Cc. msmtp -t / sendmail -t read To/Cc from
#   the headers we emit.
send_local_email() {
  local subj="$1" body="$2"
  [[ "${CSAIR_LOCAL_EMAIL:-}" == "1" && -n "${CSAIR_MAIL_TO:-}" ]] || return 0
  {
    echo "To: ${CSAIR_MAIL_TO}"
    [[ -n "${CSAIR_MAIL_CC:-}" ]] && echo "Cc: ${CSAIR_MAIL_CC}"
    echo "Subject: ${subj}"
    echo
    cat "$body"
  } | (command -v msmtp >/dev/null && msmtp -t || sendmail -t) \
    && echo "monitor: local email sent to ${CSAIR_MAIL_TO}" >&2
}
