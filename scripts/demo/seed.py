#!/usr/bin/env python3
"""Seeds a demo Pocket Backups through its API (see make.sh): real sources, a real local repository and real runs."""
import json, os, sys, time, urllib.request, http.cookiejar

BASE, D = sys.argv[1].rstrip("/"), sys.argv[2]
F = D + "/files"
jar = http.cookiejar.CookieJar()
op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))


def api(m, p, b=None):
    r = urllib.request.Request(BASE + "/api/v1" + p, method=m, data=json.dumps(b).encode() if b is not None else None,
                               headers={"Content-Type": "application/json"})
    with op.open(r, timeout=600) as x:
        d = x.read()
        return json.loads(d) if d else None


def wait(run):
    while True:
        r = api("GET", f"/runs/{run['id']}")["run"]
        if r["status"] != "running":
            return r
        time.sleep(0.3)


api("POST", "/auth/login", {"email": "ana@example.com", "password": "demo-password"})
api("PUT", "/settings", {"notify_url": ""})

conns = {}
for name, folder in (("TrueNAS", "nas"), ("Google Drive", "gdrive"), ("Backblaze B2", "b2")):
    conns[name] = api("POST", "/connections", {"name": name, "kind": "rclone", "options": f"type = alias\nremote = {D}/remotes/{folder}"})["id"]

repo = api("POST", "/repositories", {"name": "This box", "location": "local", "path": D + "/app/repo", "create": True,
                                     "options": {"compression": "max"}})
wait(repo["run"])
rid = repo["repository"]["id"]

src = {}
def add(name, kind, project="", **cfg):
    src[name] = api("POST", "/sources", {"name": name, "kind": kind, "project": project, "config": cfg})["id"]

add("Documents", "paths", "", paths=[F + "/Documents"])
add("Photos", "paths", "", paths=[F + "/Photos"], excludes=["*.tmp", "**/thumbs"])
add("Recipes", "paths", "", paths=[F + "/Recipes"])
add("Apps", "paths", "", paths=[F + "/srv"], excludes=["**/cache", "**/logs"])
for app in ("money", "kitchen", "habits"):
    add(f"Pocket {app.capitalize()}", "sqlite", f"pocket-{app}", path=f"{F}/pocket-{app}.db", output=f"db/pocket-{app}-db.sqlite3")

job = api("POST", "/jobs", {"name": "Nightly", "kind": "backup", "repository_id": rid, "schedule": "15 3 * * *", "source_ids": list(src.values()),
                            "config": {"staging_dir": D + "/app/staging/nightly", "retention": {"keep_daily": 7, "keep_weekly": 4, "keep_monthly": 6, "prune": True},
                                       "verify_after": True, "notify_success": True, "notify_failure": True, "coverage_check": False,
                                       "heartbeat_url": ""}})
jid = job["id"]
for name, path, opts in (("NAS", "backups/restic-repo", {"transfers": 4}), ("Google Drive", "Backups/restic-repo", {"transfers": 4}),
                         ("Backblaze B2", "home-server-backups/restic-repo", {"transfers": 4, "hard_delete": True})):
    api("POST", f"/jobs/{jid}/copies", {"name": name, "mode": "mirror", "connection_id": conns[{"NAS": "TrueNAS"}.get(name, name)], "path": path, "enabled": True, "options": opts})

for i in range(4):
    with open(f"{F}/Documents/notes-{i}.txt", "w") as f:
        f.write("week notes " * (200 * (i + 1)))
    r = wait(api("POST", f"/jobs/{jid}/run"))
    print("run", r["id"], r["status"], r["summary"])
print("seeded")
