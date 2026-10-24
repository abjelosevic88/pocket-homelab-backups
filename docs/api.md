# API

JSON over HTTP under `/api/v1`. Authenticate with a session cookie, or `Authorization: Bearer pbk_…` (Settings →
Tokens), or a Pocket Core token. Everything except `/overview` and `/widget` needs an admin.

| | |
|---|---|
| `GET /overview` | health (`ok` / `warning` / `error` / `empty`), issues; admins also get jobs, repositories, connections, recent and running runs |
| `GET /widget` | a small summary for a Homepage `customapi` card: `health`, `issues`, `last_backup_ago`, `stored` |
| `GET/POST /jobs`, `GET/PUT/DELETE /jobs/{id}` | jobs (with `source_ids`, `copies`, `next_run`, `last_run`, `last_good`, `running`) |
| `POST /jobs/{id}/run` | start now → the run (202) |
| `POST /jobs/{id}/copies`, `PUT/DELETE /jobs/{id}/copies/{cid}` | copies after each snapshot |
| `GET/POST /sources`, `PUT/DELETE /sources/{id}`, `POST /sources/{id}/run` | what to protect; capture one now |
| `GET /discover` | suggested sources from what is running |
| `GET/POST /repositories`, `GET/PUT/DELETE /repositories/{id}` | restic repositories (`create: true` makes a new one with a generated password) |
| `POST /repositories/{id}/action` | `{"action": "check" | "prune" | "unlock" | "stats" | "init" | "mount" | "unmount" | "resume" | "pause"}` |
| `POST /repositories/{id}/password` | the repository password |
| `GET /repositories/{id}/snapshots` | newest first |
| `GET /repositories/{id}/ls?snapshot=&path=` | one folder of a snapshot |
| `GET /repositories/{id}/download?snapshot=&path=[&zip=1]` | a file, or a folder as a zip |
| `POST /repositories/{id}/restore` | `{"snapshot", "paths": [...], "target": "/" or a folder, "overwrite": "never"…}` |
| `GET /repositories/{id}/report` | size per app of the newest snapshot |
| `GET/POST /connections`, `PUT/DELETE /connections/{id}` | clouds and servers; `POST /connections/{id}/test`, `GET /connections/{id}/browse?path=` |
| `GET /connections/kinds`, `GET /connections/importable`, `POST /connections/import` | kinds and their fields; import from an rclone.conf |
| `GET /connections/oauth/{kind}/start` | sign in to Google Drive, OneDrive or Dropbox |
| `GET /runs?job=&repository=&kind=&status=&before=&limit=`, `GET /runs/{id}`, `POST /runs/{id}/cancel` | runs with steps and log |
| `GET /coverage`, `POST /coverage/skip` | which running apps a backup covers |
| `GET /backrest`, `POST /backrest/import`, `GET/POST /backrest/hooks` | Backrest underneath |
| `POST /hooks/backrest` | what Backrest's hooks call (see [backrest.md](backrest.md)) |
| `GET /browse?path=&dirs=1` | the box's folders (picker) |
| `GET/PUT /integrations` | Homepage report files |

## Homepage

```yaml
- Backups:
    href: https://backups.example.com
    icon: https://backups.example.com/favicon.svg
    widget:
      type: customapi
      url: https://backups.example.com/api/v1/widget
      headers: { Authorization: Bearer pbk_… }
      mappings:
        - { field: health, label: Status }
        - { field: last_backup_ago, label: Last backup }
        - { field: stored, label: Stored }
```
