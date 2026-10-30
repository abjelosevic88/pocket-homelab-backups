package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// ---- scheduled jobs, for Pocket Monitoring's Jobs tab ----
//
// The jobs contract (docs/jobs.md in pocket-homelab-monitoring): every backup
// job and every repository check and prune this app runs, when it ran, how it
// went and when it runs next. Backups are the box's, so admins only.

type jobRun struct {
	Job     string `json:"job"`
	ID      string `json:"id"`
	Start   int64  `json:"start"`
	End     int64  `json:"end"`
	OK      bool   `json:"ok"`
	Summary string `json:"summary"`
}

type jobOut struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Schedule    string  `json:"schedule"`
	Enabled     bool    `json:"enabled"`
	Next        int64   `json:"next"`
	Running     bool    `json:"running"`
	Started     int64   `json:"started"`
	Last        *jobRun `json:"last"`
	URL         string  `json:"url"`
	CanRun      bool    `json:"can_run"`
}

var weekdays = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// cronWords says the common schedules in words ("every day at 03:30");
// anything else stays a cron line.
func cronWords(c string) string {
	f := strings.Fields(c)
	if len(f) != 5 {
		return c
	}
	m, err1 := strconv.Atoi(f[0])
	h, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil || f[2] != "*" || f[3] != "*" {
		return "cron " + c
	}
	at := fmt.Sprintf("%02d:%02d", h, m)
	if f[4] == "*" {
		return "every day at " + at
	}
	if d, err := strconv.Atoi(f[4]); err == nil && d >= 0 && d <= 7 {
		return "every " + weekdays[d%7] + " at " + at
	}
	return "cron " + c
}

func cronNext(c string, enabled bool) int64 {
	if !enabled || c == "" {
		return 0
	}
	cr, err := engine.ParseCron(c)
	if err != nil {
		return 0
	}
	if n := cr.Next(time.Now()); !n.IsZero() {
		return n.UnixMilli()
	}
	return 0
}

func runMS(rfc string) int64 {
	if t, err := time.Parse(time.RFC3339Nano, rfc); err == nil {
		return t.UnixMilli()
	}
	return 0
}

// jobKey is the contract id a run belongs to.
func jobKey(r store.Run) string {
	switch {
	case r.JobID != nil && (r.Kind == "backup" || r.Kind == "pull"):
		return fmt.Sprintf("job-%d", *r.JobID)
	case r.RepositoryID != nil && (r.Kind == "check" || r.Kind == "prune"):
		return fmt.Sprintf("%s-%d", r.Kind, *r.RepositoryID)
	}
	return ""
}

func (s *Server) jobsList(ctx context.Context, since int64) ([]jobOut, []jobRun) {
	jobs := []jobOut{}
	runs := []jobRun{}
	list, _ := s.store.ListJobs(ctx)
	repos, _ := s.store.ListRepositories(ctx)
	for i := range list {
		j := &list[i]
		o := jobOut{ID: fmt.Sprintf("job-%d", j.ID), Name: j.Name, Enabled: j.Enabled, CanRun: j.Engine != "backrest", URL: fmt.Sprintf("/jobs/%d", j.ID),
			Description: map[string]string{"backup": "Backup", "pull": "Copy from another place"}[j.Kind]}
		switch {
		case j.Schedule != "":
			o.Schedule, o.Next = cronWords(j.Schedule), cronNext(j.Schedule, j.Enabled)
		case j.Config.RunOnConnect:
			o.Schedule = fmt.Sprintf("when its disk is plugged in (at most once in %d days)", max(j.Config.RunOnConnectDays, 1))
		default:
			o.Schedule = "only when started by hand"
		}
		jobs = append(jobs, o)
	}
	for i := range repos {
		x := &repos[i]
		pocket := x.ManagedBy != "backrest"
		if x.CheckSchedule != "" {
			d := "Checks the repository's structure"
			if x.CheckReadPct > 0 {
				d = fmt.Sprintf("Checks the repository's structure and reads %d%% of the data", x.CheckReadPct)
			}
			jobs = append(jobs, jobOut{ID: fmt.Sprintf("check-%d", x.ID), Name: "Check " + x.Name, Description: d, Schedule: cronWords(x.CheckSchedule),
				Enabled: pocket, Next: cronNext(x.CheckSchedule, pocket), CanRun: pocket, URL: fmt.Sprintf("/repositories/%d", x.ID)})
		}
		if x.PruneSchedule != "" {
			jobs = append(jobs, jobOut{ID: fmt.Sprintf("prune-%d", x.ID), Name: "Prune " + x.Name, Description: "Forgets snapshots past the retention and frees the space", Schedule: cronWords(x.PruneSchedule),
				Enabled: pocket, Next: cronNext(x.PruneSchedule, pocket), CanRun: pocket, URL: fmt.Sprintf("/repositories/%d", x.ID)})
		}
	}
	idx := map[string]int{}
	for i, j := range jobs {
		idx[j.ID] = i
	}
	recent, _ := s.store.ListRuns(ctx, store.RunFilter{Limit: 500})
	for _, r := range recent { // newest first
		key := jobKey(r)
		i, ok := idx[key]
		if !ok {
			continue
		}
		j := &jobs[i]
		if r.Status == "running" {
			if !j.Running {
				j.Running, j.Started = true, runMS(r.StartedAt)
			}
			continue
		}
		end := runMS(r.FinishedAt)
		if end == 0 {
			continue
		}
		jr := jobRun{Job: key, ID: fmt.Sprintf("run-%d", r.ID), Start: runMS(r.StartedAt), End: end, OK: r.Status != "failed" && r.Status != "cancelled",
			Summary: firstNonEmpty(r.Error, r.Summary, r.Status)}
		if r.Status == "warning" && r.Error == "" {
			jr.Summary = "warning: " + firstNonEmpty(r.Summary, "finished with warnings")
		}
		if j.Last == nil {
			last := jr
			j.Last = &last
		}
		if end > since {
			runs = append(runs, jr)
		}
	}
	sort.Slice(runs, func(a, b int) bool { return runs[a].End < runs[b].End })
	if len(runs) > 200 {
		runs = runs[len(runs)-200:]
	}
	return jobs, runs
}

func (s *Server) handleJobsContract(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r.Context()); u == nil || u.Role != "admin" {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []jobOut{}, "runs": []jobRun{}}) // backups are the box's
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	jobs, runs := s.jobsList(r.Context(), since)
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "runs": runs})
}

// handleRunJobContract starts a job, check or prune now.
func (s *Server) handleRunJobContract(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	jobs, _ := s.jobsList(ctx, 0)
	var job *jobOut
	for i := range jobs {
		if jobs[i].ID == id {
			job = &jobs[i]
		}
	}
	switch {
	case job == nil:
		writeErr(w, http.StatusNotFound, "no such job")
		return
	case !job.CanRun:
		writeErr(w, http.StatusForbidden, job.Name+" is not run by this app")
		return
	case job.Running:
		writeErr(w, http.StatusConflict, job.Name+" is running already")
		return
	}
	kind, num, _ := strings.Cut(id, "-")
	n, _ := strconv.ParseInt(num, 10, 64)
	var err error
	if kind == "job" {
		var j *store.Job
		if j, err = s.store.GetJob(ctx, n); err == nil {
			_, err = s.StartJob(ctx, j, "manual", me(r))
		}
	} else {
		var x *store.Repository
		if x, err = s.store.GetRepository(ctx, n); err == nil {
			_, err = s.StartRepoTask(ctx, x, kind, "manual", me(r), x.CheckReadPct)
		}
	}
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}
