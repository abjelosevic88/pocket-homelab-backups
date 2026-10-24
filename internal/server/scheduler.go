package server

import (
	"context"
	"fmt"
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// scheduler starts what is due: this app's jobs and repository checks and
// prunes (Backrest runs its own plans), watches USB disks being plugged in,
// and keeps the coverage picture fresh.
func (s *Server) scheduler(ctx context.Context) {
	prev := time.Now()
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	slow := 0
	go s.refreshCoverage(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.dueJobs(ctx, prev, now)
			s.dueRepos(ctx, prev, now)
			s.watchDisks(ctx)
			prev = now
			slow++
			if slow%90 == 0 { // every 30 minutes
				go s.refreshCoverage(ctx)
				s.saveRefreshedTokens(ctx)
			}
			if slow%4320 == 0 { // daily
				s.store.PruneRuns(ctx, 180)
				_ = s.store.PurgeSessions(ctx)
			}
		}
	}
}

// firedRecently guards against starting the same thing twice in an hour:
// when the clocks go back, 02:00-03:00 happens twice.
func (s *Server) firedRecently(key string, now time.Time) bool {
	s.diskMu.Lock()
	defer s.diskMu.Unlock()
	if t, ok := s.fired[key]; ok && now.Sub(t) < 65*time.Minute {
		return true
	}
	s.fired[key] = now
	return false
}

func due(schedule string, prev, now time.Time) bool {
	if schedule == "" {
		return false
	}
	c, err := engine.ParseCron(schedule)
	if err != nil {
		return false
	}
	n := c.Next(prev)
	return !n.IsZero() && !n.After(now)
}

func (s *Server) dueJobs(ctx context.Context, prev, now time.Time) {
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return
	}
	for i := range jobs {
		j := &jobs[i]
		if !j.Enabled || j.Engine == "backrest" || !due(j.Schedule, prev, now) || s.firedRecently(fmt.Sprintf("job:%d", j.ID), now) {
			continue
		}
		if _, err := s.StartJob(ctx, j, "schedule", 0); err != nil {
			s.log.Warn("scheduled job did not start", "job", j.Name, "err", err)
		} else {
			s.log.Info("scheduled job started", "job", j.Name)
		}
	}
}

func (s *Server) dueRepos(ctx context.Context, prev, now time.Time) {
	repos, err := s.store.ListRepositories(ctx)
	if err != nil {
		return
	}
	for i := range repos {
		x := &repos[i]
		if x.ManagedBy == "backrest" {
			continue
		}
		if due(x.CheckSchedule, prev, now) && !s.firedRecently(fmt.Sprintf("check:%d", x.ID), now) {
			if _, err := s.StartRepoTask(ctx, x, "check", "schedule", 0, 0); err != nil {
				s.log.Warn("scheduled check did not start", "repository", x.Name, "err", err)
			}
		}
		if due(x.PruneSchedule, prev, now) && !s.firedRecently(fmt.Sprintf("prune:%d", x.ID), now) {
			if _, err := s.StartRepoTask(ctx, x, "prune", "schedule", 0, 0); err != nil {
				s.log.Warn("scheduled prune did not start", "repository", x.Name, "err", err)
			}
		}
	}
}

// watchDisks notices a USB disk being plugged in: it tells the admins and
// starts the jobs that asked to run then.
func (s *Server) watchDisks(ctx context.Context) {
	repos, err := s.store.ListRepositories(ctx)
	if err != nil {
		return
	}
	for i := range repos {
		x := &repos[i]
		u := x.Options.USB
		if x.Location != "usb" || u == nil || u.UUID == "" {
			continue
		}
		now := s.eng.USBConnected(u.UUID)
		s.diskMu.Lock()
		was, seen := s.diskSeen[x.ID]
		s.diskSeen[x.ID] = now
		s.diskMu.Unlock()
		if !seen || was || !now {
			continue
		}
		s.log.Info("usb disk connected", "repository", x.Name)
		jobs, _ := s.store.ListJobs(ctx)
		started := []string{}
		waiting := []string{}
		for k := range jobs {
			j := &jobs[k]
			if !j.Enabled || j.RepositoryID == nil || *j.RepositoryID != x.ID {
				continue
			}
			if !j.Config.RunOnConnect {
				waiting = append(waiting, j.Name)
				continue
			}
			if last, err := s.store.LastGoodRun(ctx, j.ID, "backup,post"); err == nil && j.Config.RunOnConnectDays > 0 &&
				time.Since(store.ParseTime(last.FinishedAt)) < time.Duration(j.Config.RunOnConnectDays)*24*time.Hour {
				waiting = append(waiting, j.Name)
				continue
			}
			if _, err := s.StartJob(ctx, j, "disk", 0); err == nil {
				started = append(started, j.Name)
			}
		}
		switch {
		case len(started) > 0:
			s.notifyAdmins(ctx, "success", "USB disk connected — backing up", x.Name+": started "+joinAnd(started)+". You will hear when it is safe to unplug.", "info")
		case len(waiting) > 0:
			s.notifyAdmins(ctx, "success", "USB disk connected", x.Name+" is plugged in. Start "+joinAnd(waiting)+" in Pocket Backups.", "info")
		}
		s.touch()
	}
}

func joinAnd(v []string) string {
	switch len(v) {
	case 0:
		return ""
	case 1:
		return v[0]
	}
	out := ""
	for i, s := range v {
		switch {
		case i == 0:
			out = s
		case i == len(v)-1:
			out += " and " + s
		default:
			out += ", " + s
		}
	}
	return out
}

func (s *Server) refreshCoverage(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	rep := s.coverage(c)
	s.covMu.Lock()
	s.lastCov = &rep
	s.covMu.Unlock()
}

func (s *Server) lastCoverage() *coverageReport {
	s.covMu.Lock()
	defer s.covMu.Unlock()
	return s.lastCov
}
