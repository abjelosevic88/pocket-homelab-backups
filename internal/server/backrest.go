package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// Backrest underneath: its plans make the snapshots on their schedules and
// call this app from their hooks for everything before (dumps, mounting a
// disk, the coverage check) and after (retention, copies to the NAS and the
// clouds, the report, the heartbeat, telling people).

type hookIn struct {
	Event    string `json:"event"`
	Plan     string `json:"plan"`
	Repo     string `json:"repo"`
	Snapshot string `json:"snapshot"`
	Task     string `json:"task"`
	Error    string `json:"error"`
}

func readHook(r *http.Request) (hookIn, error) {
	var in hookIn
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return in, decode(r, &in)
	}
	if err := r.ParseForm(); err != nil {
		return in, err
	}
	in.Event, in.Plan, in.Repo = r.Form.Get("event"), r.Form.Get("plan"), r.Form.Get("repo")
	in.Snapshot, in.Task, in.Error = r.Form.Get("snapshot"), r.Form.Get("task"), r.Form.Get("error")
	return in, nil
}

// handleBackrestHook is what Backrest's hooks call. It answers when the
// work is done; 409 means "stop" (an ON_ERROR_CANCEL hook cancels the task).
func (s *Server) handleBackrestHook(w http.ResponseWriter, r *http.Request) {
	if u := userFrom(r.Context()); u == nil || u.Role != "admin" {
		writeErr(w, http.StatusForbidden, "hooks need an admin's API token")
		return
	}
	in, err := readHook(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid hook")
		return
	}
	in.Event = strings.ToLower(strings.TrimPrefix(strings.ToUpper(in.Event), "CONDITION_"))
	ctx := context.WithoutCancel(r.Context())
	s.log.Info("backrest hook", "event", in.Event, "plan", in.Plan, "repo", in.Repo, "snapshot", shortID(in.Snapshot))
	var job *store.Job
	if in.Plan != "" {
		job, _ = s.store.GetJobByPlan(ctx, in.Plan)
	}
	var repo *store.Repository
	switch {
	case in.Repo != "":
		repo, _ = s.store.GetRepositoryByBackrest(ctx, in.Repo)
	case job != nil && job.RepositoryID != nil:
		repo, _ = s.store.GetRepository(ctx, *job.RepositoryID)
	}
	res := map[string]any{"ok": true, "event": in.Event}
	status := http.StatusOK
	switch in.Event {
	case "snapshot_start":
		if job == nil {
			res["note"] = "no job for plan " + in.Plan
			break
		}
		run, cancel := s.backrestPrep(ctx, job, repo)
		res["run"] = run.ID
		if cancel {
			status, res["ok"] = http.StatusConflict, false
		}
	case "snapshot_success", "snapshot_warning":
		if job == nil {
			res["note"] = "no job for plan " + in.Plan
			break
		}
		run := s.backrestPost(ctx, job, repo, in)
		res["run"] = run.ID
	case "snapshot_error", "any_error":
		run := s.backrestError(ctx, job, repo, in)
		res["run"] = run.ID
	case "check_start", "prune_start", "forget_start":
		if repo == nil {
			break
		}
		if u := usbOf(repo); u != nil {
			if err := s.eng.MountUSB(ctx, *u, nil); err != nil {
				s.notifyAdmins(ctx, "failure", "USB disk — "+strings.TrimSuffix(in.Event, "_start")+" skipped", repo.Name+": "+err.Error(), "warning")
				status, res["ok"], res["error"] = http.StatusConflict, false, err.Error()
			}
		}
	case "check_success", "check_error", "prune_success", "prune_error", "forget_success", "forget_error":
		run := s.backrestRepoDone(ctx, repo, in)
		if run != nil {
			res["run"] = run.ID
		}
	default:
		res["note"] = "event ignored"
	}
	s.touch()
	writeJSON(w, status, res)
}

