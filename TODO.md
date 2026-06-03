# TODO

## Retire the seat monitor after the trip dates pass

The seat monitor only watches the dates in `monitor.toml` (currently
**2026-06-10** and **2026-06-14**). Once the **last** monitored date is in the
past, the workflow just burns CI minutes and (in `status` mode) emails a digest
for flights that have already departed. Clean it up after **2026-06-14**:

- [ ] Disable the schedule in `.github/workflows/monitor.yml` — drop the `cron:`
      entries (keep `workflow_dispatch` if you want to run it ad hoc), or disable
      the workflow from the Actions tab.
- [ ] Remove the past `[[monitor.targets]]` from `monitor.toml` (or replace them
      with the next trip's route/date so the monitor keeps working).
- [ ] If you don't need the history, delete the stale snapshots in
      `data/monitor/SFO-CAN-2026-06-*.json` (git history still preserves them).

If there's no next trip to watch, you can also remove the monitor entirely:
`.github/workflows/monitor.yml`, `scripts/report-mail.sh`, `scripts/lib-email.sh`,
`cmd/report.go`, `internal/monitor/`, `monitor.toml`, and `data/monitor/`. The
core `csair search`/`auth` CLI is independent and unaffected.
