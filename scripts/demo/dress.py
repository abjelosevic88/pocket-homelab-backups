#!/usr/bin/env python3
"""Dresses the stopped demo database up for screenshots: the alias "clouds" become a NAS, Google Drive and B2,
an offline USB disk and a Google Drive pull appear, the runs spread over the past nights."""
import json, sqlite3, sys
from datetime import datetime, timedelta, timezone

db = sqlite3.connect(sys.argv[1])
D = sys.argv[2]
now = datetime.now(timezone.utc).replace(microsecond=0)
iso = lambda t: t.strftime("%Y-%m-%dT%H:%M:%SZ")

db.execute("UPDATE connections SET kind='sftp', config=?, status='ok', checked_at=? WHERE name='TrueNAS'",
           (json.dumps({"host": "192.168.1.20", "user": "ana", "key_file": "/home/ana/.ssh/id_ed25519"}), iso(now)))
db.execute("UPDATE connections SET kind='gdrive', config=?, status='ok', checked_at=? WHERE name='Google Drive'",
           (json.dumps({"scope": "drive", "account": "ana.petrovic@gmail.com"}), iso(now)))
db.execute("UPDATE connections SET kind='b2', config=?, status='ok', checked_at=? WHERE name='Backblaze B2'",
           (json.dumps({"account": "0051f2e9a7c3", "bucket": "home-server-backups"}), iso(now)))
db.execute("INSERT INTO connections (name, kind, config, status, checked_at) VALUES ('Pocket Files', 'pocketfiles', ?, 'ok', ?)",
           (json.dumps({"url": "http://pocket-files:8080", "account": "ana@example.com"}), iso(now)))

# an offline USB disk, last used two weeks ago
db.execute("""INSERT INTO repositories (name, location, path, options, managed_by, stats, status) VALUES ('Offline USB disk', 'usb', '/mnt/usbbackup/restic-repo', ?, 'pocket', ?, 'ok')""",
           (json.dumps({"usb": {"uuid": "93c05053-a3a0-4710-8708-2c7084e5c211", "label": "usbbackup", "mountpoint": "/mnt/usbbackup", "power_off": True, "warn_pct": 85}}),
            json.dumps({"size": 1_984_221_331_456, "snapshots": 9, "latest": iso(now - timedelta(days=13)), "disk_free": 1_210_000_000_000, "disk_used_pct": 66, "updated_at": iso(now - timedelta(days=13))})))
usb = db.execute("SELECT id FROM repositories WHERE name='Offline USB disk'").fetchone()[0]
db.execute("""INSERT INTO jobs (name, kind, engine, repository_id, schedule, enabled, config, sort_order) VALUES ('Offline USB disk', 'backup', 'pocket', ?, '', 1, ?, 2)""",
           (usb, json.dumps({"run_on_connect": True, "run_on_connect_days": 7, "check_read_pct": 5, "notify_success": True, "notify_failure": True,
                             "retention": {"keep_last": 3, "keep_monthly": 12, "keep_yearly": 3, "prune": True}})))
usbjob = db.execute("SELECT id FROM jobs WHERE name='Offline USB disk'").fetchone()[0]
gd = db.execute("SELECT id FROM connections WHERE name='Google Drive'").fetchone()[0]
db.execute("""INSERT INTO jobs (name, kind, engine, schedule, enabled, config, sort_order) VALUES ('Google Drive → NAS', 'pull', 'pocket', '0 4 * * 0', 1, ?, 3)""",
           (json.dumps({"notify_failure": True, "pull": {"connection_id": gd, "remote_path": "", "local_path": "/mnt/nas/gdrive/drive", "shared_with_me": True,
                                                         "shared_path": "/mnt/nas/gdrive/shared-with-me", "export_formats": "docx,xlsx,pptx,svg", "tps_limit": 10, "transfers": 8}}),))
pull = db.execute("SELECT id FROM jobs WHERE name='Google Drive → NAS'").fetchone()[0]

# the real runs become the last four nights at 03:15
runs = db.execute("SELECT id FROM runs WHERE kind='backup' ORDER BY id").fetchall()
for i, (rid,) in enumerate(reversed(runs)):
    start = (now - timedelta(days=i)).replace(hour=1, minute=15, second=4)
    if start > now:
        start -= timedelta(days=1)
    db.execute("UPDATE runs SET started_at=?, finished_at=?, trigger='schedule', user_id=NULL WHERE id=?", (iso(start), iso(start + timedelta(seconds=97)), rid))
db.execute("DELETE FROM runs WHERE kind IN ('init')")

def run(job, repo, kind, title, start, secs, status, summary, steps, snap="", added=0, total=0):
    db.execute("""INSERT INTO runs (job_id, repository_id, kind, trigger, status, title, started_at, finished_at, duration_ms, snapshot_id, bytes_added, bytes_total, summary, steps)
                  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
               (job, repo, kind, "disk" if kind == "backup" else "schedule", status, title, iso(start), iso(start + timedelta(seconds=secs)), secs * 1000, snap, added, total, summary, json.dumps(steps)))

t = now - timedelta(days=13, hours=5)
run(usbjob, usb, "backup", "Offline USB disk", t, 2*3600 + 714, "success", "snapshot 9e41c07b · repository 1.8 TiB · disk 66% full",
    [{"name": "Mount the disk", "status": "ok", "ms": 2100, "detail": "/mnt/usbbackup"}, {"name": "Snapshot", "status": "ok", "ms": 7_210_000, "detail": "9e41c07b · 3.1 GiB added · 2.4 TiB in all"},
     {"name": "Retention", "status": "ok", "ms": 41_000, "detail": "keep 3 last, 12 monthly, 3 yearly, pruned"}, {"name": "Check", "status": "ok", "ms": 380_000, "detail": "read 5% of the data"},
     {"name": "Unmount the disk", "status": "ok", "ms": 3100, "detail": "safe to unplug"}], "9e41c07b", 3_330_000_000, 2_640_000_000_000)
for w in range(3):
    t = (now - timedelta(days=now.weekday() + 1 + 7 * w)).replace(hour=2, minute=0, second=0)
    run(pull, None, "pull", "Google Drive → NAS", t, 780 - w * 40, "success", "copied Google Drive to /mnt/nas/gdrive/drive",
        [{"name": "Check the folder", "status": "ok", "ms": 12, "detail": "/mnt/nas/gdrive"}, {"name": "Copy Google Drive", "status": "ok", "ms": 103_000, "detail": "c2: → /mnt/nas/gdrive/drive"},
         {"name": "Copy Shared with me", "status": "ok", "ms": 650_000, "detail": "→ /mnt/nas/gdrive/shared-with-me"}])
for (cid,) in db.execute("SELECT id FROM copies").fetchall():
    db.execute("UPDATE copies SET last_ok_at=? WHERE id=?", (iso(now.replace(hour=1, minute=16) if now.hour >= 2 else now - timedelta(hours=10)), cid))
db.execute("INSERT OR REPLACE INTO coverage_skips (project, reason) VALUES ('it-tools', 'stateless: nothing to keep')")
db.commit()
print("dressed")
