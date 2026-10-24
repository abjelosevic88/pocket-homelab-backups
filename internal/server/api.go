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
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// ---------- repositories ----------

type repoView struct {
	store.Repository
	Connected *bool   `json:"disk_connected,omitempty"`
	Mounted   *bool   `json:"disk_mounted,omitempty"`
	URI       string  `json:"uri"`
	Jobs      []int64 `json:"jobs"`
}

func (s *Server) repoView(ctx context.Context, r store.Repository, jobs []store.Job) repoView {
	v := repoView{Repository: r, Jobs: []int64{}}
	if spec, err := s.repoSpec(ctx, &r); err == nil {
		v.URI = spec.URI
	}
	if u := r.Options.USB; u != nil && r.Location == "usb" {
		c, m := s.eng.USBConnected(u.UUID), engine.Mounted(u.Mountpoint)
		v.Connected, v.Mounted = &c, &m
	}
	for _, j := range jobs {
		if j.RepositoryID != nil && *j.RepositoryID == r.ID {
			v.Jobs = append(v.Jobs, j.ID)
		}
		for _, c := range j.Copies {
			if c.TargetRepositoryID != nil && *c.TargetRepositoryID == r.ID {
				v.Jobs = append(v.Jobs, j.ID)
			}
		}
	}
	return v
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	list, err := s.store.ListRepositories(ctx)
	if err != nil {
		s.fail(w, err, "repositories")
		return
	}
	jobs, _ := s.store.ListJobs(ctx)
	out := make([]repoView, 0, len(list))
	for _, x := range list {
		out = append(out, s.repoView(ctx, x, jobs))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetRepo(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	x, err := s.store.GetRepository(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "repository")
		return
	}
	jobs, _ := s.store.ListJobs(ctx)
	writeJSON(w, http.StatusOK, s.repoView(ctx, *x, jobs))
}

type repoIn struct {
	Name          string             `json:"name"`
	Location      string             `json:"location"`
	Path          string             `json:"path"`
	ConnectionID  *int64             `json:"connection_id"`
	Password      *string            `json:"password"`
	PasswordFile  *string            `json:"password_file"`
	Options       *store.RepoOptions `json:"options"`
	CheckSchedule *string            `json:"check_schedule"`
	CheckReadPct  *int               `json:"check_read_pct"`
	PruneSchedule *string            `json:"prune_schedule"`
	Create        bool               `json:"create"` // initialise a new repository there
}

func (s *Server) handleSaveRepo(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in repoIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	x := &store.Repository{ManagedBy: "pocket"}
	if id := idParam(r, "id"); id > 0 {
		var err error
		if x, err = s.store.GetRepository(ctx, id); err != nil {
			s.fail(w, err, "repository")
			return
		}
	}
	if in.Name != "" {
		x.Name = strings.TrimSpace(in.Name)
	}
	if in.Location != "" {
		x.Location = in.Location
	}
	if in.Path != "" || x.ID == 0 {
		x.Path = strings.TrimSpace(in.Path)
	}
	if in.ConnectionID != nil {
		x.ConnectionID = in.ConnectionID
	}
	if in.Options != nil {
		x.Options = *in.Options
	}
	if in.CheckSchedule != nil {
		x.CheckSchedule = strings.TrimSpace(*in.CheckSchedule)
	}
	if in.CheckReadPct != nil {
		x.CheckReadPct = min(max(*in.CheckReadPct, 0), 100)
	}
	if in.PruneSchedule != nil {
		x.PruneSchedule = strings.TrimSpace(*in.PruneSchedule)
	}
	if in.PasswordFile != nil {
		x.PasswordFile = strings.TrimSpace(*in.PasswordFile)
		if x.PasswordFile != "" {
			x.Password = ""
		}
	}
	if in.Password != nil && *in.Password != "" {
		sealed, err := s.seal(*in.Password)
		if err != nil {
			s.fail(w, err, "seal")
			return
		}
		x.Password, x.PasswordFile = sealed, ""
	}
	if x.ID == 0 && x.Password == "" && x.PasswordFile == "" && in.Create {
		// a new repository: make a strong password the person can reveal later
		sealed, err := s.seal(store.RandomToken(24))
		if err != nil {
			s.fail(w, err, "seal")
			return
		}
		x.Password = sealed
	}
	if err := validateRepo(x); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SaveRepository(ctx, x); err != nil {
		s.fail(w, err, "save repository")
		return
	}
	s.touch()
	res := map[string]any{"repository": x}
	if in.Create {
		run, err := s.StartRepoTask(ctx, x, "init", "manual", me(r), 0)
		if err == nil {
			res["run"] = run
		}
	} else if usbOf(x) == nil || engine.Mounted(x.Options.USB.Mountpoint) {
		go func() {
			if _, err := s.refreshRepoStats(context.Background(), x); err != nil {
				s.log.Debug("repository stats", "repository", x.Name, "err", err)
			}
		}()
	}
	writeJSON(w, http.StatusOK, res)
}

func validateRepo(x *store.Repository) error {
	if x.Name == "" {
		return errors.New("give the repository a name")
	}
	switch x.Location {
	case "local":
		if !filepath.IsAbs(x.Path) {
			return errors.New("a folder on this box must be an absolute path")
		}
	case "usb":
		if x.Options.USB == nil || x.Options.USB.UUID == "" || x.Options.USB.Mountpoint == "" {
			return errors.New("a USB disk needs its filesystem UUID and a mount point")
		}
		if !filepath.IsAbs(x.Path) || !strings.HasPrefix(x.Path, strings.TrimRight(x.Options.USB.Mountpoint, "/")+"/") {
			return errors.New("the repository must be a folder on the disk (inside its mount point)")
		}
	case "connection":
		if x.ConnectionID == nil {
			return errors.New("which connection?")
		}
	case "uri":
		if x.Path == "" {
			return errors.New("which repository?")
		}
	default:
		return errors.New("location must be local, usb, connection or uri")
	}
	if x.CheckSchedule != "" {
		if _, err := engine.ParseCron(x.CheckSchedule); err != nil {
			return err
		}
	}
	if x.PruneSchedule != "" {
		if _, err := engine.ParseCron(x.PruneSchedule); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	err := s.store.DeleteRepository(r.Context(), idParam(r, "id"))
	if errors.Is(err, store.ErrInUse) {
		writeErr(w, http.StatusConflict, "a job still uses this repository")
		return
	}
	if err != nil {
		s.fail(w, err, "delete repository")
		return
	}
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

// handleRepoAction runs check | prune | unlock | stats | init now.
func (s *Server) handleRepoAction(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	x, err := s.store.GetRepository(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "repository")
		return
	}
	var in struct {
		Action  string `json:"action"`
		ReadPct int    `json:"read_pct"`
	}
	_ = decode(r, &in)
	switch in.Action {
	case "check", "prune", "unlock", "stats", "init":
	case "resume":
		_ = s.store.SetCopiesPaused(ctx, x.ID, false, "")
		s.touch()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case "pause":
		_ = s.store.SetCopiesPaused(ctx, x.ID, true, "paused by hand")
		s.touch()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case "mount", "unmount":
		u := usbOf(x)
		if u == nil {
			writeErr(w, http.StatusBadRequest, "this repository is not on a USB disk")
			return
		}
		if in.Action == "mount" {
			err = s.eng.MountUSB(ctx, *u, nil)
		} else {
			var safe bool
			safe, err = s.eng.UnmountUSB(ctx, *u, nil)
			if err == nil && !safe {
				err = errors.New("not safe to unplug yet")
			}
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.touch()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	default:
		writeErr(w, http.StatusBadRequest, "unknown action")
		return
	}
	run, err := s.StartRepoTask(ctx, x, in.Action, "manual", me(r), in.ReadPct)
	if err != nil {
		s.fail(w, err, in.Action)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

// handleRevealPassword shows a repository's password (an admin's password
// is asked again with local accounts).
func (s *Server) handleRevealPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	x, err := s.store.GetRepository(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "repository")
		return
	}
	pw := ""
	if x.Password != "" {
		if pw, err = s.open(x.Password); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else if x.PasswordFile != "" {
		b, err := os.ReadFile(x.PasswordFile)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "cannot read "+x.PasswordFile+": "+err.Error())
			return
		}
		pw = strings.TrimSpace(string(b))
	}
	s.log.Info("repository password shown", "repository", x.Name, "by", userFrom(ctx).Email)
	writeJSON(w, http.StatusOK, map[string]string{"password": pw})
}

// ---------- snapshots + restore ----------

type cachedSnaps struct {
	at    time.Time
	snaps []engine.Snapshot
}

func (s *Server) repoForRead(w http.ResponseWriter, r *http.Request) (*store.Repository, engine.Repo, bool) {
	ctx := r.Context()
	x, err := s.store.GetRepository(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "repository")
		return nil, engine.Repo{}, false
	}
	spec, err := s.repoSpec(ctx, x)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, spec, false
	}
	if u := x.Options.USB; u != nil && x.Location == "usb" && !engine.Mounted(u.Mountpoint) {
		writeErr(w, http.StatusConflict, "the disk is not mounted — mount it first (it is unmounted again when you are done)")
		return nil, spec, false
	}
	return x, spec, true
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	x, spec, ok := s.repoForRead(w, r)
	if !ok {
		return
	}
	if c, ok := s.snapCache.Load(x.ID); ok && !queryBool(r, "fresh") && time.Since(c.(cachedSnaps).at) < 2*time.Minute {
		writeJSON(w, http.StatusOK, reverse(c.(cachedSnaps).snaps))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	snaps, err := s.eng.Snapshots(ctx, spec)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.snapCache.Store(x.ID, cachedSnaps{at: time.Now(), snaps: snaps})
	writeJSON(w, http.StatusOK, reverse(snaps))
}

func reverse(in []engine.Snapshot) []engine.Snapshot {
	out := make([]engine.Snapshot, len(in))
	for i, s := range in {
		out[len(in)-1-i] = s
	}
	return out
}

func validSnap(id string) bool {
	if id == "latest" {
		return true
	}
	if len(id) < 6 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s *Server) handleLs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	_, spec, ok := s.repoForRead(w, r)
	if !ok {
		return
	}
	snap := r.URL.Query().Get("snapshot")
	if !validSnap(snap) {
		writeErr(w, http.StatusBadRequest, "which snapshot?")
		return
	}
	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = "/"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	nodes, err := s.eng.Ls(ctx, spec, snap, dir)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	sort.Slice(nodes, func(i, j int) bool {
		if (nodes[i].Type == "dir") != (nodes[j].Type == "dir") {
			return nodes[i].Type == "dir"
		}
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": dir, "entries": nodes})
}

// handleDownload streams one file of a snapshot, or a folder as a zip.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	x, spec, ok := s.repoForRead(w, r)
	if !ok {
		return
	}
	snap, p := r.URL.Query().Get("snapshot"), r.URL.Query().Get("path")
	if !validSnap(snap) || p == "" {
		writeErr(w, http.StatusBadRequest, "which snapshot and path?")
		return
	}
	archive := ""
	name := filepath.Base(p)
	if queryBool(r, "zip") {
		archive = "zip"
		name += ".zip"
		w.Header().Set("Content-Type", "application/zip")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, strings.ReplaceAll(name, `"`, "")))
	s.log.Info("restore download", "repository", x.Name, "snapshot", snap, "path", p, "by", userFrom(r.Context()).Email)
	if err := s.eng.Dump(r.Context(), spec, snap, p, archive, w); err != nil {
		s.log.Warn("restore download failed", "err", err)
	}
}

