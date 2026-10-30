// Package server wires the HTTP API, the job runner and scheduler, the
// Backrest hooks, auth, Pocket Core and the embedded SPA.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/crypto/bcrypt"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/config"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Version is injected at build time.
var Version = "dev"

// Server holds dependencies.
type Server struct {
	cfg   *config.Config
	store *store.Store
	log   *slog.Logger
	webFS fs.FS
	http  *http.Client
	eng   *engine.Engine
	br    *engine.Backrest

	started  time.Time
	requests atomic.Int64
	errors   atomic.Int64
	changed  atomic.Int64 // bumps on every write; GET /changes

	core    *core.Client
	coreMu  sync.Mutex
	coreOK  bool
	coreErr string

	oidc   *oidcProvider
	oidcMu sync.Mutex

	linkApps sync.Map // pcx_ token → acting app slug

	activeMu sync.Mutex
	active   map[int64]*activeRun

	locksMu   sync.Mutex
	repoLocks map[int64]*sync.Mutex
	jobBusy   map[int64]bool

	rcloneMu sync.Mutex

	diskMu   sync.Mutex
	diskSeen map[int64]bool
	fired    map[string]time.Time

	covMu    sync.Mutex
	lastCov  *coverageReport
	covTimer *time.Timer

	brMu  sync.Mutex
	brErr string
	brOps *brOpsCache

	snapCache   sync.Map // repository id → cachedSnaps
	reportCache sync.Map // repository id → cachedReport

	toolsOnce    sync.Once
	toolVersions map[string]string
}

// New creates a server.
func New(cfg *config.Config, st *store.Store, log *slog.Logger, webFS fs.FS) *Server {
	ua := "PocketBackups/" + Version + " (+https://github.com/abjelosevic88/pocket-homelab-backups)"
	s := &Server{cfg: cfg, store: st, log: log, webFS: webFS, started: time.Now(),
		active: map[int64]*activeRun{}, repoLocks: map[int64]*sync.Mutex{}, jobBusy: map[int64]bool{}, diskSeen: map[int64]bool{}, fired: map[string]time.Time{}}
	s.http = &http.Client{Timeout: 60 * time.Second}
	s.core = core.New(cfg.CoreURL, core.Options{DB: st.DB, TokenFile: cfg.DataDir + "/core.token", UserAgent: ua, ServiceCache: time.Minute, ServiceStale: 15 * time.Minute})
	s.oidc = &oidcProvider{issuerURL: cfg.OIDCIssuer}
	s.eng = NewEngine(cfg)
	s.br = &engine.Backrest{URL: cfg.BackrestURL, User: cfg.BackrestUser, Password: cfg.BackrestPassword}
	return s
}

// NewEngine builds the engine from the configuration (also used by the CLI).
func NewEngine(cfg *config.Config) *engine.Engine {
	return &engine.Engine{ResticBin: cfg.ResticBin, RcloneBin: cfg.RcloneBin, DockerBin: cfg.DockerBin, DockerHost: cfg.DockerHost,
		TmpDir: cfg.TmpDir, CacheDir: cfg.CacheDir, RcloneConfig: cfg.DataDir + "/rclone/rclone.conf", ResticHost: cfg.ResticHost,
		HelperImage: cfg.HelperImage, HostAccess: cfg.HostAccess, DevDiskDir: cfg.DevDiskDir}
}

// isOwner reports whether uid is the account the box was set up with.
func (s *Server) isOwner(ctx context.Context, uid int64) bool {
	return uid > 0 && uid == s.store.OwnerID(ctx)
}

