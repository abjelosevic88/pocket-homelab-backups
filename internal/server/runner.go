package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// activeRun is a run in progress: its live log and a way to stop it.
type activeRun struct {
	run    *store.Run
	log    *engine.Log
	cancel context.CancelFunc
	mu     sync.Mutex
	step   string
	pct    float64
}

// runner records one run: its steps, its log and how it ended.
type runner struct {
	s       *Server
	ctx     context.Context
	run     *store.Run
	act     *activeRun
	log     *engine.Log
	started time.Time
	// hardFailed is set by a failed step() (soft() steps only warn the run)
	hardFailed bool
}

// begin creates the run and registers it as active.
func (s *Server) begin(parent context.Context, r *store.Run) (*runner, error) {
	r.Steps = []store.Step{}
	if err := s.store.CreateRun(parent, r); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	log := engine.NewLog(512 << 10)
	act := &activeRun{run: r, log: log, cancel: cancel}
	s.activeMu.Lock()
	s.active[r.ID] = act
	s.activeMu.Unlock()
	log.Printf("%s — %s", time.Now().Format("2006-01-02 15:04:05"), firstNonEmpty(r.Title, r.Kind))
	s.touch()
	return &runner{s: s, ctx: ctx, run: r, act: act, log: log, started: time.Now()}, nil
}

// step runs one part of the work and records it. fn returns a short detail,
// a status (ok | warning | skipped) and an error (= failed).
func (rn *runner) step(name string, fn func() (string, string, error)) error {
	t := time.Now()
	rn.act.mu.Lock()
	rn.act.step = name
	rn.act.mu.Unlock()
	rn.run.Steps = append(rn.run.Steps, store.Step{Name: name, Status: "running"})
	idx := len(rn.run.Steps) - 1
	rn.log.Printf("▸ %s", name)
	rn.save()
	detail, status, err := fn()
	st := &rn.run.Steps[idx]
	st.MS = time.Since(t).Milliseconds()
	st.Detail = detail
	switch {
	case err != nil:
		st.Status = "failed"
		if st.Detail == "" {
			st.Detail = err.Error()
		} else {
			st.Detail += " — " + err.Error()
		}
		rn.log.Printf("  ✗ %s", err)
		rn.hardFailed = true
	case status == "warning":
		st.Status = "warning"
		rn.log.Printf("  ! %s", detail)
	case status == "skipped":
		st.Status = "skipped"
		rn.log.Printf("  – %s", detail)
	default:
		st.Status = "ok"
		if detail != "" {
			rn.log.Printf("  ✓ %s", detail)
		}
	}
	rn.save()
	return err
}

// soft runs a step whose failure only warns the run as a whole (a copy, a
// report, one dump): the step shows as failed, the run as a warning.
func (rn *runner) soft(name string, fn func() (string, string, error)) error {
	was := rn.hardFailed
	err := rn.step(name, fn)
	rn.hardFailed = was
	return err
}

func (rn *runner) save() {
	rn.run.Log = rn.log.String()
	rn.run.DurationMS = time.Since(rn.started).Milliseconds()
	_ = rn.s.store.UpdateRun(context.WithoutCancel(rn.ctx), rn.run)
	rn.s.touch()
}

// status works out how the run went from its steps.
func (rn *runner) status() string {
	warned := false
	for _, st := range rn.run.Steps {
		if st.Status == "failed" || st.Status == "warning" {
			warned = true
		}
	}
	switch {
	case rn.ctx.Err() != nil:
		return "cancelled"
	case rn.hardFailed:
		return "failed"
	case warned:
		return "warning"
	}
	return "success"
}

// finish ends the run. status "" = worked out from the steps.
func (rn *runner) finish(status, summary string, err error) *store.Run {
	if status == "" {
		status = rn.status()
	}
	if err != nil && rn.run.Error == "" {
		rn.run.Error = err.Error()
	}
	if rn.run.Error == "" && status == "failed" {
		for _, st := range rn.run.Steps {
			if st.Status == "failed" {
				rn.run.Error = st.Name + ": " + st.Detail
				break
			}
		}
	}
	rn.run.Status = status
	rn.run.Summary = summary
	rn.run.FinishedAt = store.Now()
	rn.log.Printf("%s — %s (%s)", time.Now().Format("2006-01-02 15:04:05"), status, time.Since(rn.started).Round(time.Second))
	rn.save()
	rn.s.activeMu.Lock()
	delete(rn.s.active, rn.run.ID)
	rn.s.activeMu.Unlock()
	rn.act.cancel()
	rn.s.saveRefreshedTokens(context.Background())
	rn.s.publishRun(rn.run)
	return rn.run
}

