# TODO

## Retire the seat monitor after the trip dates pass

The seat monitor only watches the dates in `monitor.toml` (currently
**2026-06-10** and **2026-06-14**).

**It already auto-retires:** once every date has completely passed in its
departure airport's timezone, `report due` returns `false` and `report-mail.sh`
no-ops — no token mint, no email. So no flights-already-departed digests get
sent. The steps below are optional housekeeping (the scheduled runs still spin
up a runner 4×/day and exit early until you remove the schedule):

- [ ] Disable the schedule in `.github/workflows/monitor.yml` — drop the `cron:`
      entries (keep `workflow_dispatch` if you want to run it ad hoc), or disable
      the workflow from the Actions tab. This is the only step that saves CI.
- [ ] Remove the past `[[monitor.targets]]` from `monitor.toml` (or replace them
      with the next trip's route/date so the monitor keeps working).
- [ ] If you don't need the history, delete the stale snapshots in
      `data/monitor/SFO-CAN-2026-06-*.json` (git history still preserves them).

If there's no next trip to watch, you can also remove the monitor entirely:
`.github/workflows/monitor.yml`, `scripts/report-mail.sh`, `scripts/lib-email.sh`,
`cmd/report.go`, `internal/monitor/`, `monitor.toml`, and `data/monitor/`. The
core `csair search`/`auth` CLI is independent and unaffected.