// Bootstrap seeds the first admin (if configured) and starts the workers.
func (s *Server) Bootstrap(ctx context.Context) error {
	if err := s.core.Start(ctx); err != nil {
		return err
	}
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n == 0 && s.cfg.AuthMode == "local" && s.cfg.AdminEmail != "" && s.cfg.AdminPassword != "" {
		if len(s.cfg.AdminPassword) < 8 {
			return errors.New("ADMIN_PASSWORD must be at least 8 characters")
		}
		h, _ := bcrypt.GenerateFromPassword([]byte(s.cfg.AdminPassword), bcrypt.DefaultCost)
		u := &store.User{Email: s.cfg.AdminEmail, Name: s.cfg.AdminName, PasswordHash: string(h), Role: "admin"}
		if err := s.store.CreateUser(ctx, u); err != nil {
			return err
		}
		st, _ := s.store.GetSettings(ctx, 0)
		st.SetupComplete = true
		if err := s.store.SaveBoxSettings(ctx, st); err != nil {
			return err
		}
		s.log.Info("admin user created from environment", "email", u.Email)
	}
	_ = s.store.PurgeSessions(ctx)
	s.changed.Store(time.Now().UnixNano())
	if err := s.loadSecretKey(); err != nil {
		return err
	}
	for _, d := range []string{s.cfg.StagingDir, s.cfg.TmpDir, s.cfg.CacheDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
	}
	if n := s.store.AbandonRunning(ctx); n > 0 {
		s.log.Warn("runs cut short by a restart", "count", n)
	}
	if err := s.syncRclone(ctx); err != nil {
		s.log.Warn("rclone.conf", "err", err)
	}
	if s.cfg.DemoData {
		s.seedDemo(ctx)
	}
	go s.scheduler(ctx)
	go s.watchBackrest(ctx)
	go s.refreshAllStats(ctx)
	if s.core.Configured() {
		go s.coreLoop(ctx)
	}
	return nil
}

// refreshAllStats reads every repository on the box once after a start (a
// disk that is not plugged in waits for its next use).
func (s *Server) refreshAllStats(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(20 * time.Second):
	}
	repos, _ := s.store.ListRepositories(ctx)
	for i := range repos {
		x := &repos[i]
		if u := usbOf(x); u != nil && !engine.Mounted(u.Mountpoint) {
			continue
		}
		if _, err := s.refreshRepoStats(ctx, x); err != nil {
			s.log.Warn("repository could not be read", "repository", x.Name, "err", err)
		}
	}
	s.touch()
}

// touch records that something changed (the UI polls /changes).
func (s *Server) touch() { s.changed.Store(time.Now().UnixNano()) }

// notifyAdmins sends a message to every admin who wants that kind
// ("failure" = failures and warnings, "success" = good news).
func (s *Server) notifyAdmins(ctx context.Context, what, title, body, level string) {
	users, _ := s.store.ListUsers(ctx)
	sent := map[string]bool{}
	for _, u := range users {
		if u.Role != "admin" {
			continue
		}
		st, err := s.store.GetSettings(ctx, u.ID)
		if err != nil {
			continue
		}
		if (what == "failure" && !st.NotifyFailures) || (what == "success" && !st.NotifySuccesses) {
			continue
		}
		kind, url, token := s.notifyRoute(ctx, u.ID, st)
		if url == "" || sent[url] {
			continue
		}
		sent[url] = true
		go func(kind, url, token string) {
			c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := notify.Send(c, kind, url, token, title, body, s.cfg.BaseURL, level); err != nil {
				s.log.Warn("notification failed", "title", title, "err", err)
			}
		}(kind, url, token)
	}
	s.log.Info("notify", "title", title, "level", level)
}

// publishRun tells the Core bus a run ended (Pocket Day, Hub tiles).
func (s *Server) publishRun(r *store.Run) {
	if !s.core.Configured() || (r.Kind != "backup" && r.Kind != "post" && r.Kind != "pull") {
		return
	}
	topic := "backups.run.finished"
	if r.Status == "failed" {
		topic = "backups.run.failed"
	}
	payload := map[string]any{"run": r.ID, "status": r.Status, "title": r.Title, "snapshot": r.SnapshotID, "bytes_added": r.BytesAdded, "summary": r.Summary}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.core.Publish(c, topic, strconv.FormatInt(r.ID, 10), payload); err != nil {
			s.log.Debug("pocket core: publish", "err", err)
		}
	}()
}