// handleRestore puts files back on the box (into their old place or a new one).
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	x, spec, ok := s.repoForRead(w, r)
	if !ok {
		return
	}
	var in struct {
		Snapshot  string   `json:"snapshot"`
		Paths     []string `json:"paths"`
		Target    string   `json:"target"`    // a folder; "/" = where they came from
		Overwrite string   `json:"overwrite"` // always | if-changed | if-newer | never
	}
	if err := decode(r, &in); err != nil || !validSnap(in.Snapshot) || len(in.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "which snapshot and paths?")
		return
	}
	in.Target = filepath.Clean(strings.TrimSpace(in.Target))
	if !filepath.IsAbs(in.Target) || !underAny(in.Target, []string{s.cfg.BrowseRoot}) {
		writeErr(w, http.StatusBadRequest, "restore into an absolute folder inside "+s.cfg.BrowseRoot)
		return
	}
	switch in.Overwrite {
	case "", "never", "if-changed", "if-newer", "always":
	default:
		writeErr(w, http.StatusBadRequest, "bad overwrite")
		return
	}
	if in.Overwrite == "" {
		in.Overwrite = "never"
	}
	run := &store.Run{RepositoryID: &x.ID, Kind: "restore", Trigger: "manual", Title: "Restore from " + x.Name, UserID: ptr(me(r))}
	rn, err := s.begin(r.Context(), run)
	if err != nil {
		s.fail(w, err, "restore")
		return
	}
	go func() {
		rn.step("Restore", func() (string, string, error) { //nolint:errcheck
			d := fmt.Sprintf("%d item(s) of %s into %s", len(in.Paths), shortID(in.Snapshot), in.Target)
			return d, "ok", s.eng.Restore(rn.ctx, spec, in.Snapshot, in.Target, in.Paths, in.Overwrite, rn.log)
		})
		rn.finish("", "", nil)
	}()
	writeJSON(w, http.StatusAccepted, run)
}

