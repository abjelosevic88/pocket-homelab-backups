package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/config"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

type harness struct {
	t   *testing.T
	srv *httptest.Server
}

func newHarness(t *testing.T) (*harness, *Server) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DataDir = t.TempDir()
	cfg.DBPath = cfg.DataDir + "/test.db"
	cfg.AuthMode = "local"
	cfg.StagingDir = cfg.DataDir + "/staging"
	cfg.TmpDir = cfg.DataDir + "/tmp"
	cfg.CacheDir = cfg.DataDir + "/cache"
	cfg.BrowseRoot = "/"
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open(cfg.DBPath, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(cfg, st, log, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Bootstrap(ctx); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, srv: httptest.NewServer(s.Router())}
	t.Cleanup(h.srv.Close)
	return h, s
}

// user makes an account and returns its API token.
func (h *harness) user(s *Server, email, role string) string {
	h.t.Helper()
	u := &store.User{Email: email, Name: email, Role: role}
	if err := s.store.CreateUser(context.Background(), u); err != nil {
		h.t.Fatal(err)
	}
	tok, _, err := s.store.CreateAPIToken(context.Background(), u.ID, "test")
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

func (h *harness) do(tok, method, path string, body any, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+"/api/v1"+path, rd)
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func (h *harness) raw(tok, path string) (int, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest("GET", h.srv.URL+"/api/v1"+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

// wait polls a run until it is no longer running.
func (h *harness) wait(tok string, id int64) store.Run {
	h.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var out struct {
			Run store.Run `json:"run"`
		}
		h.do(tok, "GET", "/runs/"+strconv.FormatInt(id, 10), nil, &out)
		if out.Run.Status != "" && out.Run.Status != "running" {
			return out.Run
		}
		time.Sleep(200 * time.Millisecond)
	}
	h.t.Fatalf("run %d did not finish", id)
	return store.Run{}
}

func TestMembersOnlySeeHealth(t *testing.T) {
	h, s := newHarness(t)
	admin := h.user(s, "owner@example.com", "admin")
	member := h.user(s, "member@example.com", "member")
	for _, p := range []string{"/jobs", "/repositories", "/sources", "/connections", "/runs", "/coverage", "/backrest", "/browse", "/integrations"} {
		if code := h.do(member, "GET", p, nil, nil); code != http.StatusForbidden {
			t.Errorf("member GET %s = %d, want 403", p, code)
		}
	}
	var ov map[string]any
	if code := h.do(member, "GET", "/overview", nil, &ov); code != 200 {
		t.Fatalf("member overview = %d", code)
	}
	if _, ok := ov["jobs"]; ok {
		t.Error("a member must not see the jobs")
	}
	if code := h.do(member, "POST", "/hooks/backrest", map[string]string{"event": "snapshot_start", "plan": "x"}, nil); code != http.StatusForbidden {
		t.Errorf("member hook = %d, want 403", code)
	}
	if code := h.do(admin, "GET", "/jobs", nil, nil); code != 200 {
		t.Errorf("admin jobs = %d", code)
	}
}

func makeSQLite(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE t (v TEXT); INSERT INTO t VALUES ('hello')`); err != nil {
		t.Fatal(err)
	}
}

func TestBackrestPrepHookAndCheckPause(t *testing.T) {
	h, s := newHarness(t)
	admin := h.user(s, "owner@example.com", "admin")
	dir := t.TempDir()
	makeSQLite(t, dir+"/app.db")
	var repo struct {
		Repository store.Repository `json:"repository"`
	}
	if code := h.do(admin, "POST", "/repositories", map[string]any{"name": "home-server", "location": "local", "path": dir + "/repo", "password": "x"}, &repo); code != 200 {
		t.Fatalf("repo = %d", code)
	}
	s.store.DB.Exec(`UPDATE repositories SET backrest_repo = 'home-server', managed_by = 'backrest' WHERE id = ?`, repo.Repository.ID)
	var src store.Source
	if code := h.do(admin, "POST", "/sources", map[string]any{"name": "app", "kind": "sqlite", "config": map[string]any{"path": dir + "/app.db", "output": "db/app-db.sqlite3"}}, &src); code != 200 {
		t.Fatalf("source = %d", code)
	}
	staging := dir + "/staging"
	var job map[string]any
	if code := h.do(admin, "POST", "/jobs", map[string]any{"name": "Nightly", "kind": "backup", "engine": "backrest", "backrest_plan": "nightly",
		"repository_id": repo.Repository.ID, "source_ids": []int64{src.ID}, "config": map[string]any{"staging_dir": staging, "notify_failure": true}}, &job); code != 200 {
		t.Fatalf("job = %d %v", code, job)
	}
	var res map[string]any
	if code := h.do(admin, "POST", "/hooks/backrest", map[string]string{"event": "snapshot_start", "plan": "nightly"}, &res); code != 200 {
		t.Fatalf("prep hook = %d %v", code, res)
	}
	db, err := sql.Open("sqlite", staging+"/db/app-db.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	var v string
	if err := db.QueryRow(`SELECT v FROM t`).Scan(&v); err != nil || v != "hello" {
		t.Fatalf("snapshot of the database: %q %v", v, err)
	}
	db.Close()
	// a failed check pauses the copies, a passing one resumes them
	h.do(admin, "POST", "/hooks/backrest", map[string]string{"event": "check_error", "repo": "home-server", "error": "pack 1 damaged"}, nil)
	x, _ := s.store.GetRepository(context.Background(), repo.Repository.ID)
	if !x.CopiesPaused {
		t.Fatal("copies should be paused after a failed check")
	}
	h.do(admin, "POST", "/hooks/backrest", map[string]string{"event": "check_success", "repo": "home-server"}, nil)
	x, _ = s.store.GetRepository(context.Background(), repo.Repository.ID)
	if x.CopiesPaused {
		t.Fatal("a passing check should resume the copies")
	}
	// a stopped share cancels the snapshot
	h.do(admin, "PUT", "/jobs/"+strconv.FormatInt(int64(job["id"].(float64)), 10), map[string]any{"config": map[string]any{"staging_dir": staging, "require_mounts": []string{dir + "/not-a-mount"}}}, nil)
	if code := h.do(admin, "POST", "/hooks/backrest", map[string]string{"event": "snapshot_start", "plan": "nightly"}, nil); code != http.StatusConflict {
		t.Fatalf("prep with a missing mount = %d, want 409", code)
	}
}

// TestLocalBackupEndToEnd runs the whole pocket engine against a real
// restic (skipped where restic is not installed).
func TestLocalBackupEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not installed")
	}
	h, s := newHarness(t)
	admin := h.user(s, "owner@example.com", "admin")
	dir := t.TempDir()
	os.MkdirAll(dir+"/docs/sub", 0o755)
	os.WriteFile(dir+"/docs/a.txt", []byte("first file"), 0o644)
	os.WriteFile(dir+"/docs/sub/b.txt", []byte("second file"), 0o644)
	os.WriteFile(dir+"/docs/skip.log", []byte("x"), 0o644)
	makeSQLite(t, dir+"/live.db")

	var repo struct {
		Repository store.Repository `json:"repository"`
		Run        *store.Run       `json:"run"`
	}
	if code := h.do(admin, "POST", "/repositories", map[string]any{"name": "Local", "location": "local", "path": dir + "/repo", "create": true}, &repo); code != 200 || repo.Run == nil {
		t.Fatalf("create repo = %d %+v", code, repo)
	}
	if r := h.wait(admin, repo.Run.ID); r.Status != "success" {
		t.Fatalf("init = %s %s", r.Status, r.Error)
	}
	var folders, db store.Source
	h.do(admin, "POST", "/sources", map[string]any{"name": "Docs", "kind": "paths", "config": map[string]any{"paths": []string{dir + "/docs"}, "excludes": []string{"*.log"}}}, &folders)
	h.do(admin, "POST", "/sources", map[string]any{"name": "Live", "kind": "sqlite", "config": map[string]any{"path": dir + "/live.db"}}, &db)
	var job struct {
		ID int64 `json:"id"`
	}
	if code := h.do(admin, "POST", "/jobs", map[string]any{"name": "Daily", "kind": "backup", "repository_id": repo.Repository.ID, "source_ids": []int64{folders.ID, db.ID},
		"config": map[string]any{"staging_dir": dir + "/staging", "retention": map[string]any{"keep_last": 1, "prune": true}, "verify_after": true}}, &job); code != 200 {
		t.Fatalf("job = %d", code)
	}
	var cp store.Copy
	if code := h.do(admin, "POST", "/jobs/"+strconv.FormatInt(job.ID, 10)+"/copies", map[string]any{"name": "Mirror", "mode": "mirror", "path": dir + "/mirror", "enabled": true}, &cp); code != 200 {
		t.Fatalf("copy = %d", code)
	}
	for i := 0; i < 2; i++ {
		var run store.Run
		if code := h.do(admin, "POST", "/jobs/"+strconv.FormatInt(job.ID, 10)+"/run", nil, &run); code != http.StatusAccepted {
			t.Fatalf("run = %d", code)
		}
		r := h.wait(admin, run.ID)
		if r.Status != "success" || r.SnapshotID == "" {
			full, _ := s.store.GetRun(context.Background(), run.ID)
			t.Fatalf("backup = %s %s\n%s", r.Status, r.Error, full.Log)
		}
	}
	if _, err := os.Stat(dir + "/mirror/config"); err != nil {
		t.Fatal("the mirror should hold the repository")
	}
	var snaps []map[string]any
	h.do(admin, "GET", "/repositories/"+strconv.FormatInt(repo.Repository.ID, 10)+"/snapshots?fresh=1", nil, &snaps)
	if len(snaps) != 1 {
		t.Fatalf("retention keep_last 1 left %d snapshots", len(snaps))
	}
	id := snaps[0]["id"].(string)
	var ls struct {
		Entries []map[string]any `json:"entries"`
	}
	h.do(admin, "GET", "/repositories/"+strconv.FormatInt(repo.Repository.ID, 10)+"/ls?snapshot="+id+"&path="+url.QueryEscape(dir+"/docs"), nil, &ls)
	names := []string{}
	for _, e := range ls.Entries {
		names = append(names, e["name"].(string))
	}
	if strings.Join(names, ",") != "sub,a.txt" {
		t.Fatalf("ls = %v (skip.log must be excluded)", names)
	}
	code, body := h.raw(admin, "/repositories/"+strconv.FormatInt(repo.Repository.ID, 10)+"/download?snapshot="+id+"&path="+url.QueryEscape(dir+"/docs/a.txt"))
	if code != 200 || string(body) != "first file" {
		t.Fatalf("download = %d %q", code, body)
	}
	var rr store.Run
	if code := h.do(admin, "POST", "/repositories/"+strconv.FormatInt(repo.Repository.ID, 10)+"/restore", map[string]any{"snapshot": id, "paths": []string{dir + "/docs/sub"}, "target": dir + "/restored"}, &rr); code != http.StatusAccepted {
		t.Fatalf("restore = %d", code)
	}
	if r := h.wait(admin, rr.ID); r.Status != "success" {
		t.Fatalf("restore = %s %s", r.Status, r.Error)
	}
	if b, err := os.ReadFile(dir + "/restored" + dir + "/docs/sub/b.txt"); err != nil || string(b) != "second file" {
		t.Fatalf("restored file: %q %v", b, err)
	}
}

func TestHookCommand(t *testing.T) {
	_, s := newHarness(t)
	s.cfg.HookBaseURL = "http://127.0.0.1:8203"
	cmd := s.hookCommand("pbk_x", "snapshot_success", "nightly", "")
	for _, want := range []string{"Bearer pbk_x", "event=snapshot_success", "plan=nightly", "snapshot={{ .SnapshotId }}", "http://127.0.0.1:8203/api/v1/hooks/backrest"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("hook command lacks %q: %s", want, cmd)
		}
	}
}