// Router builds the chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	if s.cfg.TrustProxy {
		r.Use(middleware.RealIP)
	}
	r.Use(middleware.RequestID)
	r.Use(s.logging)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5, "application/json", "text/html", "text/css", "application/javascript", "text/plain"))

	r.Get("/pocket-ask.js", s.core.AskWidget("backups"))
	r.Get("/healthz", s.handleHealth)
	r.Get("/readyz", s.handleReady)
	r.Get("/api/health", s.handleHealth)
	if s.cfg.MetricsEnabled {
		r.Get("/metrics", s.handleMetrics)
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(middleware.Timeout(180 * time.Second))
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/setup", s.handleSetup)
		r.Get("/auth/status", s.handleAuthStatus)
		r.Get("/auth/oidc/start", s.handleOIDCStart)
		r.Get("/auth/oidc/callback", s.handleOIDCCallback)
		r.Post("/client-error", s.handleClientError)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)
			r.Put("/auth/me", s.handleUpdateMe)
			r.Get("/changes", s.handleChanges)

			r.Get("/overview", s.handleOverview)
			r.Get("/widget", s.handleWidget)

			r.Get("/connections", s.handleListConnections)
			r.Get("/connections/kinds", s.handleConnKinds)
			r.Get("/connections/importable", s.handleImportable)
			r.Post("/connections/import", s.handleImportConnections)
			r.Get("/connections/oauth/{kind}/start", s.handleOAuthStart)
			r.Get("/connections/oauth/callback", s.handleOAuthCallback)
			r.Post("/connections", s.handleSaveConnection)
			r.Put("/connections/{id}", s.handleSaveConnection)
			r.Delete("/connections/{id}", s.handleDeleteConnection)
			r.Post("/connections/{id}/test", s.handleTestConnection)
			r.Get("/connections/{id}/browse", s.handleBrowseConnection)

			r.Get("/repositories", s.handleListRepos)
			r.Post("/repositories", s.handleSaveRepo)
			r.Get("/repositories/{id}", s.handleGetRepo)
			r.Put("/repositories/{id}", s.handleSaveRepo)
			r.Delete("/repositories/{id}", s.handleDeleteRepo)
			r.Post("/repositories/{id}/action", s.handleRepoAction)
			r.Post("/repositories/{id}/password", s.handleRevealPassword)
			r.Get("/repositories/{id}/snapshots", s.handleSnapshots)
			r.Get("/repositories/{id}/ls", s.handleLs)
			r.Get("/repositories/{id}/report", s.handleReport)
			r.Post("/repositories/{id}/restore", s.handleRestore)

			r.Get("/sources", s.handleListSources)
			r.Post("/sources", s.handleSaveSource)
			r.Put("/sources/{id}", s.handleSaveSource)
			r.Delete("/sources/{id}", s.handleDeleteSource)
			r.Post("/sources/{id}/run", s.handleRunSource)
			r.Get("/discover", s.handleDiscover)

			r.Get("/jobs", s.handleListJobs)
			r.Post("/jobs", s.handleSaveJob)
			r.Get("/jobs/{id}", s.handleGetJob)
			r.Put("/jobs/{id}", s.handleSaveJob)
			r.Delete("/jobs/{id}", s.handleDeleteJob)
			r.Post("/jobs/{id}/run", s.handleRunJob)
			r.Post("/jobs/{id}/copies", s.handleSaveCopy)
			r.Put("/jobs/{id}/copies/{cid}", s.handleSaveCopy)
			r.Delete("/jobs/{id}/copies/{cid}", s.handleDeleteCopy)

			r.Get("/runs", s.handleListRuns)
			r.Get("/runs/{id}", s.handleGetRun)
			r.Post("/runs/{id}/cancel", s.handleCancelRun)

			r.Get("/coverage", s.handleCoverage)
			r.Get("/coverage/summary", s.handleCoverageSummary)
			r.Post("/coverage/skip", s.handleCoverageSkip)

			r.Get("/backrest", s.handleBackrest)
			r.Post("/backrest/import", s.handleBackrestImport)
			r.Post("/backrest/takeover", s.handleBackrestTakeover)
			r.Get("/schedule", s.handleSchedule)
			r.Get("/backrest/hooks", s.handleBackrestHooks)
			r.Post("/backrest/hooks", s.handleBackrestHooks)

			r.Get("/browse", s.handleBrowseLocal)
			r.Get("/integrations", s.handleGetIntegrations)
			r.Put("/integrations", s.handlePutIntegrations)

			r.Get("/users", s.handleListUsers)
			r.Post("/users", s.handleCreateUser)
			r.Put("/users/{id}", s.handleUpdateUser)
			r.Delete("/users/{id}", s.handleDeleteUser)

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handlePutSettings)
			r.Get("/settings/system", s.handleSystemInfo)
			r.Post("/notify/test", s.handleTestNotify)

			r.Get("/tokens", s.handleListTokens)
			r.Post("/tokens", s.handleCreateToken)
			r.Delete("/tokens/{id}", s.handleDeleteToken)

			r.Get("/backup", s.handleBackup)
		})
	})

	// Backrest's hooks: long-running (a prep or the copies take minutes), so
	// outside the 180 s timeout.
	r.With(s.requireAuth).Post("/api/v1/hooks/backrest", s.handleBackrestHook)
	r.With(s.requireAuth).Get("/api/v1/repositories/{id}/download", s.handleDownload)

	r.NotFound(s.handleSPA)
	return r
}