func ptr[T any](v T) *T { return &v }

// ---------- sources ----------

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	list, err := s.store.ListSources(r.Context())
	if err != nil {
		s.fail(w, err, "sources")
		return
	}
	type view struct {
		store.Source
		Output string `json:"output"`
	}
	out := make([]view, 0, len(list))
	for i := range list {
		o := ""
		if engine.NeedsStaging(list[i].Kind) {
			o = engine.DefaultOutput(&list[i])
		}
		out = append(out, view{list[i], o})
	}
	writeJSON(w, http.StatusOK, out)
}

type sourceIn struct {
	Name     string              `json:"name"`
	Kind     string              `json:"kind"`
	Enabled  *bool               `json:"enabled"`
	Config   *store.SourceConfig `json:"config"`
	Password *string             `json:"password"`
	Project  *string             `json:"project"`
	Notes    *string             `json:"notes"`
	Jobs     []int64             `json:"jobs"` // add to these jobs (new sources)
}

func (s *Server) handleSaveSource(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in sourceIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	x := &store.Source{Enabled: true}
	if id := idParam(r, "id"); id > 0 {
		var err error
		if x, err = s.store.GetSource(ctx, id); err != nil {
			s.fail(w, err, "source")
			return
		}
	} else {
		x.Kind = in.Kind
	}
	if in.Name != "" {
		x.Name = strings.TrimSpace(in.Name)
	}
	if in.Enabled != nil {
		x.Enabled = *in.Enabled
	}
	if in.Config != nil {
		x.Config = *in.Config
	}
	if in.Project != nil {
		x.Project = strings.TrimSpace(*in.Project)
	}
	if in.Notes != nil {
		x.Notes = *in.Notes
	}
	if in.Password != nil {
		sealed, err := s.seal(*in.Password)
		if err != nil {
			s.fail(w, err, "seal")
			return
		}
		x.Secret = sealed
	}
	if err := s.validateSource(x); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	isNew := x.ID == 0
	if err := s.store.SaveSource(ctx, x); err != nil {
		s.fail(w, err, "save source")
		return
	}
	if isNew {
		for _, jid := range in.Jobs {
			if j, err := s.store.GetJob(ctx, jid); err == nil {
				j.SourceIDs = append(j.SourceIDs, x.ID)
				_ = s.store.SaveJob(ctx, j)
			}
		}
	}
	x, _ = s.store.GetSource(ctx, x.ID)
	s.touch()
	s.coverageChanged()
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) validateSource(x *store.Source) error {
	if x.Name == "" {
		return errors.New("give the source a name")
	}
	c := x.Config
	switch x.Kind {
	case "paths":
		if len(c.Paths) == 0 {
			return errors.New("which folders?")
		}
		for _, p := range c.Paths {
			if !filepath.IsAbs(p) {
				return fmt.Errorf("%q is not an absolute path", p)
			}
		}
	case "sqlite":
		if !filepath.IsAbs(c.Path) {
			return errors.New("the database file must be an absolute path")
		}
	case "postgres", "mariadb", "container_files", "container_command":
		if c.Container == "" {
			return errors.New("which container?")
		}
		if x.Kind == "container_command" && strings.TrimSpace(c.Command) == "" {
			return errors.New("which command?")
		}
	case "volume":
		if c.Volume == "" {
			return errors.New("which volume?")
		}
	case "host_command":
		if strings.TrimSpace(c.Command) == "" {
			return errors.New("which command?")
		}
		if !s.cfg.HostAccess {
			return engine.ErrNoHostAccess
		}
	default:
		return fmt.Errorf("unknown kind %q", x.Kind)
	}
	if o := c.Output; o != "" && (filepath.IsAbs(o) || strings.Contains(o, "..")) {
		return errors.New("the output must be a relative path inside the staging folder")
	}
	return nil
}