// ---------- jobs ----------

var errBusy = errors.New("this job is already running")

func (s *Server) tryJob(id int64) bool {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	if s.jobBusy[id] {
		return false
	}
	s.jobBusy[id] = true
	return true
}

func (s *Server) doneJob(id int64) {
	s.locksMu.Lock()
	delete(s.jobBusy, id)
	s.locksMu.Unlock()
}

// StartJob runs a job in the background and returns its run at once.
func (s *Server) StartJob(ctx context.Context, job *store.Job, trigger string, userID int64) (*store.Run, error) {
	if job.Engine == "backrest" && job.Kind == "backup" {
		return s.startBackrestPlan(ctx, job, trigger, userID)
	}
	if !s.tryJob(job.ID) {
		return nil, errBusy
	}
	kind := "backup"
	if job.Kind == "pull" {
		kind = "pull"
	}
	r := &store.Run{JobID: &job.ID, RepositoryID: job.RepositoryID, Kind: kind, Trigger: trigger, Title: job.Name}
	if userID > 0 {
		r.UserID = &userID
	}
	rn, err := s.begin(ctx, r)
	if err != nil {
		s.doneJob(job.ID)
		return nil, err
	}
	go func() {
		defer s.doneJob(job.ID)
		if job.Kind == "pull" {
			s.runPull(rn, job)
		} else {
			s.runBackup(rn, job)
		}
	}()
	return r, nil
}

// requireMounts refuses to back up a share that is not mounted: an empty
// folder would back up as "everything was deleted".
func (s *Server) requireMounts(paths []string) error {
	var missing []string
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" && !engine.Mounted(p) {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("not mounted: %s (refusing to back up what may be an empty folder)", strings.Join(missing, ", "))
	}
	return nil
}

func (s *Server) stagingDir(job *store.Job) string {
	if job.Config.StagingDir != "" {
		return job.Config.StagingDir
	}
	return filepath.Join(s.cfg.StagingDir, fmt.Sprintf("job-%d", job.ID))
}

// prepare captures a job's sources into its staging folder. It never fails
// the run: a source that fails is a warning (last night's file stays).
func (s *Server) prepare(rn *runner, job *store.Job) (failed []string) {
	sources := s.jobSources(rn.ctx, job)
	staging := s.stagingDir(job)
	for i := range sources {
		src := &sources[i]
		if !src.Enabled || src.Kind == "paths" {
			continue
		}
		var pw string
		if src.Secret != "" {
			pw, _ = s.open(src.Secret)
		}
		t := time.Now()
		var res *engine.Capture
		err := rn.soft(src.Name, func() (string, string, error) {
			c, cancel := context.WithTimeout(rn.ctx, 2*time.Hour)
			defer cancel()
			var err error
			res, err = s.eng.CaptureSource(c, src, pw, staging, rn.log)
			if res == nil {
				return "", "", err
			}
			if res.Skipped {
				return res.Detail, "skipped", nil
			}
			return res.Detail, "ok", err
		})
		if err != nil {
			failed = append(failed, src.Name)
			s.store.SetSourceResult(rn.ctx, src.ID, false, err.Error(), 0, time.Since(t))
		} else if res != nil && !res.Skipped {
			s.store.SetSourceResult(rn.ctx, src.ID, true, "", res.Size, time.Since(t))
		}
	}
	return failed
}

func (s *Server) jobSources(ctx context.Context, job *store.Job) []store.Source {
	all, _ := s.store.ListSources(ctx)
	in := map[int64]bool{}
	for _, id := range job.SourceIDs {
		in[id] = true
	}
	out := []store.Source{}
	for _, x := range all {
		if in[x.ID] {
			out = append(out, x)
		}
	}
	return out
}

