#!/usr/bin/env bash
# A throwaway Pocket Backups with demo data for the README screenshots (never screenshot a real box):
# a few folders and SQLite databases, a real local restic repository with a week of runs, copies to three
# "clouds" (rclone aliases to local folders, dressed up afterwards as a NAS, Google Drive and Backblaze B2).
#
#   scripts/demo/make.sh            # builds, seeds, leaves it running on :18299 (ana@example.com / demo-password)
#   BASE=http://127.0.0.1:18299 CDP=… node scripts/screenshots.mjs
set -euo pipefail
cd "$(dirname "$0")/../.."
D=${DEMO_DIR:-$HOME/.cache/pocket-backups-demo}
PORT=${PORT:-18299}
pid=$(ss -ltnp 2>/dev/null | grep ":$PORT " | sed -E 's/.*pid=([0-9]+).*/\1/' || true)
[ -n "$pid" ] && kill "$pid" && sleep 1
rm -rf "$D" && mkdir -p "$D"/{app,files,remotes/nas,remotes/gdrive,remotes/b2}
go build -ldflags "-X main.version=0.1.0" -o "$D/pocket-backups" ./cmd/pocket-backups

# things to protect
F="$D/files"
mkdir -p "$F/srv/paperless" "$F/srv/immich" "$F/srv/nextcloud" "$F/srv/vaultwarden/data" "$F/srv/homepage"
mkdir -p "$F/Documents/Taxes 2025" "$F/Documents/Contracts" "$F/Photos/2026/Summer in Kotor" "$F/Photos/2026/Winter" "$F/Recipes"
for i in $(seq 1 12); do head -c $((RANDOM * 20)) /dev/urandom > "$F/Photos/2026/Summer in Kotor/IMG_$((4100 + i)).jpg"; done
for i in $(seq 1 6); do head -c $((RANDOM * 15)) /dev/urandom > "$F/Photos/2026/Winter/IMG_$((5200 + i)).heic"; done
printf 'Rental agreement — Apartment 4B\n' > "$F/Documents/Contracts/Rental agreement.pdf"
printf 'Insurance policy 2026\n' > "$F/Documents/Contracts/Car insurance.pdf"
for m in 01 02 03 04 05 06; do printf 'Invoice %s/2025\n' "$m" > "$F/Documents/Taxes 2025/Invoice-2025-$m.pdf"; done
printf '# Grandma'\''s sarma\n' > "$F/Recipes/sarma.md"
python3 - "$F" <<'PY'
import sqlite3, sys
f = sys.argv[1]
for name, rows in (("money", 4000), ("kitchen", 600), ("habits", 900)):
    db = sqlite3.connect(f"{f}/pocket-{name}.db"); db.execute("PRAGMA journal_mode=WAL")
    db.execute("CREATE TABLE IF NOT EXISTS t (id INTEGER PRIMARY KEY, v TEXT)")
    db.executemany("INSERT INTO t (v) VALUES (?)", [(f"{name} row {i} " + "x" * 80,) for i in range(rows)]); db.commit(); db.close()
PY

start() {
  (DATA_DIR="$D/app" PORT=$PORT HOST=127.0.0.1 ADMIN_EMAIL=ana@example.com ADMIN_PASSWORD=demo-password ADMIN_NAME="Ana Petrović" \
    DOCKER_BIN="$PWD/scripts/demo/fake-docker" DEMO_FILES="$F" \
    STAGING_DIR="$D/app/staging" TMP_DIR="$D/app/tmp" SNAPSHOT_HOST=home-server TZ=Europe/Berlin BASE_URL=http://127.0.0.1:$PORT \
    nohup "$D/pocket-backups" > "$D/server.log" 2>&1 &)
  for _ in $(seq 1 50); do curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && return; sleep 0.2; done
}
start
python3 scripts/demo/seed.py "http://127.0.0.1:$PORT" "$D"
pid=$(ss -ltnp 2>/dev/null | grep ":$PORT " | sed -E 's/.*pid=([0-9]+).*/\1/'); kill "$pid"; sleep 1
python3 scripts/demo/dress.py "$D/app/pocket-backups.db" "$D"
start
echo "demo running on http://127.0.0.1:$PORT (ana@example.com / demo-password)"