// backrestPrep runs before Backrest's snapshot. cancel = the snapshot must
// not run (a share not mounted, the disk missing).
func (s *Server) backrestPrep(ctx context.Context, job *store.Job, repo *store.Repository) (*store.Run, bool) {
	r := &store.Run{JobID: &job.ID, RepositoryID: job.RepositoryID, Kind: "prep", Trigger: "backrest", Title: job.Name + " — before the snapshot"}
	rn, err := s.begin(ctx, r)
	if err != nil {
		return r, false
	}
	if !s.tryJob(job.ID) {
		rn.step("Start", func() (string, string, error) { return "", "", errBusy }) //nolint:errcheck
		return rn.finish("failed", "", nil), false
	}
	defer s.doneJob(job.ID)
	if err := rn.step("Check mounts", func() (string, string, error) {
		if len(job.Config.RequireMounts) == 0 {
			return "nothing to check", "skipped", nil
		}
		return strings.Join(job.Config.RequireMounts, ", "), "ok", s.requireMounts(job.Config.RequireMounts)
	}); err != nil {
		run := rn.finish("failed", "", nil)
		s.notifyAdmins(ctx, "failure", job.Name+" STOPPED", err.Error(), "failure")
		return run, true
	}
	if repo != nil {
		if u := usbOf(repo); u != nil {
			if err := rn.step("Mount the disk", func() (string, string, error) { return u.Mountpoint, "ok", s.eng.MountUSB(rn.ctx, *u, rn.log) }); err != nil {
				run := rn.finish("failed", "", nil)
				msg := err.Error()
				if errors.Is(err, engine.ErrDiskAbsent) {
					msg = "the external disk is not plugged in. Connect it and run the plan again."
				}
				s.notifyAdmins(ctx, "failure", job.Name+" — disk not ready", msg, "failure")
				return run, true
			}
		}
	}
	if job.Config.CoverageCheck {
		rn.soft("Coverage", func() (string, string, error) { return s.coverageStep(rn.ctx) }) //nolint:errcheck
	}
	failed := s.prepare(rn, job)
	summary := ""
	if len(failed) > 0 {
		summary = "stale dumps: " + strings.Join(failed, ", ")
		s.notifyAdmins(ctx, "failure", job.Name+" — prep FAILED", "These dumps failed and Backrest will snapshot last night's copies of them: "+strings.Join(failed, ", ")+".", "failure")
	}
	return rn.finish("", summary, nil), false
}

// backrestPost runs after Backrest's snapshot (success or warning).
func (s *Server) backrestPost(ctx context.Context, job *store.Job, repo *store.Repository, in hookIn) *store.Run {
	r := &store.Run{JobID: &job.ID, RepositoryID: job.RepositoryID, Kind: "post", Trigger: "backrest", Title: job.Name + " — after the snapshot", SnapshotID: in.Snapshot}
	rn, err := s.begin(ctx, r)
	if err != nil {
		return r
	}
	if in.Event == "snapshot_warning" {
		rn.step("Snapshot", func() (string, string, error) { //nolint:errcheck
			return "Backrest made snapshot " + shortID(in.Snapshot) + " but some files could not be read: " + firstNonEmpty(in.Error, "see Backrest"), "warning", nil
		})
	} else {
		rn.step("Snapshot", func() (string, string, error) { return "Backrest made snapshot " + shortID(in.Snapshot), "ok", nil }) //nolint:errcheck
	}
	if repo == nil {
		rn.step("Repository", func() (string, string, error) { return "", "", errors.New("no repository is set for this job") }) //nolint:errcheck
		return rn.finish("", "", nil)
	}
	lock := s.repoLock(repo.ID)
	lock.Lock()
	spec, err := s.repoSpec(ctx, repo)
	if err == nil {
		s.afterSnapshot(rn, job, repo, spec)
	} else {
		rn.step("Repository", func() (string, string, error) { return "", "", err }) //nolint:errcheck
	}
	lock.Unlock()
	if u := usbOf(repo); u != nil {
		s.unmountAfter(rn, repo, u)
	}
	summary := s.runSummary(rn, repo, nil)
	run := rn.finish("", summary, nil)
	s.notifyRun(job, run, summary)
	return run
}

// backrestError tells people a Backrest task failed (and lets a disk go).
func (s *Server) backrestError(ctx context.Context, job *store.Job, repo *store.Repository, in hookIn) *store.Run {
	title := "Backrest task failed"
	if job != nil {
		title = job.Name + " — Backrest task failed"
	}
	r := &store.Run{Kind: "hook", Trigger: "backrest", Title: title}
	if job != nil {
		r.JobID = &job.ID
	}
	if repo != nil {
		r.RepositoryID = &repo.ID
	}
	rn, err := s.begin(ctx, r)
	if err != nil {
		return r
	}
	rn.step(firstNonEmpty(in.Task, "Backrest"), func() (string, string, error) { //nolint:errcheck
		return "", "", errors.New(firstNonEmpty(in.Error, "unknown error"))
	})
	if repo != nil {
		if u := usbOf(repo); u != nil {
			s.unmountAfter(rn, repo, u)
		}
	}
	run := rn.finish("failed", "", nil)
	if job == nil || job.Config.NotifyFailure {
		s.notifyAdmins(ctx, "failure", "Backup FAILED", fmt.Sprintf("Backrest task failed: %s: %s.", firstNonEmpty(in.Task, "unknown task"), firstNonEmpty(in.Error, "unknown error")), "failure")
	}
	return run
}