func (s *Server) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.store.DeleteSource(r.Context(), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete source")
		return
	}
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunSource(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	x, err := s.store.GetSource(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "source")
		return
	}
	run, err := s.StartSourceRun(r.Context(), x, me(r))
	if err != nil {
		s.fail(w, err, "capture")
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

// ---------- jobs ----------

type jobView struct {
	store.Job
	NextRun  string     `json:"next_run"`
	Last     *store.Run `json:"last_run"`
	LastGood *store.Run `json:"last_good"`
	Running  *int64     `json:"running"`
}

func (s *Server) jobView(ctx context.Context, j store.Job) jobView {
	v := jobView{Job: j}
	if n := s.nextRun(&j); !n.IsZero() {
		v.NextRun = n.UTC().Format(time.RFC3339)
	}
	kinds := "backup,pull,post"
	v.Last, _ = s.store.LastRun(ctx, j.ID, kinds, true)
	v.LastGood, _ = s.store.LastGoodRun(ctx, j.ID, kinds)
	if j.Engine == "backrest" {
		// Backrest's own log knows about snapshots made before this app (or
		// without its hooks): use its newest backup when that is newer
		if br := s.backrestLastBackup(ctx, j.BackrestPlan); br != nil {
			if v.Last == nil || br.FinishedAt > v.Last.FinishedAt {
				v.Last = br
			}
			if br.Status != "failed" && (v.LastGood == nil || br.FinishedAt > v.LastGood.FinishedAt) {
				v.LastGood = br
			}
		}
	}
	s.activeMu.Lock()
	for id, a := range s.active {
		if a.run.JobID != nil && *a.run.JobID == j.ID {
			rid := id
			v.Running = &rid
		}
	}
	s.activeMu.Unlock()
	return v
}

// nextRun is when a job runs next (for Backrest jobs: the plan's schedule).
func (s *Server) nextRun(j *store.Job) time.Time {
	if !j.Enabled {
		return time.Time{}
	}
	return engine.NextRun(j.Schedule, time.Now())
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	list, err := s.store.ListJobs(ctx)
	if err != nil {
		s.fail(w, err, "jobs")
		return
	}
	out := make([]jobView, 0, len(list))
	for _, j := range list {
		out = append(out, s.jobView(ctx, j))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	j, err := s.store.GetJob(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "job")
		return
	}
	writeJSON(w, http.StatusOK, s.jobView(r.Context(), *j))
}

type jobIn struct {
	Name         string           `json:"name"`
	Kind         string           `json:"kind"`
	Engine       string           `json:"engine"`
	BackrestPlan *string          `json:"backrest_plan"`
	RepositoryID *int64           `json:"repository_id"`
	Schedule     *string          `json:"schedule"`
	Enabled      *bool            `json:"enabled"`
	Config       *store.JobConfig `json:"config"`
	SourceIDs    []int64          `json:"source_ids"`
}

func (s *Server) handleSaveJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in jobIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	j := &store.Job{Kind: "backup", Engine: "pocket", Enabled: true, Config: store.JobConfig{NotifyFailure: true}}
	if id := idParam(r, "id"); id > 0 {
		var err error
		if j, err = s.store.GetJob(ctx, id); err != nil {
			s.fail(w, err, "job")
			return
		}
	}
	if in.Name != "" {
		j.Name = strings.TrimSpace(in.Name)
	}
	if in.Kind != "" {
		j.Kind = in.Kind
	}
	if in.Engine != "" {
		j.Engine = in.Engine
	}
	if in.BackrestPlan != nil {
		j.BackrestPlan = strings.TrimSpace(*in.BackrestPlan)
	}
	if in.RepositoryID != nil {
		j.RepositoryID = in.RepositoryID
		if *in.RepositoryID <= 0 {
			j.RepositoryID = nil
		}
	}
	if in.Schedule != nil {
		j.Schedule = strings.TrimSpace(*in.Schedule)
	}
	if in.Enabled != nil {
		j.Enabled = *in.Enabled
	}
	if in.Config != nil {
		j.Config = *in.Config
	}
	if in.SourceIDs != nil {
		j.SourceIDs = in.SourceIDs
	}
	if err := validateJob(j); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SaveJob(ctx, j); err != nil {
		s.fail(w, err, "save job")
		return
	}
	j, _ = s.store.GetJob(ctx, j.ID)
	s.touch()
	s.coverageChanged()
	writeJSON(w, http.StatusOK, s.jobView(ctx, *j))
}