// ---------- helpers ----------

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.requests.Add(1)
		if ww.Status() >= 500 {
			s.errors.Add(1)
		}
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			return
		}
		s.log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "dur", time.Since(start).String(), "ip", r.RemoteAddr)
	})
}

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func badRequest(msg string) error { return userError{msg} }

func (s *Server) fail(w http.ResponseWriter, err error, context string) {
	var ue userError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
		return
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "already there")
		return
	case errors.As(err, &ue):
		writeErr(w, http.StatusBadRequest, ue.msg)
		return
	}
	s.log.Error(context, "err", err)
	writeErr(w, http.StatusInternalServerError, context+": "+err.Error())
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<20))
	return dec.Decode(v)
}

func idParam(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return n
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func queryInt64(r *http.Request, name string) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	return n
}

func queryBool(r *http.Request, name string) bool {
	v := strings.ToLower(r.URL.Query().Get(name))
	return v == "1" || v == "true" || v == "yes"
}

func me(r *http.Request) int64 { return actorID(r.Context()) }

// ---------- Pocket Core ----------

// coreLoop registers with Core on start (retrying until it answers) and
// re-registers every 30 minutes so version and last-seen stay fresh.
func (s *Server) coreLoop(ctx context.Context) {
	wait := 5 * time.Second
	for {
		err := s.registerWithCore(ctx)
		s.coreMu.Lock()
		s.coreOK, s.coreErr = err == nil, ""
		if err != nil {
			s.coreErr = err.Error()
		}
		s.coreMu.Unlock()
		if err != nil {
			s.log.Warn("pocket core: registration failed, retrying", "err", err, "in", wait.String())
		} else {
			wait = 30 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err != nil && wait < 5*time.Minute {
			wait *= 2
		}
	}
}

func (s *Server) registerWithCore(ctx context.Context) error {
	m := core.Manifest{
		Slug: "backups", Name: "Pocket Backups",
		Description: "Backs up the whole box and says plainly whether it is safe: databases dumped consistently, folders snapshotted with restic, copies to a NAS, Google Drive, Backblaze, S3 or Pocket Files, an offline USB disk, restore of any file from any night, a coverage check for new apps, and Backrest underneath if you have it.",
		Icon:        "/favicon.svg", Version: Version,
		URL: s.cfg.CoreAppURL, PublicURL: s.cfg.BaseURL, HealthPath: "/healthz", EnvPrefix: "BACKUPS",
		Publishes: []string{"backups.run.finished", "backups.run.failed"},
		Offers:    linkOffers,
		Uses:      linkUses,
	}
	if s.cfg.BaseURL != "" {
		m.OIDC = &core.OIDCRequest{RedirectURIs: []string{s.cfg.BaseURL + "/api/v1/auth/oidc/callback"}}
	}
	reg, err := s.core.Register(ctx, s.cfg.CoreRegistrationKey, m)
	if err != nil {
		return err
	}
	if reg.Token != "" {
		s.log.Info("pocket core: registered, app token stored", "core", s.cfg.CoreURL)
	} else {
		s.log.Debug("pocket core: re-registered", "core", s.cfg.CoreURL)
	}
	return nil
}

// coreConnected is "configured AND registered": only then are Core-only choices live.
func (s *Server) coreConnected() bool {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return s.core.Configured() && s.coreOK
}

// coreStatus summarises the Pocket Core link for the About page.
func (s *Server) coreStatus() map[string]any {
	s.coreMu.Lock()
	defer s.coreMu.Unlock()
	return map[string]any{"configured": s.core.Configured(), "url": s.cfg.CoreURL, "registered": s.coreOK, "error": s.coreErr}
}

// handleClientError writes a crash of the web app into the server's log.
func (s *Server) handleClientError(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message, Stack, Component, URL, Where, Agent string
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	who := ""
	if u, err := s.resolveUser(r); err == nil && u != nil {
		who = u.Email
	}
	s.log.Warn("web app crashed", "user", who, "where", in.Where, "url", in.URL, "message", in.Message, "stack", in.Stack, "component", in.Component, "agent", in.Agent)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"stamp": s.changed.Load()})
}