// backrestRepoDone handles the end of a Backrest check, prune or forget.
func (s *Server) backrestRepoDone(ctx context.Context, repo *store.Repository, in hookIn) *store.Run {
	if repo == nil {
		return nil
	}
	kind, outcome, _ := strings.Cut(in.Event, "_")
	switch {
	case kind == "check" && outcome == "error":
		_ = s.store.SetCopiesPaused(ctx, repo.ID, true, "check failed "+time.Now().Format("2006-01-02")+": "+firstNonEmpty(in.Error, "see Backrest"))
		s.notifyAdmins(ctx, "failure", "Backup repository CORRUPT: "+repo.Name,
			"Backrest's check failed — copies of this repository are paused to protect the last good copies elsewhere. "+firstNonEmpty(in.Error, "")+" After repairing it, resume the copies on the repository in Pocket Backups.", "failure")
	case kind == "check" && outcome == "success":
		if repo.CopiesPaused && strings.HasPrefix(repo.PausedReason, "check failed") {
			_ = s.store.SetCopiesPaused(ctx, repo.ID, false, "")
		}
	case outcome == "error":
		s.notifyAdmins(ctx, "failure", "Backup "+kind+" FAILED: "+repo.Name, firstNonEmpty(in.Error, "see Backrest"), "failure")
	}
	if u := usbOf(repo); u != nil {
		r := &store.Run{RepositoryID: &repo.ID, Kind: "hook", Trigger: "backrest", Title: repo.Name + " — after Backrest's " + kind}
		rn, err := s.begin(ctx, r)
		if err != nil {
			return nil
		}
		s.unmountAfter(rn, repo, u)
		return rn.finish("", "", nil)
	}
	return nil
}

// startBackrestPlan asks Backrest to run a plan now; its hooks do the rest.
func (s *Server) startBackrestPlan(ctx context.Context, job *store.Job, trigger string, userID int64) (*store.Run, error) {
	if !s.br.Configured() {
		return nil, badRequest("this job runs on Backrest, but no Backrest is configured (BACKREST_URL)")
	}
	r := &store.Run{JobID: &job.ID, RepositoryID: job.RepositoryID, Kind: "backup", Trigger: trigger, Title: job.Name + " (Backrest)"}
	if userID > 0 {
		r.UserID = &userID
	}
	rn, err := s.begin(ctx, r)
	if err != nil {
		return nil, err
	}
	go func() {
		rn.step("Backrest runs plan "+job.BackrestPlan, func() (string, string, error) { //nolint:errcheck
			return "the steps before and after show up as their own runs", "ok", s.br.Backup(rn.ctx, job.BackrestPlan)
		})
		rn.finish("", "", nil)
	}()
	return r, nil
}

// ---------- the watcher ----------