func validateJob(j *store.Job) error {
	if j.Name == "" {
		return errors.New("give the job a name")
	}
	switch j.Kind {
	case "backup":
		if j.RepositoryID == nil {
			return errors.New("where should it go? Choose a repository")
		}
	case "pull":
		p := j.Config.Pull
		if p == nil || p.ConnectionID == 0 || !filepath.IsAbs(p.LocalPath) {
			return errors.New("a pull needs a connection and an absolute local folder")
		}
	default:
		return errors.New("kind must be backup or pull")
	}
	switch j.Engine {
	case "pocket":
	case "backrest":
		if j.BackrestPlan == "" {
			return errors.New("which Backrest plan?")
		}
	default:
		return errors.New("engine must be pocket or backrest")
	}
	if j.Schedule != "" {
		if _, err := engine.ParseCron(j.Schedule); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.store.DeleteJob(r.Context(), idParam(r, "id")); err != nil {
		s.fail(w, err, "delete job")
		return
	}
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRunJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	j, err := s.store.GetJob(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "job")
		return
	}
	run, err := s.StartJob(r.Context(), j, "manual", me(r))
	if errors.Is(err, errBusy) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		s.fail(w, err, "run job")
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

// ---------- copies ----------

func (s *Server) handleSaveCopy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in store.Copy
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	jobID := idParam(r, "id")
	if _, err := s.store.GetJob(ctx, jobID); err != nil {
		s.fail(w, err, "job")
		return
	}
	c := &store.Copy{JobID: jobID, Enabled: true}
	if cid := idParam(r, "cid"); cid > 0 {
		var err error
		if c, err = s.store.GetCopy(ctx, cid); err != nil || c.JobID != jobID {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
	}
	c.Name, c.Mode, c.ConnectionID, c.Path, c.TargetRepositoryID, c.Enabled, c.Options = strings.TrimSpace(in.Name), firstNonEmpty(in.Mode, "mirror"), in.ConnectionID, strings.TrimSpace(in.Path), in.TargetRepositoryID, in.Enabled, in.Options
	if in.SortOrder != 0 {
		c.SortOrder = in.SortOrder
	}
	switch {
	case c.Name == "":
		writeErr(w, http.StatusBadRequest, "give the copy a name")
		return
	case c.Mode == "restic" && c.TargetRepositoryID == nil:
		writeErr(w, http.StatusBadRequest, "which repository should get the snapshots?")
		return
	case c.Mode == "mirror" && c.ConnectionID == nil && !filepath.IsAbs(c.Path):
		writeErr(w, http.StatusBadRequest, "choose a connection, or an absolute folder on this box")
		return
	}
	if err := s.store.SaveCopy(ctx, c); err != nil {
		s.fail(w, err, "save copy")
		return
	}
	s.touch()
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteCopy(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	c, err := s.store.GetCopy(r.Context(), idParam(r, "cid"))
	if err != nil || c.JobID != idParam(r, "id") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	_ = s.store.DeleteCopy(r.Context(), c.ID)
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

// ---------- runs ----------

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	q := r.URL.Query()
	list, err := s.store.ListRuns(r.Context(), store.RunFilter{JobID: queryInt64(r, "job"), RepositoryID: queryInt64(r, "repository"),
		Kind: q.Get("kind"), Status: q.Get("status"), Before: queryInt64(r, "before"), Limit: queryInt(r, "limit")})
	if err != nil {
		s.fail(w, err, "runs")
		return
	}
	s.activeMu.Lock()
	for i := range list {
		if a, ok := s.active[list[i].ID]; ok {
			a.mu.Lock()
			list[i].Summary = a.step
			a.mu.Unlock()
		}
	}
	s.activeMu.Unlock()
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	run, err := s.store.GetRun(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "run")
		return
	}
	out := map[string]any{"run": run}
	s.activeMu.Lock()
	if a, ok := s.active[run.ID]; ok {
		run.Log = a.log.String()
		a.mu.Lock()
		out["step"], out["percent"] = a.step, a.pct
		a.mu.Unlock()
	}
	s.activeMu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.activeMu.Lock()
	a, ok := s.active[idParam(r, "id")]
	s.activeMu.Unlock()
	if !ok {
		writeErr(w, http.StatusConflict, "this run is not running")
		return
	}
	a.cancel()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- the box's folders (picker) ----------

func (s *Server) handleBrowseLocal(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	p := filepath.Clean("/" + r.URL.Query().Get("path"))
	if !underAny(p, []string{s.cfg.BrowseRoot}) {
		p = s.cfg.BrowseRoot
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	type ent struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size"`
		Mount bool   `json:"mount,omitempty"`
	}
	out := []ent{}
	for _, e := range entries {
		full := filepath.Join(p, e.Name())
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(full); err == nil {
				isDir = fi.IsDir()
			}
		}
		if queryBool(r, "dirs") && !isDir {
			continue
		}
		x := ent{Name: e.Name(), Path: full, IsDir: isDir}
		if fi, err := e.Info(); err == nil && !isDir {
			x.Size = fi.Size()
		}
		if isDir && engine.Mounted(full) {
			x.Mount = true
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "root": s.cfg.BrowseRoot, "entries": out})
}

// findSQLite looks for SQLite databases under dir (depth levels deep).
func findSQLite(dir string, depth int) []string {
	var out []string
	var walk func(string, int)
	walk = func(d string, left int) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			full := filepath.Join(d, e.Name())
			if e.IsDir() {
				n := e.Name()
				if left > 0 && !strings.HasPrefix(n, ".") && n != "node_modules" && n != "cache" && n != "backups" && n != "logs" {
					walk(full, left-1)
				}
				continue
			}
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if ext != ".db" && ext != ".sqlite" && ext != ".sqlite3" {
				continue
			}
			f, err := os.Open(full)
			if err != nil {
				continue
			}
			head := make([]byte, 16)
			n, _ := f.Read(head)
			f.Close()
			if n == 16 && string(head[:15]) == "SQLite format 3" {
				out = append(out, full)
			}
			if len(out) > 20 {
				return
			}
		}
	}
	walk(dir, depth)
	return out
}