// backupPaths is what a pocket-engine job snapshots.
func (s *Server) backupPaths(ctx context.Context, job *store.Job) (paths, excludes []string) {
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, p := range job.Config.Paths {
		add(p)
	}
	excludes = append(excludes, job.Config.Excludes...)
	staging := false
	for _, src := range s.jobSources(ctx, job) {
		if !src.Enabled {
			continue
		}
		if src.Kind == "paths" {
			for _, p := range src.Config.Paths {
				add(p)
			}
			excludes = append(excludes, src.Config.Excludes...)
		} else {
			staging = true
		}
	}
	if staging {
		// unless the job already names what it wants from the staging folder
		// (a Backrest plan taken over backs up only its own dump)
		dir := filepath.Clean(s.stagingDir(job))
		named := false
		for _, p := range paths {
			if underAny(p, []string{dir}) {
				named = true
			}
		}
		if !named {
			add(dir)
		}
	}
	sort.Strings(paths)
	return paths, excludes
}

func (s *Server) runBackup(rn *runner, job *store.Job) {
	ctx := rn.ctx
	var repo *store.Repository
	if job.RepositoryID != nil {
		repo, _ = s.store.GetRepository(ctx, *job.RepositoryID)
	}
	if repo == nil {
		rn.finish("failed", "", errors.New("this job has no repository"))
		s.notifyRun(job, rn.run, "")
		return
	}
	if err := rn.step("Check mounts", func() (string, string, error) {
		if len(job.Config.RequireMounts) == 0 {
			return "nothing to check", "skipped", nil
		}
		return strings.Join(job.Config.RequireMounts, ", "), "ok", s.requireMounts(job.Config.RequireMounts)
	}); err != nil {
		rn.finish("failed", "", nil)
		s.notifyRun(job, rn.run, "")
		return
	}
	lock := s.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()
	usb := usbOf(repo)
	if usb != nil {
		if err := rn.step("Mount the disk", func() (string, string, error) {
			err := s.eng.MountUSB(ctx, *usb, rn.log)
			return usb.Mountpoint, "ok", err
		}); err != nil {
			rn.finish("failed", "", nil)
			s.notifyRun(job, rn.run, "")
			return
		}
		defer s.unmountAfter(rn, repo, usb)
	}
	if job.Config.CoverageCheck {
		rn.soft("Coverage", func() (string, string, error) { return s.coverageStep(ctx) })
	}
	failedSources := s.prepare(rn, job)
	spec, err := s.repoSpec(ctx, repo)
	if err != nil {
		rn.step("Snapshot", func() (string, string, error) { return "", "", err }) //nolint:errcheck
		rn.finish("failed", "", nil)
		s.notifyRun(job, rn.run, "")
		return
	}
	paths, excludes := s.backupPaths(ctx, job)
	var res *engine.BackupResult
	snapErr := rn.step("Snapshot", func() (string, string, error) {
		if len(paths) == 0 {
			return "", "", errors.New("nothing to back up: add folders or sources to this job")
		}
		if err := s.eng.IsRepo(ctx, spec); err != nil {
			return "", "", fmt.Errorf("the repository does not open: %w", err)
		}
		tags := append([]string{"pocket-backups", fmt.Sprintf("job:%d", job.ID)}, job.Config.Tags...)
		var err error
		res, err = s.eng.Backup(ctx, spec, engine.BackupOptions{Paths: paths, Excludes: excludes, Tags: tags, Host: s.eng.ResticHost,
			ExcludeCaches: job.Config.ExcludeCaches, OnProgress: func(p engine.Progress) {
				rn.act.mu.Lock()
				rn.act.pct = p.Percent
				rn.act.mu.Unlock()
			}}, rn.log)
		if err != nil {
			return "", "", err
		}
		rn.run.SnapshotID = res.SnapshotID
		rn.run.BytesAdded, rn.run.BytesTotal, rn.run.FilesNew, rn.run.FilesChanged = res.DataPacked, res.TotalBytes, res.FilesNew, res.FilesChanged
		d := fmt.Sprintf("%s · %s added · %s in all", shortID(res.SnapshotID), engine.HumanBytes(res.DataPacked), engine.HumanBytes(res.TotalBytes))
		if len(res.Warnings) > 0 {
			return fmt.Sprintf("%s · %d file(s) could not be read: %s", d, len(res.Warnings), strings.Join(firstN(res.Warnings, 3), "; ")), "warning", nil
		}
		return d, "ok", nil
	})
	if snapErr == nil {
		s.afterSnapshot(rn, job, repo, spec)
	}
	summary := s.runSummary(rn, repo, failedSources)
	rn.finish("", summary, nil)
	s.notifyRun(job, rn.run, summary)
}