// ---------- health / metrics ----------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"core_outbox": s.coreOutboxHealth(r.Context()), "status": "ok", "version": Version, "uptime": time.Since(s.started).Round(time.Second).String()})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DB.PingContext(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts := s.store.Counts(ctx)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP pocket_backups_info Build info.\n# TYPE pocket_backups_info gauge\npocket_backups_info{version=%q} 1\n", Version)
	fmt.Fprintf(w, "# HELP pocket_backups_uptime_seconds Process uptime.\n# TYPE pocket_backups_uptime_seconds gauge\npocket_backups_uptime_seconds %d\n", int(time.Since(s.started).Seconds()))
	fmt.Fprintf(w, "# HELP pocket_backups_http_requests_total HTTP requests served.\n# TYPE pocket_backups_http_requests_total counter\npocket_backups_http_requests_total %d\n", s.requests.Load())
	fmt.Fprintf(w, "# HELP pocket_backups_http_errors_total HTTP 5xx responses.\n# TYPE pocket_backups_http_errors_total counter\npocket_backups_http_errors_total %d\n", s.errors.Load())
	fmt.Fprintf(w, "# HELP pocket_backups_rows Rows per table.\n# TYPE pocket_backups_rows gauge\n")
	for k, v := range counts {
		fmt.Fprintf(w, "pocket_backups_rows{table=%q} %d\n", k, v)
	}
}

// ---------- static / SPA ----------

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if s.webFS == nil {
		http.Error(w, "web UI not built (run `make web`)", http.StatusNotFound)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.webFS.Open(path); err == nil {
		f.Close()
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.FileServerFS(s.webFS).ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	r.URL.Path = "/"
	http.FileServerFS(s.webFS).ServeHTTP(w, r)
}
