# Configuration

Everything is an environment variable with a sensible default; a single `/data` volume is enough to start.

## The app

| Variable | Default | |
|---|---|---|
| `PORT` / `HOST` | `8080` / `0.0.0.0` | where it listens |
| `BASE_URL` | – | the address people open (needed for sign-in redirects and links in notifications) |
| `DATA_DIR` | `./data` (`/data` in the image) | database, `secret.key`, the generated `rclone/rclone.conf`, the Core token |
| `SECURE_COOKIES` | `false` | `true` behind HTTPS |
| `TZ` | `UTC` | schedules run in this zone |
| `LOG_LEVEL` / `LOG_FORMAT` | `info` / `text` | `debug`…`error`; `json` |
| `AUTH_MODE` | `local` | `local` accounts, `proxy` (trust `AUTH_PROXY_HEADER`/`AUTH_PROXY_EMAIL_HEADER`), `none` |
| `ADMIN_EMAIL` / `ADMIN_PASSWORD` / `ADMIN_NAME` | – | the first admin, created while there are no users |
| `OIDC_ISSUER` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` / `OIDC_NAME` | – | sign in with any OpenID Connect provider (not needed with Pocket Core) |
| `NOTIFY_URL` / `NOTIFY_KIND` | – / `apprise` | the box owner's notification route until they set their own: an Apprise route, an ntfy topic URL or a webhook |

## The engine

| Variable | Default | |
|---|---|---|
| `STAGING_DIR` | `$DATA_DIR/staging` | where dumps land before a snapshot (each job can set its own folder) |
| `TMP_DIR` | `$DATA_DIR/tmp` | restic's temporary pack files. Keep it on a disk: a RAM-backed `/tmp` fills up at the very end of a big backup |
| `CACHE_DIR` | `$DATA_DIR/cache` | restic's cache |
| `SNAPSHOT_HOST` | the host name | the host snapshots are recorded under (keep it stable: restic finds the previous snapshot by host and paths) |
| `RESTIC_BIN` / `RCLONE_BIN` / `DOCKER_BIN` | in `PATH` | the image ships restic, rclone and the docker CLI |
| `DOCKER_HOST` | the socket | another Docker daemon |
| `HOST_ACCESS` | `false` | allow host commands and USB disks: a short-lived privileged helper container (`nsenter` into the host) runs them |
| `HELPER_IMAGE` | `alpine:3.21` | the helper's image (the app's own image works and needs no pull) |
| `DEV_DISK_DIR` | `/host/dev/disk` | the host's `/dev/disk` mounted read-only: how the app sees a USB disk being plugged in |
| `BROWSE_ROOT` | `/` | the folder picker and restore targets stay inside this |
| `RCLONE_IMPORT` | – | an existing `rclone.conf` whose remotes can be imported as connections |

## Backrest (optional)

| Variable | | |
|---|---|---|
| `BACKREST_URL` | – | e.g. `http://host.docker.internal:9898` |
| `BACKREST_USER` / `BACKREST_PASSWORD` | – | Backrest's login |
| `HOOK_BASE_URL` | `BASE_URL` | how Backrest reaches this app from its hooks (e.g. `http://127.0.0.1:8203`) |

## Clouds that sign in with a browser

Signing in from the app needs an OAuth client of your own (a token from `rclone authorize` works without one):

| Variables | Where | Redirect URI |
|---|---|---|
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Google Cloud console: enable the Drive API, publish the consent screen, OAuth client of type *Web application* | `BASE_URL/api/v1/connections/oauth/callback` |
| `MICROSOFT_CLIENT_ID`, `MICROSOFT_CLIENT_SECRET`, `MICROSOFT_TENANT` | Microsoft Entra: app registration for personal and work accounts, a client secret | same |
| `DROPBOX_APP_KEY`, `DROPBOX_APP_SECRET` | Dropbox App Console: scoped access, full Dropbox, `files.content.write` | same |

## Pocket Core

| Variable | | |
|---|---|---|
| `CORE_URL` | – | e.g. `http://pocket-core:8080`; registers on start |
| `CORE_REGISTRATION_KEY` | – | Core's registration key |
| `CORE_APP_URL` | `BASE_URL` | how Core reaches this app |

## What to mount

The app backs up what it can see, at the paths it sees. Mount folders at the **same path** as on the host so the
paths in jobs, restores and Backrest agree. For database dumps mount the Docker socket and add the docker group
(`group_add`). Run it as the user that owns your files (`user: "1000:1000"`) so dumps and restores belong to you.
SFTP repositories use `ssh`: set `HOME` to a folder with `.ssh` (keys and `known_hosts`).