// ---------- overview ----------

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	jobs, _ := s.store.ListJobs(ctx)
	views := []jobView{}
	for _, j := range jobs {
		views = append(views, s.jobView(ctx, j))
	}
	health, issues := s.health(ctx, views)
	out := map[string]any{"health": health, "issues": issues}
	if u.Role != "admin" {
		// members see only whether the box is protected
		var last string
		for _, v := range views {
			if v.LastGood != nil && v.LastGood.FinishedAt > last {
				last = v.LastGood.FinishedAt
			}
		}
		out["last_backup"] = last
		out["issues"] = len(issues)
		writeJSON(w, http.StatusOK, out)
		return
	}
	repos, _ := s.store.ListRepositories(ctx)
	rv := []repoView{}
	for _, x := range repos {
		rv = append(rv, s.repoView(ctx, x, jobs))
	}
	runs, _ := s.store.ListRuns(ctx, store.RunFilter{Limit: 12})
	conns, _ := s.store.ListConnections(ctx)
	out["jobs"], out["repositories"], out["recent_runs"], out["connections"] = views, rv, runs, conns
	s.activeMu.Lock()
	active := []map[string]any{}
	for _, a := range s.active {
		a.mu.Lock()
		active = append(active, map[string]any{"run": a.run, "step": a.step, "percent": a.pct})
		a.mu.Unlock()
	}
	s.activeMu.Unlock()
	out["active"] = active
	writeJSON(w, http.StatusOK, out)
}

