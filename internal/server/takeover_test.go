package server

import (
	"encoding/json"
	"testing"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

func TestRetentionFromBackrest(t *testing.T) {
	cases := map[string]string{
		`{"policyKeepLastN": 1}`: "keep 1 last",
		`{"policyTimeBucketed": {"daily": 14, "weekly": 8, "monthly": 6}}`: "keep 14 daily, 8 weekly, 6 monthly",
		`{"policyKeepAll": true}`: "keep everything",
	}
	for raw, want := range cases {
		r, ok := retentionFromBackrest(json.RawMessage(raw))
		if !ok || retentionText(r) != want {
			t.Errorf("%s → %q (%v), want %q", raw, retentionText(r), ok, want)
		}
	}
}

func TestScheduleLists(t *testing.T) {
	h, s := newHarness(t)
	admin := h.user(s, "owner@example.com", "admin")
	var repo struct {
		Repository struct{ ID int64 } `json:"repository"`
	}
	h.do(admin, "POST", "/repositories", map[string]any{"name": "R", "location": "local", "path": t.TempDir() + "/r", "password": "x",
		"check_schedule": "0 2 * * 0", "check_read_pct": 100, "prune_schedule": "45 4 * * 0"}, &repo)
	h.do(admin, "POST", "/jobs", map[string]any{"name": "Nightly", "kind": "backup", "repository_id": repo.Repository.ID, "schedule": "15 4 * * *",
		"config": map[string]any{"paths": []string{t.TempDir()}}}, nil)
	var out struct {
		Recurring []recurring `json:"recurring"`
		Upcoming  []scheduled `json:"upcoming"`
	}
	if code := h.do(admin, "GET", "/schedule?days=7", nil, &out); code != 200 {
		t.Fatalf("schedule = %d", code)
	}
	if len(out.Recurring) != 3 {
		t.Fatalf("recurring = %d, want job + check + prune", len(out.Recurring))
	}
	nightly := 0
	for i, u := range out.Upcoming {
		if u.Title == "Nightly" {
			nightly++
		}
		if i > 0 && u.At < out.Upcoming[i-1].At {
			t.Fatal("upcoming is not in time order")
		}
	}
	if nightly < 6 || nightly > 8 {
		t.Fatalf("a daily job should come up 7 times in 7 days, got %d", nightly)
	}
}

func TestScheduledJobsContract(t *testing.T) {
	h, s := newHarness(t)
	admin := h.user(s, "owner@example.com", "admin")
	member := h.user(s, "member@example.com", "member")
	var repo struct {
		Repository struct{ ID int64 } `json:"repository"`
	}
	h.do(admin, "POST", "/repositories", map[string]any{"name": "R", "location": "local", "path": t.TempDir() + "/r", "password": "x",
		"check_schedule": "0 2 * * 0", "prune_schedule": "45 4 * * 0"}, &repo)
	h.do(admin, "POST", "/jobs", map[string]any{"name": "Nightly", "kind": "backup", "repository_id": repo.Repository.ID, "schedule": "15 4 * * *",
		"config": map[string]any{"paths": []string{t.TempDir()}}}, nil)
	var out struct {
		Jobs []jobOut `json:"jobs"`
		Runs []jobRun `json:"runs"`
	}
	if code := h.do(admin, "GET", "/scheduled-jobs", nil, &out); code != 200 || len(out.Jobs) != 3 {
		t.Fatalf("scheduled jobs = %d, %d jobs", code, len(out.Jobs))
	}
	j := out.Jobs[0]
	if j.Name != "Nightly" || j.Schedule != "every day at 04:15" || !j.Enabled || j.Next == 0 || !j.CanRun {
		t.Fatalf("job %+v", j)
	}
	if out.Jobs[1].Schedule != "every Sunday at 02:00" || out.Jobs[2].Name != "Prune R" {
		t.Fatalf("repo jobs %+v", out.Jobs[1:])
	}
	var none struct {
		Jobs []jobOut `json:"jobs"`
	}
	if h.do(member, "GET", "/scheduled-jobs", nil, &none); len(none.Jobs) != 0 {
		t.Fatal("a member sees the box's backups")
	}
	if code := h.do(member, "POST", "/scheduled-jobs/"+j.ID+"/run", nil, nil); code != 403 {
		t.Fatalf("member run = %d", code)
	}
	if code := h.do(admin, "POST", "/scheduled-jobs/nope-1/run", nil, nil); code != 404 {
		t.Fatalf("unknown = %d", code)
	}
	if code := h.do(admin, "POST", "/scheduled-jobs/"+j.ID+"/run", nil, nil); code != 202 {
		t.Fatalf("run = %d", code)
	}
	var runs []store.Run
	h.do(admin, "GET", "/runs", nil, &runs)
	if len(runs) > 0 {
		h.wait(admin, runs[0].ID)
	}
	h.do(admin, "GET", "/scheduled-jobs?since=0", nil, &out)
	if out.Jobs[0].Last == nil || len(out.Runs) == 0 || out.Runs[0].Job != j.ID {
		t.Fatalf("after a run: last %+v runs %+v", out.Jobs[0].Last, out.Runs)
	}
}