// afterSnapshot does everything after a good snapshot: retention, a read
// back, the copies, a check, stats, the report and the heartbeat. Used by
// pocket-engine jobs and for Backrest's snapshot-success hook.
func (s *Server) afterSnapshot(rn *runner, job *store.Job, repo *store.Repository, spec engine.Repo) {
	ctx := rn.ctx
	ret := job.Config.Retention
	if !ret.Empty() {
		rn.soft("Retention", func() (string, string, error) {
			err := s.eng.Forget(ctx, spec, engine.ForgetPolicy{KeepLast: ret.KeepLast, KeepHourly: ret.KeepHourly, KeepDaily: ret.KeepDaily,
				KeepWeekly: ret.KeepWeekly, KeepMonthly: ret.KeepMonthly, KeepYearly: ret.KeepYearly, GroupByNone: ret.GroupByNone, Prune: ret.Prune,
				Tags: []string{retentionTag(job)}}, rn.log)
			return retentionText(ret), "ok", err
		})
	}
	if job.Config.VerifyAfter {
		rn.soft("Read back", func() (string, string, error) {
			snaps, err := s.eng.Snapshots(ctx, spec)
			if err != nil {
				return "the repository could not be read back", "", err
			}
			if len(snaps) == 0 {
				return "", "", errors.New("the repository holds no snapshots")
			}
			return fmt.Sprintf("%d snapshot(s), newest %s", len(snaps), snaps[len(snaps)-1].Time.Local().Format("2006-01-02 15:04")), "ok", nil
		})
	}
	s.runCopies(rn, job, repo, spec)
	if job.Config.CheckReadPct > 0 {
		rn.soft("Check", func() (string, string, error) {
			err := s.eng.Check(ctx, spec, job.Config.CheckReadPct, rn.log)
			if err != nil {
				_ = s.store.SetCopiesPaused(ctx, repo.ID, true, "the check after "+job.Name+" failed: "+err.Error())
			}
			return fmt.Sprintf("read %d%% of the data", job.Config.CheckReadPct), "ok", err
		})
	}
	rn.soft("Repository size", func() (string, string, error) {
		st, err := s.refreshRepoStats(ctx, repo)
		if err != nil {
			return "", "", err
		}
		repo.Stats = *st
		d := fmt.Sprintf("%s in %d snapshot(s)", engine.HumanBytes(st.Size), st.Snapshots)
		if repo.Options.USB != nil && repo.Options.USB.WarnPct > 0 && st.DiskUsed >= repo.Options.USB.WarnPct {
			return fmt.Sprintf("%s · the disk is %d%% full (%s free)", d, st.DiskUsed, engine.HumanBytes(st.DiskFree)), "warning", nil
		}
		return d, "ok", nil
	})
	if job.Config.Report {
		rn.soft("Homepage report", func() (string, string, error) { return s.writeReport(ctx, repo, spec) })
	}
	if job.Config.HeartbeatURL != "" {
		rn.soft("Heartbeat", func() (string, string, error) {
			return "pinged", "ok", s.heartbeat(ctx, job.Config.HeartbeatURL, rn.run.SnapshotID)
		})
	}
}