type issue struct {
	Level string `json:"level"` // warning | error
	Text  string `json:"text"`
	Link  string `json:"link,omitempty"`
}

// health sums the box up: ok, warning or error, with what is wrong.
func (s *Server) health(ctx context.Context, jobs []jobView) (string, []issue) {
	issues := []issue{}
	for _, v := range jobs {
		if !v.Enabled {
			continue
		}
		link := fmt.Sprintf("/jobs/%d", v.ID)
		if v.Last != nil && v.Last.Status == "failed" {
			issues = append(issues, issue{"error", v.Name + " failed: " + firstNonEmpty(v.Last.Error, "see the run"), link})
		} else if v.Last != nil && v.Last.Status == "warning" {
			issues = append(issues, issue{"warning", v.Name + " finished with warnings", link})
		}
		if v.Schedule != "" {
			c, err := engine.ParseCron(v.Schedule)
			if err == nil {
				// two scheduled times without a good run = overdue
				prev := c.Next(time.Now().Add(-49 * time.Hour))
				second := c.Next(prev)
				if second.Before(time.Now()) && (v.LastGood == nil || store.ParseTime(v.LastGood.FinishedAt).Before(prev)) {
					issues = append(issues, issue{"error", v.Name + " has not run successfully since " + localDay(prev), link})
				}
			}
		}
		if v.Config.Pull == nil && v.Schedule == "" && v.LastGood != nil && v.RepositoryID != nil {
			if age := time.Since(store.ParseTime(v.LastGood.FinishedAt)); age > 35*24*time.Hour {
				issues = append(issues, issue{"warning", fmt.Sprintf("%s last ran %d days ago", v.Name, int(age.Hours()/24)), link})
			}
		}
	}
	repos, _ := s.store.ListRepositories(ctx)
	for _, x := range repos {
		if x.CopiesPaused {
			issues = append(issues, issue{"error", x.Name + ": copies are paused — " + x.PausedReason, fmt.Sprintf("/places/repositories/%d", x.ID)})
		}
		if x.Status == "error" && (x.Location != "usb" || x.Options.USB == nil || engine.Mounted(x.Options.USB.Mountpoint)) {
			issues = append(issues, issue{"warning", x.Name + " could not be read: " + x.LastError, fmt.Sprintf("/places/repositories/%d", x.ID)})
		}
	}
	conns, _ := s.store.ListConnections(ctx)
	for _, c := range conns {
		if c.Status == "error" {
			issues = append(issues, issue{"warning", c.Name + " does not answer: " + c.LastError, "/places"})
		}
	}
	if rep := s.lastCoverage(); rep != nil && rep.Uncovered > 0 {
		issues = append(issues, issue{"warning", fmt.Sprintf("%d app(s) are not covered by any backup", rep.Uncovered), "/coverage"})
	}
	health := "ok"
	for _, i := range issues {
		if i.Level == "error" {
			return "error", issues
		}
		health = "warning"
	}
	if len(jobs) == 0 {
		health = "empty"
	}
	return health, issues
}

