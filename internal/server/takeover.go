package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// Taking over from Backrest: a job that runs on Backrest becomes one this
// app runs itself, with the plan's folders, excludes, schedule and
// retention, and the repository's check and prune schedules. Backrest's
// last backup comes along as a run (so "last good backup" stays true), and
// the plan is switched off in Backrest (schedule disabled, hooks and the
// repository's check/prune removed) so nothing runs twice. The restic
// repositories are the same; nothing is copied.

type takeoverResult struct {
	Plan     string   `json:"plan"`
	Job      int64    `json:"job_id"`
	Schedule string   `json:"schedule"`
	Paths    int      `json:"paths"`
	Excludes int      `json:"excludes"`
	Notes    []string `json:"notes"`
}

func retentionFromBackrest(raw json.RawMessage) (store.Retention, bool) {
	var p struct {
		KeepLastN    int  `json:"policyKeepLastN"`
		KeepAll      bool `json:"policyKeepAll"`
		TimeBucketed *struct {
			Hourly, Daily, Weekly, Monthly, Yearly int
			KeepLastN                              int `json:"keepLastN"`
		} `json:"policyTimeBucketed"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil {
		return store.Retention{}, false
	}
	switch {
	case p.KeepAll:
		return store.Retention{KeepAll: true}, true
	case p.KeepLastN > 0:
		return store.Retention{KeepLast: p.KeepLastN}, true
	case p.TimeBucketed != nil:
		t := p.TimeBucketed
		return store.Retention{KeepLast: t.KeepLastN, KeepHourly: t.Hourly, KeepDaily: t.Daily, KeepWeekly: t.Weekly, KeepMonthly: t.Monthly, KeepYearly: t.Yearly}, true
	}
	return store.Retention{}, false
}

func cronOf(raw any) string {
	m, _ := raw.(map[string]any)
	if m == nil {
		return ""
	}
	if d, _ := m["disabled"].(bool); d {
		return ""
	}
	c, _ := m["cron"].(string)
	return c
}

func (s *Server) handleBackrestTakeover(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in struct {
		Plans []string `json:"plans"` // empty = every plan a job runs on
	}
	_ = decode(r, &in)
	cfg, err := s.br.GetConfig(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	want := map[string]bool{}
	for _, p := range in.Plans {
		want[p] = true
	}
	jobs, _ := s.store.ListJobs(ctx)
	results := []takeoverResult{}
	rawPlans, _ := cfg.Raw["plans"].([]any)
	rawRepos, _ := cfg.Raw["repos"].([]any)
	takenRepos := map[string]bool{}
	for i := range jobs {
		j := &jobs[i]
		if j.Engine != "backrest" || j.BackrestPlan == "" || (len(want) > 0 && !want[j.BackrestPlan]) {
			continue
		}
		var plan *engine.BRPlan
		for k := range cfg.Plans {
			if cfg.Plans[k].ID == j.BackrestPlan {
				plan = &cfg.Plans[k]
			}
		}
		if plan == nil {
			results = append(results, takeoverResult{Plan: j.BackrestPlan, Job: j.ID, Notes: []string{"Backrest has no such plan any more — left as it is"}})
			continue
		}
		res := takeoverResult{Plan: plan.ID, Job: j.ID, Notes: []string{}}
		// the history first, while the job still points at the plan
		if last := s.backrestLastBackup(ctx, plan.ID); last != nil {
			last.JobID, last.RepositoryID = &j.ID, j.RepositoryID
			last.Title = j.Name + " (Backrest)"
			if err := s.store.CreateRun(ctx, last); err == nil {
				_ = s.store.UpdateRun(ctx, last)
				res.Notes = append(res.Notes, "Backrest's last backup ("+last.FinishedAt+") kept as a run")
			}
		}
		j.Engine, j.Schedule = "pocket", plan.Cron()
		j.Config.Paths, j.Config.Excludes = append([]string{}, plan.Paths...), append([]string{}, plan.Excludes...)
		tag := "plan:" + plan.ID
		if len(j.Config.Tags) == 0 || j.Config.Tags[0] != tag {
			j.Config.Tags = append([]string{tag}, j.Config.Tags...)
		}
		if ret, ok := retentionFromBackrest(plan.Retention); ok && j.Config.Retention.Empty() && !j.Config.Retention.KeepAll {
			j.Config.Retention = ret
			res.Notes = append(res.Notes, "retention: "+retentionText(ret))
		}
		if err := s.store.SaveJob(ctx, j); err != nil {
			s.fail(w, err, "save job")
			return
		}
		res.Schedule, res.Paths, res.Excludes = j.Schedule, len(j.Config.Paths), len(j.Config.Excludes)
		// switch the plan off in Backrest
		for _, rp := range rawPlans {
			p, _ := rp.(map[string]any)
			if p != nil && p["id"] == plan.ID {
				p["schedule"] = map[string]any{"disabled": true}
				p["hooks"] = []any{}
			}
		}
		takenRepos[plan.Repo] = true
		results = append(results, res)
	}
	// the repositories: their check and prune schedules move here
	for _, rr := range rawRepos {
		x, _ := rr.(map[string]any)
		if x == nil {
			continue
		}
		id, _ := x["id"].(string)
		if !takenRepos[id] {
			continue
		}
		repo, err := s.store.GetRepositoryByBackrest(ctx, id)
		if err != nil {
			continue
		}
		if cp, _ := x["checkPolicy"].(map[string]any); cp != nil {
			repo.CheckSchedule = cronOf(cp["schedule"])
			if pct, ok := cp["readDataSubsetPercent"].(float64); ok {
				repo.CheckReadPct = int(pct)
			}
			x["checkPolicy"] = map[string]any{"schedule": map[string]any{"disabled": true}}
		}
		if pp, _ := x["prunePolicy"].(map[string]any); pp != nil {
			repo.PruneSchedule = cronOf(pp["schedule"])
			x["prunePolicy"] = map[string]any{"schedule": map[string]any{"disabled": true}}
		}
		x["hooks"] = []any{}
		repo.ManagedBy = "pocket"
		if repo.Location == "usb" && repo.Options.USB != nil {
			repo.Options.Background = true // like usb-backup.sh: lowest CPU and disk priority
		}
		if err := s.store.SaveRepository(ctx, repo); err != nil {
			s.fail(w, err, "save repository")
			return
		}
	}
	if len(results) > 0 {
		if err := s.br.SetConfig(ctx, cfg.Raw); err != nil {
			writeErr(w, http.StatusBadGateway, "the jobs are taken over, but switching the plans off in Backrest failed — switch them off there by hand: "+err.Error())
			return
		}
	}
	s.touch()
	s.coverageChanged()
	s.log.Info("took over from Backrest", "plans", len(results), "by", userFrom(ctx).Email)
	writeJSON(w, http.StatusOK, map[string]any{"taken_over": results})
}

// ---------- the schedule ----------

type scheduled struct {
	At       string `json:"at"`
	Kind     string `json:"kind"` // backup | pull | check | prune
	Title    string `json:"title"`
	JobID    *int64 `json:"job_id,omitempty"`
	RepoID   *int64 `json:"repository_id,omitempty"`
	Schedule string `json:"schedule"`
}

type recurring struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Schedule string `json:"schedule"` // cron; "" with Trigger set = not on a clock
	Trigger  string `json:"trigger,omitempty"`
	NextRun  string `json:"next_run,omitempty"`
	JobID    *int64 `json:"job_id,omitempty"`
	RepoID   *int64 `json:"repository_id,omitempty"`
	Detail   string `json:"detail,omitempty"`
	RunsBy   string `json:"runs_by"` // pocket | backrest
	Enabled  bool   `json:"enabled"`
}

// handleSchedule lists everything that runs on a clock (or when a disk is
// plugged in) and what comes up in the next days.
func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	days := queryInt(r, "days")
	if days <= 0 || days > 62 {
		days = 14
	}
	now := time.Now()
	until := now.AddDate(0, 0, days)
	jobs, _ := s.store.ListJobs(ctx)
	repos, _ := s.store.ListRepositories(ctx)
	rec := []recurring{}
	upcoming := []scheduled{}
	add := func(kind, title, cron string, job, repo *int64, runsBy string, enabled bool, detail string) {
		rr := recurring{Kind: kind, Title: title, Schedule: cron, JobID: job, RepoID: repo, RunsBy: runsBy, Enabled: enabled, Detail: detail}
		if c, err := engine.ParseCron(cron); err == nil && enabled {
			t := now
			for n := 0; n < 200; n++ {
				t = c.Next(t)
				if t.IsZero() || t.After(until) {
					break
				}
				if rr.NextRun == "" {
					rr.NextRun = t.UTC().Format(time.RFC3339)
				}
				upcoming = append(upcoming, scheduled{At: t.UTC().Format(time.RFC3339), Kind: kind, Title: title, JobID: job, RepoID: repo, Schedule: cron})
			}
		}
		rec = append(rec, rr)
	}
	for i := range jobs {
		j := &jobs[i]
		id := j.ID
		runsBy := j.Engine
		if j.Schedule != "" {
			detail := ""
			if j.Kind == "backup" {
				detail = retentionText(j.Config.Retention)
			}
			add(j.Kind, j.Name, j.Schedule, &id, j.RepositoryID, runsBy, j.Enabled, detail)
		} else if j.Config.RunOnConnect {
			rec = append(rec, recurring{Kind: j.Kind, Title: j.Name, Trigger: fmt.Sprintf("when its disk is plugged in (at most once in %d days)", max(j.Config.RunOnConnectDays, 1)),
				JobID: &id, RepoID: j.RepositoryID, RunsBy: runsBy, Enabled: j.Enabled})
		} else {
			rec = append(rec, recurring{Kind: j.Kind, Title: j.Name, Trigger: "only when started by hand", JobID: &id, RepoID: j.RepositoryID, RunsBy: runsBy, Enabled: j.Enabled})
		}
	}
	for i := range repos {
		x := &repos[i]
		id := x.ID
		if x.CheckSchedule != "" {
			d := "structure"
			if x.CheckReadPct > 0 {
				d = fmt.Sprintf("structure and %d%% of the data", x.CheckReadPct)
			}
			add("check", "Check "+x.Name, x.CheckSchedule, nil, &id, x.ManagedBy, x.ManagedBy == "pocket", d)
		}
		if x.PruneSchedule != "" {
			add("prune", "Prune "+x.Name, x.PruneSchedule, nil, &id, x.ManagedBy, x.ManagedBy == "pocket", "")
		}
	}
	sortScheduled(upcoming)
	writeJSON(w, http.StatusOK, map[string]any{"recurring": rec, "upcoming": upcoming, "timezone": s.cfg.Timezone, "days": days})
}

func sortScheduled(v []scheduled) {
	sort.SliceStable(v, func(i, k int) bool { return v[i].At < v[k].At })
}