// runCopies copies the repository to each of the job's places, unless a
// failed check paused them.
func (s *Server) runCopies(rn *runner, job *store.Job, repo *store.Repository, spec engine.Repo) {
	ctx := rn.ctx
	copies := []store.Copy{}
	for _, c := range job.Copies {
		if c.Enabled {
			copies = append(copies, c)
		}
	}
	if len(copies) == 0 {
		return
	}
	if fresh, err := s.store.GetRepository(ctx, repo.ID); err == nil && fresh.CopiesPaused {
		rn.step("Copies", func() (string, string, error) { //nolint:errcheck
			return "paused: " + firstNonEmpty(fresh.PausedReason, "a check failed") + " — resume them on the repository once it is repaired", "warning", nil
		})
		return
	}
	for _, c := range copies {
		c := c
		t := time.Now()
		err := rn.soft("Copy to "+c.Name, func() (string, string, error) { return s.doCopy(ctx, &c, repo, spec, rn.log) })
		s.store.SetCopyResult(ctx, c.ID, err == nil, errText(err), time.Since(t))
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// doCopy runs one copy.
func (s *Server) doCopy(ctx context.Context, c *store.Copy, repo *store.Repository, spec engine.Repo, log *engine.Log) (string, string, error) {
	switch c.Mode {
	case "restic":
		if c.TargetRepositoryID == nil {
			return "", "", errors.New("no target repository")
		}
		target, err := s.store.GetRepository(ctx, *c.TargetRepositoryID)
		if err != nil {
			return "", "", err
		}
		tspec, err := s.repoSpec(ctx, target)
		if err != nil {
			return "", "", err
		}
		if u := usbOf(target); u != nil {
			if err := s.eng.MountUSB(ctx, *u, log); err != nil {
				return "", "", err
			}
			defer s.eng.UnmountUSB(context.WithoutCancel(ctx), *u, log) //nolint:errcheck
		}
		return "new snapshots copied into " + target.Name, "ok", s.eng.CopySnapshots(ctx, spec, tspec, log)
	default: // mirror
		if repo.Location != "local" && repo.Location != "usb" {
			return "", "", errors.New("only a repository on this box can be mirrored; use a restic copy for this one")
		}
		dst := c.Path
		if c.ConnectionID != nil {
			conn, err := s.store.GetConnection(ctx, *c.ConnectionID)
			if err != nil {
				return "", "", errors.New("its connection is gone")
			}
			dst = remotePath(conn, c.Path)
		} else if !filepath.IsAbs(dst) {
			return "", "", errors.New("a folder on this box must be an absolute path")
		}
		o := engine.SyncOptions{Transfers: c.Options.Transfers, HardDelete: c.Options.HardDelete, DeleteBefore: c.Options.DeleteBefore,
			UseTrash: c.Options.UseTrash, BwLimit: c.Options.BwLimit}
		if o.Transfers == 0 {
			o.Transfers = 4
		}
		err := s.eng.Sync(ctx, strings.TrimRight(repo.Path, "/"), dst, o, log)
		return dst, "ok", err
	}
}

func (s *Server) unmountAfter(rn *runner, repo *store.Repository, usb *engine.USB) {
	rn.soft("Unmount the disk", func() (string, string, error) { //nolint:errcheck
		safe, err := s.eng.UnmountUSB(context.WithoutCancel(rn.ctx), *usb, rn.log)
		if safe {
			s.notifyAdmins(context.Background(), "success", "USB backup — safe to unplug", repo.Name+": the disk is unmounted"+map[bool]string{true: " and powered down", false: ""}[usb.PowerOff]+".", "info")
			return "safe to unplug", "ok", nil
		}
		s.notifyAdmins(context.Background(), "failure", "USB backup — still mounted", repo.Name+": unmounting failed. Do NOT unplug the disk. "+errText(err), "failure")
		return "do NOT unplug", "", err
	})
}

func (s *Server) heartbeat(ctx context.Context, url, snap string) error {
	if strings.Contains(url, "?") {
		url += "&msg=snapshot+" + shortID(snap)
	}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := s.http.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}

func (s *Server) runSummary(rn *runner, repo *store.Repository, failedSources []string) string {
	var parts []string
	if rn.run.SnapshotID != "" {
		parts = append(parts, "snapshot "+shortID(rn.run.SnapshotID))
	}
	if repo != nil && repo.Stats.Size > 0 {
		parts = append(parts, "repository "+engine.HumanBytes(repo.Stats.Size))
	}
	var copied, failedCopies []string
	for _, st := range rn.run.Steps {
		if name, ok := strings.CutPrefix(st.Name, "Copy to "); ok {
			if st.Status == "ok" {
				copied = append(copied, name)
			} else {
				failedCopies = append(failedCopies, name)
			}
		}
	}
	if len(copied) > 0 {
		parts = append(parts, "copied to "+strings.Join(copied, " + "))
	}
	if len(failedCopies) > 0 {
		parts = append(parts, "copy FAILED: "+strings.Join(failedCopies, ", "))
	}
	if len(failedSources) > 0 {
		parts = append(parts, "stale dumps: "+strings.Join(failedSources, ", "))
	}
	return strings.Join(parts, " · ")
}

// retentionTag is the tag retention applies to: only this job's snapshots
// (its first own tag — "plan:nightly" for a job taken over from Backrest, so
// Backrest's older snapshots count too — else "job:<id>").
func retentionTag(job *store.Job) string {
	if len(job.Config.Tags) > 0 && job.Config.Tags[0] != "" {
		return job.Config.Tags[0]
	}
	return fmt.Sprintf("job:%d", job.ID)
}

func retentionText(r store.Retention) string {
	if r.Empty() {
		return "keep everything"
	}
	var p []string
	add := func(n int, what string) {
		if n > 0 {
			p = append(p, fmt.Sprintf("%d %s", n, what))
		}
	}
	add(r.KeepLast, "last")
	add(r.KeepHourly, "hourly")
	add(r.KeepDaily, "daily")
	add(r.KeepWeekly, "weekly")
	add(r.KeepMonthly, "monthly")
	add(r.KeepYearly, "yearly")
	t := "keep " + strings.Join(p, ", ")
	if r.Prune {
		t += ", pruned"
	}
	return t
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstN(v []string, n int) []string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// notifyRun tells the admins how a job went (failures always, successes
// when the job asks for them).
func (s *Server) notifyRun(job *store.Job, r *store.Run, summary string) {
	ctx := context.Background()
	switch r.Status {
	case "success":
		if job.Config.NotifySuccess {
			s.notifyAdmins(ctx, "success", job.Name+" OK", firstNonEmpty(summary, "done")+".", "success")
		}
	case "warning":
		if job.Config.NotifyFailure {
			s.notifyAdmins(ctx, "failure", job.Name+" — done with warnings", warnText(r)+firstNonEmpty(" "+summary, "")+".", "warning")
		}
	case "failed":
		if job.Config.NotifyFailure {
			s.notifyAdmins(ctx, "failure", job.Name+" FAILED", firstNonEmpty(r.Error, "see the run")+".", "failure")
		}
	}
}

func warnText(r *store.Run) string {
	var w []string
	for _, st := range r.Steps {
		if st.Status == "warning" || st.Status == "failed" {
			w = append(w, st.Name)
		}
	}
	if len(w) == 0 {
		return ""
	}
	return "Check: " + strings.Join(w, ", ") + "."
}

// ---------- pull ----------

func (s *Server) runPull(rn *runner, job *store.Job) {
	ctx := rn.ctx
	p := job.Config.Pull
	if p == nil || p.ConnectionID == 0 || p.LocalPath == "" {
		rn.finish("failed", "", errors.New("this pull has no connection or local folder"))
		s.notifyRun(job, rn.run, "")
		return
	}
	conn, err := s.store.GetConnection(ctx, p.ConnectionID)
	if err != nil {
		rn.finish("failed", "", errors.New("its connection is gone"))
		s.notifyRun(job, rn.run, "")
		return
	}
	if err := rn.step("Check the folder", func() (string, string, error) {
		mounts := append([]string{}, job.Config.RequireMounts...)
		if p.RequireMount {
			mounts = append(mounts, filepath.Dir(strings.TrimRight(p.LocalPath, "/")))
		}
		if len(mounts) == 0 {
			return p.LocalPath, "ok", os.MkdirAll(p.LocalPath, 0o755)
		}
		return strings.Join(mounts, ", "), "ok", s.requireMounts(mounts)
	}); err != nil {
		rn.finish("failed", "", nil)
		s.notifyRun(job, rn.run, "")
		return
	}
	extra := append([]string{}, p.ExtraFlags...)
	if conn.Kind == "gdrive" {
		extra = append(extra, "--drive-skip-shortcuts")
		if p.ExportFormats != "" {
			extra = append(extra, "--drive-export-formats", p.ExportFormats)
		}
	}
	if p.TPSLimit > 0 {
		extra = append(extra, "--tpslimit", fmt.Sprint(p.TPSLimit))
	}
	extra = append(extra, "--fast-list", "--checkers", "16")
	o := engine.SyncOptions{Copy: !p.Mirror, Transfers: firstPositive(p.Transfers, 8), Extra: extra, UseTrash: true}
	rn.step("Copy "+conn.Name, func() (string, string, error) { //nolint:errcheck
		src := remotePath(conn, p.RemotePath)
		return src + " → " + p.LocalPath, "ok", s.eng.Sync(ctx, src, p.LocalPath, o, rn.log)
	})
	if p.SharedWithMe && conn.Kind == "gdrive" && p.SharedPath != "" {
		so := o
		so.Extra = append(append([]string{}, extra...), "--drive-shared-with-me")
		rn.step("Copy Shared with me", func() (string, string, error) { //nolint:errcheck
			return "→ " + p.SharedPath, "ok", s.eng.Sync(ctx, remotePath(conn, ""), p.SharedPath, so, rn.log)
		})
	}
	if job.Config.HeartbeatURL != "" && rn.status() != "failed" {
		rn.soft("Heartbeat", func() (string, string, error) { return "pinged", "ok", s.heartbeat(ctx, job.Config.HeartbeatURL, "") })
	}
	summary := "copied " + conn.Name + " to " + p.LocalPath
	rn.finish("", summary, nil)
	s.notifyRun(job, rn.run, summary)
}

func firstPositive(v ...int) int {
	for _, n := range v {
		if n > 0 {
			return n
		}
	}
	return 0
}

// ---------- repository maintenance ----------

// StartRepoTask runs a check, prune, unlock or stats read of a repository in the background.
func (s *Server) StartRepoTask(ctx context.Context, repo *store.Repository, kind, trigger string, userID int64, readPct int) (*store.Run, error) {
	titles := map[string]string{"check": "Check " + repo.Name, "prune": "Prune " + repo.Name, "unlock": "Unlock " + repo.Name, "init": "Create " + repo.Name, "stats": "Read " + repo.Name}
	r := &store.Run{RepositoryID: &repo.ID, Kind: kind, Trigger: trigger, Title: titles[kind]}
	if userID > 0 {
		r.UserID = &userID
	}
	rn, err := s.begin(ctx, r)
	if err != nil {
		return nil, err
	}
	go s.repoTask(rn, repo, kind, readPct)
	return r, nil
}

func (s *Server) repoTask(rn *runner, repo *store.Repository, kind string, readPct int) {
	ctx := rn.ctx
	lock := s.repoLock(repo.ID)
	lock.Lock()
	defer lock.Unlock()
	spec, err := s.repoSpec(ctx, repo)
	if err != nil {
		rn.finish("failed", "", err)
		return
	}
	if usb := usbOf(repo); usb != nil {
		if err := rn.step("Mount the disk", func() (string, string, error) { return usb.Mountpoint, "ok", s.eng.MountUSB(ctx, *usb, rn.log) }); err != nil {
			rn.finish("failed", "", nil)
			if errors.Is(err, engine.ErrDiskAbsent) && rn.run.Trigger == "schedule" {
				return // a scheduled check of an offline disk waits for the disk
			}
			s.notifyAdmins(ctx, "failure", rn.run.Title+" FAILED", err.Error(), "failure")
			return
		}
		defer s.unmountAfter(rn, repo, usb)
	}
	var stepErr error
	switch kind {
	case "check":
		if readPct == 0 {
			readPct = repo.CheckReadPct
		}
		stepErr = rn.step("Check", func() (string, string, error) {
			d := "structure"
			if readPct > 0 {
				d = fmt.Sprintf("structure and %d%% of the data", readPct)
			}
			return d, "ok", s.eng.Check(ctx, spec, readPct, rn.log)
		})
		if stepErr != nil {
			_ = s.store.SetCopiesPaused(ctx, repo.ID, true, "check failed "+time.Now().Format("2006-01-02")+": "+stepErr.Error())
			s.notifyAdmins(ctx, "failure", "Backup repository CORRUPT: "+repo.Name,
				"The check failed — copies of this repository are paused so the damage cannot overwrite the last good copies elsewhere. "+stepErr.Error(), "failure")
		} else if repo.CopiesPaused && strings.HasPrefix(repo.PausedReason, "check failed") {
			_ = s.store.SetCopiesPaused(ctx, repo.ID, false, "")
			rn.log.Printf("copies resumed: the check passed")
		}
	case "prune":
		stepErr = rn.step("Prune", func() (string, string, error) { return "", "ok", s.eng.Prune(ctx, spec, rn.log) })
	case "unlock":
		stepErr = rn.step("Unlock", func() (string, string, error) { return "stale locks removed", "ok", s.eng.Unlock(ctx, spec, rn.log) })
	case "init":
		stepErr = rn.step("Create the repository", func() (string, string, error) {
			if s.eng.IsRepo(ctx, spec) == nil {
				return "it already exists — using it", "ok", nil
			}
			if repo.Location == "connection" && repo.ConnectionID != nil {
				if c, err := s.store.GetConnection(ctx, *repo.ConnectionID); err == nil {
					_ = s.eng.Mkdir(ctx, remotePath(c, repo.Path))
				}
			} else if repo.Location == "local" || repo.Location == "usb" {
				_ = os.MkdirAll(filepath.Dir(repo.Path), 0o755)
			}
			return "", "ok", s.eng.Init(ctx, spec, rn.log)
		})
	}
	if stepErr == nil || kind == "stats" {
		rn.soft("Repository size", func() (string, string, error) {
			st, err := s.refreshRepoStats(ctx, repo)
			if err != nil {
				return "", "", err
			}
			return fmt.Sprintf("%s in %d snapshot(s)", engine.HumanBytes(st.Size), st.Snapshots), "ok", nil
		})
	}
	rn.finish("", "", nil)
	if rn.run.Status == "failed" && kind != "check" {
		s.notifyAdmins(ctx, "failure", rn.run.Title+" FAILED", firstNonEmpty(rn.run.Error, "see the run"), "failure")
	}
}

// ---------- single source ----------

// StartSourceRun captures one source now (to try it out).
func (s *Server) StartSourceRun(ctx context.Context, src *store.Source, userID int64) (*store.Run, error) {
	r := &store.Run{Kind: "source", Trigger: "manual", Title: "Capture " + src.Name}
	if userID > 0 {
		r.UserID = &userID
	}
	staging := s.cfg.StagingDir + "/try"
	if len(src.Jobs) > 0 {
		if j, err := s.store.GetJob(ctx, src.Jobs[0]); err == nil {
			staging = s.stagingDir(j)
			r.JobID = &j.ID
		}
	}
	rn, err := s.begin(ctx, r)
	if err != nil {
		return nil, err
	}
	go func() {
		var pw string
		if src.Secret != "" {
			pw, _ = s.open(src.Secret)
		}
		t := time.Now()
		var res *engine.Capture
		err := rn.step(src.Name, func() (string, string, error) {
			var err error
			res, err = s.eng.CaptureSource(rn.ctx, src, pw, staging, rn.log)
			if res != nil && res.Skipped {
				return res.Detail, "skipped", nil
			}
			if res != nil && res.Output != "" && err == nil {
				return res.Detail + " → " + filepath.Join(staging, res.Output), "ok", nil
			}
			if res != nil {
				return res.Detail, "ok", err
			}
			return "", "", err
		})
		if err != nil {
			s.store.SetSourceResult(rn.ctx, src.ID, false, err.Error(), 0, time.Since(t))
		} else if res != nil && !res.Skipped {
			s.store.SetSourceResult(rn.ctx, src.ID, true, "", res.Size, time.Since(t))
		}
		rn.finish("", "", nil)
	}()
	return r, nil
}