func localDay(t time.Time) string { return t.Local().Format("Mon 2 Jan 15:04") }

// handleWidget is the Homepage customapi card (any signed-in token).
func (s *Server) handleWidget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	jobs, _ := s.store.ListJobs(ctx)
	views := []jobView{}
	var last string
	failing := 0
	for _, j := range jobs {
		v := s.jobView(ctx, j)
		views = append(views, v)
		if v.LastGood != nil && v.LastGood.FinishedAt > last && v.Kind == "backup" {
			last = v.LastGood.FinishedAt
		}
		if v.Enabled && v.Last != nil && v.Last.Status == "failed" {
			failing++
		}
	}
	health, issues := s.health(ctx, views)
	repos, _ := s.store.ListRepositories(ctx)
	var size int64
	for _, x := range repos {
		size += x.Stats.Size
	}
	ago := "never"
	if t := store.ParseTime(last); !t.IsZero() {
		ago = humanAgo(time.Since(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"health": health, "issues": len(issues), "failing": failing, "last_backup": last,
		"last_backup_ago": ago, "jobs": len(jobs), "repositories": len(repos), "stored": engine.HumanBytes(size)})
}

func humanAgo(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

// ---------- integrations (box settings) ----------

var integrationKeys = []string{"report_html_path", "report_widgets_yaml", "report_repo_id"}

func (s *Server) handleGetIntegrations(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	out := map[string]any{}
	for _, k := range integrationKeys {
		out[k] = s.store.BoxSetting(ctx, k)
	}
	out["report_last"] = s.store.BoxSetting(ctx, "report_last")
	out["backrest"] = map[string]any{"configured": s.br.Configured(), "url": s.cfg.BackrestURL}
	out["host_access"] = s.cfg.HostAccess
	out["staging_dir"] = s.cfg.StagingDir
	out["tools"] = s.tools()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutIntegrations(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var in map[string]string
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	for _, k := range integrationKeys {
		if v, ok := in[k]; ok {
			v = strings.TrimSpace(v)
			if v != "" && k != "report_repo_id" && !filepath.IsAbs(v) {
				writeErr(w, http.StatusBadRequest, k+" must be an absolute path")
				return
			}
			_ = s.store.SetBoxSetting(r.Context(), k, v)
		}
	}
	s.handleGetIntegrations(w, r)
}

func (s *Server) tools() map[string]string {
	s.toolsOnce.Do(func() { s.toolVersions = s.eng.Available() })
	return s.toolVersions
}
