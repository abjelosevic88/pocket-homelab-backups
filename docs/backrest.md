# Pocket Backups on top of Backrest

Backrest is good at running restic on a schedule. What it doesn't do is everything around the snapshot, which
people usually solve with hook scripts: dump the databases first, copy the repository to the NAS and a cloud after,
tell someone, pause the copies when a check fails, mount a USB disk. Pocket Backups does all of that, and Backrest's
hooks just call it.

1. Set `BACKREST_URL`, `BACKREST_USER` and `BACKREST_PASSWORD` (and `HOOK_BASE_URL` if Backrest reaches the app on
   another address than `BASE_URL`).
2. **Backrest → Import from Backrest**: every Backrest repository becomes a repository here (same restic repository,
   same password) and every plan a job that *runs on Backrest*.
3. Give those jobs what they need: sources (dumped before the snapshot into the job's staging folder — make sure the
   plan backs that folder up), copies, retention to apply before the copies, a heartbeat, the coverage check.
4. **Install hooks**. The preview shows, per plan and repository, the hooks that are there now and the ones that
   replace them. Installing writes Backrest's config through its API and creates an API token for the hooks.

| Backrest hook | What Pocket Backups does |
|---|---|
| plan `SNAPSHOT_START` | checks required mounts, mounts a USB disk, the coverage check, captures the sources. Answers 409 to stop the snapshot when a share is not mounted or the disk is missing (the hook is installed with *on error: cancel* then) |
| plan `SNAPSHOT_SUCCESS` | retention (+ prune), read-back, copies (paused after a failed check), a check, repository size, the Homepage report, the heartbeat, unmounting the disk, a notification |
| plan `SNAPSHOT_WARNING` | the same, and says which files could not be read |
| plan `ANY_ERROR` | a notification, unmounting the disk |
| repository `CHECK_ERROR` / `CHECK_SUCCESS` | pauses / resumes the copies |
| repository `PRUNE_ERROR` | a notification |
| repository `CHECK_START`, `PRUNE_START` (USB disks) | mounts the disk; `*_SUCCESS`/`*_ERROR` unmount it |

A hook is one `curl` to `POST /api/v1/hooks/backrest` with `event`, `plan` or `repo`, and for snapshots
`snapshot` / `task` / `error` filled from Backrest's templates. Each call shows up under **Runs** with its steps and
log.

Pocket Backups also reads Backrest's operation log every five minutes and tells you about operations that ended in
a warning or an error that no hook reported.

**Run now** on a Backrest job asks Backrest to run the plan; the steps before and after appear as their own runs.

## Moving off Backrest

**Backrest → Run them here instead** takes every plan a job runs on over:

- the job gets the plan's folders, excludes, schedule (Backrest's cron, in the box's time zone) and retention, and a
  `plan:<id>` tag so retention keeps counting Backrest's older snapshots too;
- each repository gets its check (with the same share of data read) and prune schedules, and is managed here;
- Backrest's last backup of the plan is kept as a run, so "last good backup" stays true;
- in Backrest the plans are switched off (schedule disabled, hooks removed, the repositories' checks and prunes
  disabled), so nothing runs twice. Backrest can then be stopped; the restic repositories are untouched and the next
  snapshot continues from Backrest's last one.

Everything that now runs on its own is on the **Schedule** page.
