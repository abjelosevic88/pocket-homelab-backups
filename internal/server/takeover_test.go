package server

import (
	"encoding/json"
	"testing"
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
