package server

import (
	"context"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// The coverage check: every running container should belong to something a
// backup covers. Covered = a source names its container, volume or compose
// project, or its compose folder is inside a path a job (or a Backrest plan
// a job runs on) backs up. Skipped = left out on purpose, with a reason.

type coverageProject struct {
	Project    string             `json:"project"`
	WorkingDir string             `json:"working_dir"`
	Containers []engine.Container `json:"containers"`
	Status     string             `json:"status"` // covered | skipped | uncovered
	Reason     string             `json:"reason"`
	By         []string           `json:"by"`
	Volumes    []volumeCoverage   `json:"volumes"`
}

type volumeCoverage struct {
	Name    string `json:"name"`
	Covered bool   `json:"covered"`
}

type coverageReport struct {
	Projects  []coverageProject `json:"projects"`
	Covered   int               `json:"covered"`
	Skipped   int               `json:"skipped"`
	Uncovered int               `json:"uncovered"`
	Error     string            `json:"error,omitempty"`
}

func underAny(p string, roots []string) bool {
	p = filepath.Clean(p)
	for _, r := range roots {
		r = filepath.Clean(r)
		if r == "/" || p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// coveredPaths is every folder some job backs up: its own paths, its paths
// sources, and the paths of the Backrest plan it runs on.
func (s *Server) coveredPaths(ctx context.Context) (paths map[string][]string) {
	paths = map[string][]string{}
	jobs, _ := s.store.ListJobs(ctx)
	var brPlans map[string][]string
	if s.br.Configured() {
		if cfg, err := s.br.GetConfig(ctx); err == nil {
			brPlans = map[string][]string{}
			for _, p := range cfg.Plans {
				brPlans[p.ID] = p.Paths
			}
		}
	}
	for _, j := range jobs {
		if !j.Enabled || j.Kind != "backup" {
			continue
		}
		ps, _ := s.backupPaths(ctx, &j)
		if j.Engine == "backrest" {
			ps = append(ps, brPlans[j.BackrestPlan]...)
		}
		for _, p := range ps {
			paths[p] = append(paths[p], j.Name)
		}
	}
	return paths
}

func (s *Server) coverage(ctx context.Context) coverageReport {
	rep := coverageReport{Projects: []coverageProject{}}
	containers, err := s.eng.Containers(ctx)
	if err != nil {
		rep.Error = "cannot read Docker: " + err.Error()
		return rep
	}
	sources, _ := s.store.ListSources(ctx)
	skips, _ := s.store.ListCoverageSkips(ctx)
	skip := map[string]string{}
	for _, k := range skips {
		skip[k.Project] = k.Reason
	}
	covered := s.coveredPaths(ctx)
	roots := make([]string, 0, len(covered))
	for p := range covered {
		roots = append(roots, p)
	}
	byContainer, byVolume, byProject := map[string][]string{}, map[string]bool{}, map[string][]string{}
	for _, x := range sources {
		if !x.Enabled || len(x.Jobs) == 0 {
			continue
		}
		if x.Config.Container != "" {
			byContainer[x.Config.Container] = append(byContainer[x.Config.Container], x.Name)
		}
		if x.Config.Volume != "" {
			byVolume[x.Config.Volume] = true
		}
		if x.Project != "" {
			byProject[x.Project] = append(byProject[x.Project], x.Name)
		}
	}
	groups := map[string]*coverageProject{}
	order := []string{}
	for _, c := range containers {
		key := c.Project
		if key == "" {
			key = c.Name // a lone container is its own project
		}
		g, ok := groups[key]
		if !ok {
			g = &coverageProject{Project: key, WorkingDir: c.WorkingDir, By: []string{}, Volumes: []volumeCoverage{}}
			groups[key] = g
			order = append(order, key)
		}
		g.Containers = append(g.Containers, c)
	}
	sort.Strings(order)
	for _, key := range order {
		g := groups[key]
		by := map[string]bool{}
		for _, n := range byProject[key] {
			by["source "+n] = true
		}
		vols := map[string]bool{}
		for _, c := range g.Containers {
			for _, n := range byContainer[c.Name] {
				by["source "+n] = true
			}
			for _, m := range c.Mounts {
				if m.Type == "volume" && m.Name != "" && !vols[m.Name] {
					vols[m.Name] = true
					g.Volumes = append(g.Volumes, volumeCoverage{Name: m.Name, Covered: byVolume[m.Name]})
					if byVolume[m.Name] {
						by["volume "+m.Name] = true
					}
				}
			}
		}
		if g.WorkingDir != "" && underAny(g.WorkingDir, roots) {
			for p, jobs := range covered {
				if underAny(g.WorkingDir, []string{p}) {
					for _, j := range jobs {
						by["folder in "+j] = true
					}
				}
			}
		}
		for b := range by {
			g.By = append(g.By, b)
		}
		sort.Strings(g.By)
		switch {
		case len(g.By) > 0:
			g.Status = "covered"
			rep.Covered++
		case skip[key] != "" || hasKey(skip, key):
			g.Status, g.Reason = "skipped", skip[key]
			rep.Skipped++
		default:
			g.Status = "uncovered"
			rep.Uncovered++
		}
		rep.Projects = append(rep.Projects, *g)
	}
	return rep
}

func hasKey(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

// coverageStep is the coverage check as a step of a run (warns, and tells
// the admins, when something is not covered).
func (s *Server) coverageStep(ctx context.Context) (string, string, error) {
	rep := s.coverage(ctx)
	if rep.Error != "" {
		return rep.Error, "warning", nil
	}
	if rep.Uncovered == 0 {
		return "all containers covered", "ok", nil
	}
	var names []string
	for _, p := range rep.Projects {
		if p.Status == "uncovered" {
			names = append(names, p.Project)
		}
	}
	msg := "not covered by any backup: " + strings.Join(names, ", ")
	s.notifyAdmins(ctx, "failure", "Backup coverage warning",
		"These running containers are NOT covered by backups — new app? Add a source for them in Pocket Backups, or mark them skipped: "+strings.Join(names, ", "), "warning")
	// told separately (notification + the overview); the backup itself is fine
	return msg, "ok", nil
}

func (s *Server) handleCoverage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rep := s.coverage(r.Context())
	s.covMu.Lock()
	s.lastCov = &rep
	s.covMu.Unlock()
	writeJSON(w, http.StatusOK, rep)
}

// coverageChanged recomputes the coverage picture shortly after sources,
// jobs or skips change (debounced).
func (s *Server) coverageChanged() {
	s.covMu.Lock()
	defer s.covMu.Unlock()
	if s.covTimer != nil {
		s.covTimer.Stop()
	}
	s.covTimer = time.AfterFunc(5*time.Second, func() { s.refreshCoverage(context.Background()) })
}

func (s *Server) handleCoverageSkip(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in struct {
		Project string `json:"project"`
		Reason  string `json:"reason"`
		Skip    bool   `json:"skip"`
	}
	if err := decode(r, &in); err != nil || strings.TrimSpace(in.Project) == "" {
		writeErr(w, http.StatusBadRequest, "which project?")
		return
	}
	if err := s.store.SetCoverageSkip(r.Context(), strings.TrimSpace(in.Project), strings.TrimSpace(in.Reason), in.Skip); err != nil {
		s.fail(w, err, "coverage")
		return
	}
	s.touch()
	rep := s.coverage(r.Context())
	s.covMu.Lock()
	s.lastCov = &rep
	s.covMu.Unlock()
	writeJSON(w, http.StatusOK, rep)
}

// ---------- discovery ----------

type suggestion struct {
	Kind      string             `json:"kind"`
	Name      string             `json:"name"`
	Project   string             `json:"project"`
	Config    store.SourceConfig `json:"config"`
	Why       string             `json:"why"`
	Exists    bool               `json:"exists"`
	Container string             `json:"container"`
}

// handleDiscover suggests sources from what runs: databases in containers
// (by image), SQLite files in compose folders, named volumes.
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	containers, err := s.eng.Containers(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "cannot read Docker: "+err.Error())
		return
	}
	sources, _ := s.store.ListSources(ctx)
	have := map[string]bool{}
	for _, x := range sources {
		have[x.Kind+"|"+x.Config.Container+"|"+x.Config.Path+"|"+x.Config.Volume] = true
	}
	out := []suggestion{}
	add := func(sg suggestion) {
		sg.Exists = have[sg.Kind+"|"+sg.Config.Container+"|"+sg.Config.Path+"|"+sg.Config.Volume]
		out = append(out, sg)
	}
	for _, c := range containers {
		img := strings.ToLower(c.Image)
		switch {
		case strings.Contains(img, "postgres") || strings.Contains(img, "pgvecto") || strings.Contains(img, "postgis") || strings.Contains(img, "documentdb"):
			add(suggestion{Kind: "postgres", Name: firstNonEmpty(c.Project, c.Name) + " database", Project: c.Project, Container: c.Name,
				Config: store.SourceConfig{Container: c.Name, Output: "db/" + firstNonEmpty(c.Project, c.Name) + ".sql"}, Why: "PostgreSQL in " + c.Name})
		case strings.Contains(img, "mariadb") || strings.Contains(img, "mysql"):
			add(suggestion{Kind: "mariadb", Name: firstNonEmpty(c.Project, c.Name) + " database", Project: c.Project, Container: c.Name,
				Config: store.SourceConfig{Container: c.Name, Output: "db/" + firstNonEmpty(c.Project, c.Name) + ".sql"}, Why: "MariaDB/MySQL in " + c.Name})
		}
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" && len(m.Name) < 60 && !looksLikeDBPath(m.Destination) {
				add(suggestion{Kind: "volume", Name: m.Name, Project: c.Project, Container: c.Name,
					Config: store.SourceConfig{Volume: m.Name}, Why: "named volume at " + m.Destination + " in " + c.Name})
			}
		}
		if c.WorkingDir != "" {
			for _, db := range findSQLite(c.WorkingDir, 3) {
				rel, _ := filepath.Rel(c.WorkingDir, db)
				add(suggestion{Kind: "sqlite", Name: firstNonEmpty(c.Project, c.Name) + " " + strings.TrimSuffix(filepath.Base(db), filepath.Ext(db)), Project: c.Project, Container: c.Name,
					Config: store.SourceConfig{Path: db}, Why: "SQLite file " + rel + " in " + c.WorkingDir})
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func looksLikeDBPath(p string) bool {
	return strings.Contains(p, "/var/lib/postgresql") || strings.Contains(p, "/var/lib/mysql")
}