// watchBackrest tells people about Backrest operations that ended in a
// warning or an error no hook reported (a snapshot with unreadable files
// fires neither Backrest's success nor its error hooks).
func (s *Server) watchBackrest(ctx context.Context) {
	if !s.br.Configured() {
		return
	}
	first := true
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for {
		s.checkBackrestOps(ctx, first)
		first = false
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) checkBackrestOps(ctx context.Context, baseline bool) {
	ops, err := s.br.Operations(ctx, "", "", 80)
	if err != nil {
		s.brMu.Lock()
		s.brErr = err.Error()
		s.brMu.Unlock()
		return
	}
	s.brMu.Lock()
	s.brErr = ""
	s.brMu.Unlock()
	cfg, _ := s.br.GetConfig(ctx)
	for _, op := range ops {
		if op.EndTime().IsZero() || (op.Status != "STATUS_WARNING" && op.Status != "STATUS_ERROR") {
			continue
		}
		if s.store.BackrestSeen(ctx, op.ID) || baseline || time.Since(op.EndTime()) > 24*time.Hour {
			continue
		}
		if op.Status == "STATUS_WARNING" && op.Kind() == "backup" && planHasHook(cfg, op.PlanID, "CONDITION_SNAPSHOT_WARNING") {
			continue // the warning hook told people already
		}
		if op.Status == "STATUS_ERROR" && planHasHook(cfg, op.PlanID, "CONDITION_ANY_ERROR") && op.Kind() != "hook" {
			continue
		}
		what := op.Kind()
		msg := firstNonEmpty(op.Message, "see Backrest")
		if op.Backup != nil && len(op.Backup.Errors) > 0 {
			var items []string
			for _, e := range op.Backup.Errors {
				items = append(items, e.Item)
			}
			msg = fmt.Sprintf("%d file(s) could not be read: %s", len(items), strings.Join(firstN(items, 3), ", "))
		}
		level, title := "warning", fmt.Sprintf("Backrest %s %s finished with warnings", op.PlanID, what)
		if op.Status == "STATUS_ERROR" {
			level, title = "failure", fmt.Sprintf("Backrest %s %s FAILED", firstNonEmpty(op.PlanID, op.RepoID), what)
		}
		s.notifyAdmins(ctx, "failure", title, msg+" — the steps after the snapshot (copies, heartbeat) did not run for it.", level)
	}
}

func planHasHook(cfg *engine.BRConfig, plan, cond string) bool {
	if cfg == nil {
		return false
	}
	for _, p := range cfg.Plans {
		if p.ID != plan {
			continue
		}
		for _, h := range p.Hooks {
			for _, c := range h.Conditions {
				if c == cond && h.ActionCommand != nil && strings.Contains(h.ActionCommand.Command, "/api/v1/hooks/backrest") {
					return true
				}
			}
		}
	}
	return false
}

// ---------- overview ----------

type brPlanView struct {
	ID         string         `json:"id"`
	Repo       string         `json:"repo"`
	Cron       string         `json:"cron"`
	NextRun    string         `json:"next_run"`
	Paths      []string       `json:"paths"`
	Excludes   int            `json:"excludes"`
	Job        *int64         `json:"job_id"`
	Hooked     bool           `json:"hooked"`
	OldScripts []string       `json:"old_scripts"`
	Last       *brOpView      `json:"last_backup"`
	Recent     []brOpView     `json:"recent"`
	Retention  map[string]any `json:"retention"`
}

type brOpView struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Snapshot string `json:"snapshot"`
	Message  string `json:"message"`
	Added    string `json:"data_added"`
	Errors   int    `json:"errors"`
}

func opView(o engine.BROperation) brOpView {
	v := brOpView{ID: o.ID, Kind: o.Kind(), Status: strings.ToLower(strings.TrimPrefix(o.Status, "STATUS_")), Snapshot: o.SnapshotID, Message: o.Message}
	if t := o.StartTime(); !t.IsZero() {
		v.Start = t.UTC().Format(time.RFC3339)
	}
	if t := o.EndTime(); !t.IsZero() {
		v.End = t.UTC().Format(time.RFC3339)
	}
	if o.Backup != nil {
		v.Errors = len(o.Backup.Errors)
		if o.Backup.LastStatus != nil && o.Backup.LastStatus.Summary != nil {
			v.Added = o.Backup.LastStatus.Summary.DataAdded
			if v.Snapshot == "" {
				v.Snapshot = o.Backup.LastStatus.Summary.SnapshotID
			}
		}
	}
	return v
}

func (s *Server) handleBackrest(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	out := map[string]any{"configured": s.br.Configured(), "url": s.cfg.BackrestURL, "plans": []brPlanView{}, "repos": []map[string]any{}}
	if !s.br.Configured() {
		writeJSON(w, http.StatusOK, out)
		return
	}
	cfg, err := s.br.GetConfig(ctx)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["instance"] = cfg.Instance
	jobs, _ := s.store.ListJobs(ctx)
	repos, _ := s.store.ListRepositories(ctx)
	ops, _ := s.br.Operations(ctx, "", "", 300)
	plans := []brPlanView{}
	for _, p := range cfg.Plans {
		v := brPlanView{ID: p.ID, Repo: p.Repo, Cron: p.Cron(), Paths: p.Paths, Excludes: len(p.Excludes), Recent: []brOpView{}, OldScripts: []string{}}
		if v.Paths == nil {
			v.Paths = []string{}
		}
		_ = json.Unmarshal(p.Retention, &v.Retention)
		if n := engine.NextRun(v.Cron, time.Now()); !n.IsZero() {
			v.NextRun = n.UTC().Format(time.RFC3339)
		}
		for _, j := range jobs {
			if j.Engine == "backrest" && j.BackrestPlan == p.ID {
				id := j.ID
				v.Job = &id
			}
		}
		for _, h := range p.Hooks {
			if h.ActionCommand == nil {
				continue
			}
			if strings.Contains(h.ActionCommand.Command, "/api/v1/hooks/backrest") {
				v.Hooked = true
			} else {
				v.OldScripts = append(v.OldScripts, h.ActionCommand.Command)
			}
		}
		for _, o := range ops {
			if o.PlanID != p.ID {
				continue
			}
			ov := opView(o)
			if len(v.Recent) < 12 {
				v.Recent = append(v.Recent, ov)
			}
			if o.Kind() == "backup" && ov.Status != "pending" && ov.Status != "inprogress" && (v.Last == nil || ov.Start > v.Last.Start) {
				c := ov
				v.Last = &c
			}
		}
		sort.Slice(v.Recent, func(i, j int) bool { return v.Recent[i].Start > v.Recent[j].Start })
		plans = append(plans, v)
	}
	out["plans"] = plans
	rv := []map[string]any{}
	for _, br := range cfg.Repos {
		m := map[string]any{"id": br.ID, "uri": br.URI}
		for _, x := range repos {
			if x.BackrestRepo == br.ID {
				m["repository_id"] = x.ID
			}
		}
		rv = append(rv, m)
	}
	out["repos"] = rv
	s.brMu.Lock()
	out["watch_error"] = s.brErr
	s.brMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// ---------- import + hooks ----------

// handleBackrestImport brings Backrest's repositories and plans in: a
// repository each (same restic repository, password from Backrest) and a
// job per plan that runs on Backrest.
func (s *Server) handleBackrestImport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	cfg, err := s.br.GetConfig(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	repos, _ := s.store.ListRepositories(ctx)
	have := map[string]int64{}
	for _, x := range repos {
		if x.BackrestRepo != "" {
			have[x.BackrestRepo] = x.ID
		}
	}
	added := map[string]int{"repositories": 0, "jobs": 0}
	for _, br := range cfg.Repos {
		if _, ok := have[br.ID]; ok {
			continue
		}
		x := &store.Repository{Name: br.ID, Location: "uri", Path: br.URI, ManagedBy: "backrest", BackrestRepo: br.ID}
		if strings.HasPrefix(br.URI, "/") {
			x.Location = "local"
		}
		for _, e := range br.Env {
			if v, ok := strings.CutPrefix(e, "RESTIC_COMPRESSION="); ok {
				x.Options.Compression = v
			}
		}
		if br.Password != "" {
			if x.Password, err = s.seal(br.Password); err != nil {
				s.fail(w, err, "seal")
				return
			}
		}
		if err := s.store.SaveRepository(ctx, x); err != nil {
			s.fail(w, err, "save repository")
			return
		}
		have[br.ID] = x.ID
		added["repositories"]++
	}
	jobs, _ := s.store.ListJobs(ctx)
	planned := map[string]bool{}
	for _, j := range jobs {
		if j.Engine == "backrest" {
			planned[j.BackrestPlan] = true
		}
	}
	for _, p := range cfg.Plans {
		if planned[p.ID] {
			continue
		}
		rid := have[p.Repo]
		j := &store.Job{Name: titleCase(p.ID), Kind: "backup", Engine: "backrest", BackrestPlan: p.ID, Schedule: p.Cron(), Enabled: true,
			Config: store.JobConfig{NotifyFailure: true, NotifySuccess: true}}
		if rid > 0 {
			j.RepositoryID = &rid
		}
		if err := s.store.SaveJob(ctx, j); err != nil {
			s.fail(w, err, "save job")
			return
		}
		added["jobs"]++
	}
	s.touch()
	s.coverageChanged()
	writeJSON(w, http.StatusOK, added)
}

func titleCase(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "-", " "), "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// hookCommand is the command a Backrest hook runs to call this app.
func (s *Server) hookCommand(token, event string, plan, repo string) string {
	base := firstNonEmpty(s.cfg.HookBaseURL, s.cfg.BaseURL)
	var b strings.Builder
	fmt.Fprintf(&b, "curl -fsS -m 7200 -X POST -H 'Authorization: Bearer %s' --data-urlencode event=%s", token, event)
	if plan != "" {
		fmt.Fprintf(&b, " --data-urlencode plan=%s", shellArg(plan))
	}
	if repo != "" {
		fmt.Fprintf(&b, " --data-urlencode repo=%s", shellArg(repo))
	}
	switch event {
	case "snapshot_success", "snapshot_warning":
		b.WriteString(" --data-urlencode snapshot={{ .SnapshotId }}")
	}
	if strings.HasSuffix(event, "error") || event == "snapshot_warning" {
		b.WriteString(" --data-urlencode task={{ .ShellEscape .Task }} --data-urlencode error={{ .ShellEscape .Error }}")
	}
	fmt.Fprintf(&b, " %s/api/v1/hooks/backrest", strings.TrimRight(base, "/"))
	return b.String()
}

func shellArg(s string) string {
	for _, r := range s {
		if !(r == '-' || r == '_' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

func hook(cmd, onError string, conds ...string) map[string]any {
	h := map[string]any{"conditions": conds, "actionCommand": map[string]any{"command": cmd}}
	if onError != "" {
		h["onError"] = onError
	}
	return h
}

// handleBackrestHooks shows (GET) or installs (POST) the hooks that make
// Backrest call this app instead of scripts, for every plan a job runs on
// and the repositories behind them.
func (s *Server) handleBackrestHooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	u := userFrom(ctx)
	cfg, err := s.br.GetConfig(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	install := r.Method == http.MethodPost
	token := "<an API token>"
	if install {
		plain, _, err := s.store.CreateAPIToken(ctx, u.ID, "Backrest hooks "+time.Now().Format("2006-01-02"))
		if err != nil {
			s.fail(w, err, "token")
			return
		}
		token = plain
	}
	jobs, _ := s.store.ListJobs(ctx)
	byPlan := map[string]*store.Job{}
	for i := range jobs {
		if jobs[i].Engine == "backrest" && jobs[i].BackrestPlan != "" {
			byPlan[jobs[i].BackrestPlan] = &jobs[i]
		}
	}
	repos, _ := s.store.ListRepositories(ctx)
	usbRepo := map[string]bool{}
	known := map[string]bool{}
	for _, x := range repos {
		if x.BackrestRepo != "" {
			known[x.BackrestRepo] = true
			usbRepo[x.BackrestRepo] = x.Location == "usb"
		}
	}
	type change struct {
		Kind   string   `json:"kind"` // plan | repo
		ID     string   `json:"id"`
		Before []string `json:"before"`
		After  []string `json:"after"`
	}
	changes := []change{}
	raw := cfg.Raw
	rawPlans, _ := raw["plans"].([]any)
	usedRepos := map[string]bool{}
	for _, rp := range rawPlans {
		p, ok := rp.(map[string]any)
		if !ok {
			continue
		}
		id, _ := p["id"].(string)
		if byPlan[id] == nil {
			continue
		}
		repoID, _ := p["repo"].(string)
		usedRepos[repoID] = true
		startErr := ""
		if usbRepo[repoID] || len(byPlan[id].Config.RequireMounts) > 0 {
			startErr = "ON_ERROR_CANCEL"
		}
		hooks := []any{
			hook(s.hookCommand(token, "snapshot_start", id, ""), startErr, "CONDITION_SNAPSHOT_START"),
			hook(s.hookCommand(token, "snapshot_success", id, ""), "", "CONDITION_SNAPSHOT_SUCCESS"),
			hook(s.hookCommand(token, "snapshot_warning", id, ""), "", "CONDITION_SNAPSHOT_WARNING"),
			hook(s.hookCommand(token, "any_error", id, ""), "", "CONDITION_ANY_ERROR"),
		}
		changes = append(changes, change{"plan", id, hookCommands(p["hooks"]), hookCommands(hooks)})
		if install {
			p["hooks"] = hooks
		}
	}
	rawRepos, _ := raw["repos"].([]any)
	for _, rr := range rawRepos {
		x, ok := rr.(map[string]any)
		if !ok {
			continue
		}
		id, _ := x["id"].(string)
		if !usedRepos[id] || !known[id] {
			continue
		}
		hooks := []any{
			hook(s.hookCommand(token, "check_error", "", id), "", "CONDITION_CHECK_ERROR"),
			hook(s.hookCommand(token, "check_success", "", id), "", "CONDITION_CHECK_SUCCESS"),
			hook(s.hookCommand(token, "prune_error", "", id), "", "CONDITION_PRUNE_ERROR"),
		}
		if usbRepo[id] {
			hooks = append(hooks,
				hook(s.hookCommand(token, "check_start", "", id), "ON_ERROR_CANCEL", "CONDITION_PRUNE_START", "CONDITION_CHECK_START"),
				hook(s.hookCommand(token, "prune_success", "", id), "", "CONDITION_PRUNE_SUCCESS"))
		}
		changes = append(changes, change{"repo", id, hookCommands(x["hooks"]), hookCommands(hooks)})
		if install {
			x["hooks"] = hooks
		}
	}
	if install {
		if err := s.br.SetConfig(ctx, raw); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.log.Info("backrest hooks installed", "plans", len(byPlan), "by", u.Email)
	}
	// never echo the real token back
	for i := range changes {
		for j := range changes[i].After {
			changes[i].After[j] = strings.ReplaceAll(changes[i].After[j], token, "pbk_…")
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"installed": install, "changes": changes})
}

func hookCommands(v any) []string {
	out := []string{}
	list, _ := v.([]any)
	for _, h := range list {
		m, _ := h.(map[string]any)
		cmd := ""
		switch ac := m["actionCommand"].(type) {
		case map[string]any:
			cmd, _ = ac["command"].(string)
		}
		var cs []string
		switch conds := m["conditions"].(type) {
		case []any:
			for _, c := range conds {
				if s, ok := c.(string); ok {
					cs = append(cs, strings.TrimPrefix(s, "CONDITION_"))
				}
			}
		case []string:
			for _, c := range conds {
				cs = append(cs, strings.TrimPrefix(c, "CONDITION_"))
			}
		}
		if i := strings.Index(cmd, "Bearer "); i >= 0 {
			if j := strings.Index(cmd[i:], "'"); j > 0 {
				cmd = cmd[:i] + "Bearer pbk_…" + cmd[i+j:]
			}
		}
		out = append(out, strings.Join(cs, ",")+": "+cmd)
	}
	return out
}

type brOpsCache struct {
	at  time.Time
	ops []engine.BROperation
}

// backrestLastBackup is a plan's newest finished backup in Backrest's log,
// as a run (cached for a minute).
func (s *Server) backrestLastBackup(ctx context.Context, plan string) *store.Run {
	if !s.br.Configured() || plan == "" {
		return nil
	}
	s.brMu.Lock()
	c := s.brOps
	s.brMu.Unlock()
	if c == nil || time.Since(c.at) > time.Minute {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ops, err := s.br.Operations(cctx, "", "", 300)
		cancel()
		if err != nil {
			return nil
		}
		c = &brOpsCache{at: time.Now(), ops: ops}
		s.brMu.Lock()
		s.brOps = c
		s.brMu.Unlock()
	}
	var best *engine.BROperation
	for i := range c.ops {
		o := &c.ops[i]
		if o.PlanID != plan || o.Kind() != "backup" || o.EndTime().IsZero() {
			continue
		}
		if best == nil || o.EndTime().After(best.EndTime()) {
			best = o
		}
	}
	if best == nil {
		return nil
	}
	status := map[string]string{"STATUS_SUCCESS": "success", "STATUS_WARNING": "warning", "STATUS_ERROR": "failed"}[best.Status]
	if status == "" {
		return nil
	}
	r := &store.Run{Kind: "backup", Trigger: "backrest", Status: status, Title: "Backrest " + plan, SnapshotID: best.SnapshotID,
		StartedAt: best.StartTime().UTC().Format(time.RFC3339), FinishedAt: best.EndTime().UTC().Format(time.RFC3339),
		DurationMS: best.EndTime().Sub(best.StartTime()).Milliseconds(), Summary: "snapshot by Backrest", Steps: []store.Step{}}
	if best.Backup != nil && best.Backup.LastStatus != nil && best.Backup.LastStatus.Summary != nil {
		r.SnapshotID = firstNonEmpty(r.SnapshotID, best.Backup.LastStatus.Summary.SnapshotID)
	}
	if status == "failed" {
		r.Error = firstNonEmpty(best.Message, "Backrest reported an error")
	}
	return r
}
