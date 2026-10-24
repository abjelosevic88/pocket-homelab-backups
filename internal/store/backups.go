package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func fromJSON(s string, v any) {
	if s == "" {
		return
	}
	_ = json.Unmarshal([]byte(s), v)
}

func nullID(p *int64) any {
	if p == nil || *p <= 0 {
		return nil
	}
	return *p
}

func idPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// ---------- connections ----------

const connCols = `id, name, kind, config, secret, acting_user, status, last_error, checked_at, created_at, updated_at`

func scanConn(row interface{ Scan(...any) error }) (*Connection, error) {
	var c Connection
	var cfg string
	var acting sql.NullInt64
	if err := row.Scan(&c.ID, &c.Name, &c.Kind, &cfg, &c.Secret, &acting, &c.Status, &c.LastError, &c.CheckedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	fromJSON(cfg, &c.Config)
	c.ActingUser = idPtr(acting)
	c.HasSecret = c.Secret != ""
	return &c, nil
}

// ListConnections returns every connection.
func (s *Store) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+connCols+` FROM connections ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetConnection returns one connection.
func (s *Store) GetConnection(ctx context.Context, id int64) (*Connection, error) {
	return scanConn(s.DB.QueryRowContext(ctx, `SELECT `+connCols+` FROM connections WHERE id = ?`, id))
}

// SaveConnection inserts or updates a connection.
func (s *Store) SaveConnection(ctx context.Context, c *Connection) error {
	if c.ID == 0 {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO connections (name, kind, config, secret, acting_user) VALUES (?, ?, ?, ?, ?)`,
			c.Name, c.Kind, toJSON(c.Config), c.Secret, nullID(c.ActingUser))
		if err != nil {
			return conflict(err)
		}
		c.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE connections SET name = ?, kind = ?, config = ?, secret = ?, acting_user = ?, updated_at = ? WHERE id = ?`,
		c.Name, c.Kind, toJSON(c.Config), c.Secret, nullID(c.ActingUser), Now(), c.ID)
	return conflict(err)
}

// SetConnectionSecret replaces only the sealed secret (a refreshed OAuth token).
func (s *Store) SetConnectionSecret(ctx context.Context, id int64, sealed string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE connections SET secret = ?, updated_at = ? WHERE id = ?`, sealed, Now(), id)
	return err
}

// SetConnectionStatus records the result of a test.
func (s *Store) SetConnectionStatus(ctx context.Context, id int64, status, lastErr string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE connections SET status = ?, last_error = ?, checked_at = ? WHERE id = ?`, status, lastErr, Now(), id)
}

// DeleteConnection removes a connection that nothing uses any more.
func (s *Store) DeleteConnection(ctx context.Context, id int64) error {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(1) FROM repositories WHERE connection_id = ?) + (SELECT COUNT(1) FROM copies WHERE connection_id = ?)
		+ (SELECT COUNT(1) FROM jobs WHERE json_extract(config, '$.pull.connection_id') = ?)`, id, id, id).Scan(&n)
	if n > 0 {
		return ErrInUse
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM connections WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- repositories ----------

const repoCols = `id, name, location, path, connection_id, password, password_file, options, managed_by, backrest_repo,
	check_schedule, check_read_pct, prune_schedule, copies_paused, paused_reason, stats, status, last_error, created_at, updated_at`

func scanRepo(row interface{ Scan(...any) error }) (*Repository, error) {
	var r Repository
	var conn sql.NullInt64
	var opts, stats string
	var paused int
	if err := row.Scan(&r.ID, &r.Name, &r.Location, &r.Path, &conn, &r.Password, &r.PasswordFile, &opts, &r.ManagedBy, &r.BackrestRepo,
		&r.CheckSchedule, &r.CheckReadPct, &r.PruneSchedule, &paused, &r.PausedReason, &stats, &r.Status, &r.LastError, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	r.ConnectionID = idPtr(conn)
	fromJSON(opts, &r.Options)
	fromJSON(stats, &r.Stats)
	r.CopiesPaused = paused == 1
	r.HasPassword = r.Password != "" || r.PasswordFile != ""
	return &r, nil
}

// ListRepositories returns every repository.
func (s *Store) ListRepositories(ctx context.Context) ([]Repository, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+repoCols+` FROM repositories ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Repository{}
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetRepository returns one repository.
func (s *Store) GetRepository(ctx context.Context, id int64) (*Repository, error) {
	return scanRepo(s.DB.QueryRowContext(ctx, `SELECT `+repoCols+` FROM repositories WHERE id = ?`, id))
}

// GetRepositoryByBackrest finds the repository a Backrest repo id stands for.
func (s *Store) GetRepositoryByBackrest(ctx context.Context, brID string) (*Repository, error) {
	return scanRepo(s.DB.QueryRowContext(ctx, `SELECT `+repoCols+` FROM repositories WHERE backrest_repo = ? AND backrest_repo <> '' ORDER BY id LIMIT 1`, brID))
}

// SaveRepository inserts or updates a repository (not stats/status/pause).
func (s *Store) SaveRepository(ctx context.Context, r *Repository) error {
	if r.ManagedBy == "" {
		r.ManagedBy = "pocket"
	}
	if r.ID == 0 {
		res, err := s.DB.ExecContext(ctx, `INSERT INTO repositories (name, location, path, connection_id, password, password_file, options, managed_by, backrest_repo,
			check_schedule, check_read_pct, prune_schedule) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Name, r.Location, r.Path, nullID(r.ConnectionID), r.Password, r.PasswordFile, toJSON(r.Options), r.ManagedBy, r.BackrestRepo,
			r.CheckSchedule, r.CheckReadPct, r.PruneSchedule)
		if err != nil {
			return conflict(err)
		}
		r.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE repositories SET name = ?, location = ?, path = ?, connection_id = ?, password = ?, password_file = ?, options = ?,
		managed_by = ?, backrest_repo = ?, check_schedule = ?, check_read_pct = ?, prune_schedule = ?, updated_at = ? WHERE id = ?`,
		r.Name, r.Location, r.Path, nullID(r.ConnectionID), r.Password, r.PasswordFile, toJSON(r.Options), r.ManagedBy, r.BackrestRepo,
		r.CheckSchedule, r.CheckReadPct, r.PruneSchedule, Now(), r.ID)
	return conflict(err)
}

// SetRepoStats stores what the repository looked like just now.
func (s *Store) SetRepoStats(ctx context.Context, id int64, st RepoStats) {
	st.UpdatedAt = Now()
	_, _ = s.DB.ExecContext(ctx, `UPDATE repositories SET stats = ? WHERE id = ?`, toJSON(st), id)
}

// SetRepoStatus records whether the repository answered.
func (s *Store) SetRepoStatus(ctx context.Context, id int64, status, lastErr string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE repositories SET status = ?, last_error = ? WHERE id = ?`, status, lastErr, id)
}

// SetCopiesPaused pauses (or resumes) the copies of a repository: a failed
// check must not overwrite the last good copies elsewhere.
func (s *Store) SetCopiesPaused(ctx context.Context, id int64, paused bool, reason string) error {
	if !paused {
		reason = ""
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE repositories SET copies_paused = ?, paused_reason = ? WHERE id = ?`, boolInt(paused), reason, id)
	return err
}

// DeleteRepository forgets a repository (its data is not touched).
func (s *Store) DeleteRepository(ctx context.Context, id int64) error {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM jobs WHERE repository_id = ?`, id).Scan(&n)
	if n > 0 {
		return ErrInUse
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM repositories WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- sources ----------

const sourceCols = `id, name, kind, enabled, config, secret, project, notes, sort_order, last_ok_at, last_error, last_size, last_duration_ms, created_at, updated_at`

func scanSource(row interface{ Scan(...any) error }) (*Source, error) {
	var x Source
	var cfg string
	var enabled int
	if err := row.Scan(&x.ID, &x.Name, &x.Kind, &enabled, &cfg, &x.Secret, &x.Project, &x.Notes, &x.SortOrder, &x.LastOKAt, &x.LastError,
		&x.LastSize, &x.LastDurationMS, &x.CreatedAt, &x.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	fromJSON(cfg, &x.Config)
	x.Enabled = enabled == 1
	x.HasSecret = x.Secret != ""
	x.Jobs = []int64{}
	return &x, nil
}

// ListSources returns every source with the jobs that use it.
func (s *Store) ListSources(ctx context.Context) ([]Source, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sourceCols+` FROM sources ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	out := []Source{}
	idx := map[int64]int{}
	for rows.Next() {
		x, err := scanSource(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		idx[x.ID] = len(out)
		out = append(out, *x)
	}
	rows.Close()
	links, err := s.DB.QueryContext(ctx, `SELECT job_id, source_id FROM job_sources ORDER BY job_id`)
	if err != nil {
		return nil, err
	}
	defer links.Close()
	for links.Next() {
		var j, src int64
		if links.Scan(&j, &src) == nil {
			if i, ok := idx[src]; ok {
				out[i].Jobs = append(out[i].Jobs, j)
			}
		}
	}
	return out, links.Err()
}

// GetSource returns one source.
func (s *Store) GetSource(ctx context.Context, id int64) (*Source, error) {
	x, err := scanSource(s.DB.QueryRowContext(ctx, `SELECT `+sourceCols+` FROM sources WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT job_id FROM job_sources WHERE source_id = ?`, id)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var j int64
			if rows.Scan(&j) == nil {
				x.Jobs = append(x.Jobs, j)
			}
		}
	}
	return x, nil
}

// SaveSource inserts or updates a source.
func (s *Store) SaveSource(ctx context.Context, x *Source) error {
	if x.ID == 0 {
		if x.SortOrder == 0 {
			_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM sources`).Scan(&x.SortOrder)
		}
		res, err := s.DB.ExecContext(ctx, `INSERT INTO sources (name, kind, enabled, config, secret, project, notes, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			x.Name, x.Kind, boolInt(x.Enabled), toJSON(x.Config), x.Secret, x.Project, x.Notes, x.SortOrder)
		if err != nil {
			return conflict(err)
		}
		x.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE sources SET name = ?, kind = ?, enabled = ?, config = ?, secret = ?, project = ?, notes = ?, sort_order = ?, updated_at = ? WHERE id = ?`,
		x.Name, x.Kind, boolInt(x.Enabled), toJSON(x.Config), x.Secret, x.Project, x.Notes, x.SortOrder, Now(), x.ID)
	return conflict(err)
}

// SetSourceResult records how a source's last capture went.
func (s *Store) SetSourceResult(ctx context.Context, id int64, ok bool, errMsg string, size int64, d time.Duration) {
	if ok {
		_, _ = s.DB.ExecContext(ctx, `UPDATE sources SET last_ok_at = ?, last_error = '', last_size = ?, last_duration_ms = ? WHERE id = ?`, Now(), size, d.Milliseconds(), id)
		return
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE sources SET last_error = ?, last_duration_ms = ? WHERE id = ?`, errMsg, d.Milliseconds(), id)
}

// DeleteSource removes a source (and takes it out of every job).
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- jobs ----------

const jobCols = `id, name, kind, engine, backrest_plan, repository_id, schedule, enabled, config, sort_order, created_at, updated_at`

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var j Job
	var repo sql.NullInt64
	var cfg string
	var enabled int
	if err := row.Scan(&j.ID, &j.Name, &j.Kind, &j.Engine, &j.BackrestPlan, &repo, &j.Schedule, &enabled, &cfg, &j.SortOrder, &j.CreatedAt, &j.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	j.RepositoryID = idPtr(repo)
	fromJSON(cfg, &j.Config)
	j.Enabled = enabled == 1
	j.SourceIDs = []int64{}
	j.Copies = []Copy{}
	return &j, nil
}

func (s *Store) fillJob(ctx context.Context, j *Job) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT js.source_id FROM job_sources js JOIN sources x ON x.id = js.source_id WHERE js.job_id = ? ORDER BY x.sort_order, x.id`, j.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			j.SourceIDs = append(j.SourceIDs, id)
		}
	}
	rows.Close()
	cs, err := s.ListCopies(ctx, j.ID)
	if err != nil {
		return err
	}
	j.Copies = cs
	return nil
}

// ListJobs returns every job with its sources and copies.
func (s *Store) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+jobCols+` FROM jobs ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *j)
	}
	rows.Close()
	for i := range out {
		if err := s.fillJob(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetJob returns one job with its sources and copies.
func (s *Store) GetJob(ctx context.Context, id int64) (*Job, error) {
	j, err := scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	return j, s.fillJob(ctx, j)
}

// GetJobByPlan finds the job a Backrest plan belongs to.
func (s *Store) GetJobByPlan(ctx context.Context, plan string) (*Job, error) {
	j, err := scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+` FROM jobs WHERE engine = 'backrest' AND backrest_plan = ? ORDER BY id LIMIT 1`, plan))
	if err != nil {
		return nil, err
	}
	return j, s.fillJob(ctx, j)
}

// SaveJob inserts or updates a job and its list of sources.
func (s *Store) SaveJob(ctx context.Context, j *Job) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if j.ID == 0 {
		if j.SortOrder == 0 {
			_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM jobs`).Scan(&j.SortOrder)
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO jobs (name, kind, engine, backrest_plan, repository_id, schedule, enabled, config, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			j.Name, j.Kind, j.Engine, j.BackrestPlan, nullID(j.RepositoryID), j.Schedule, boolInt(j.Enabled), toJSON(j.Config), j.SortOrder)
		if err != nil {
			return conflict(err)
		}
		j.ID, _ = res.LastInsertId()
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET name = ?, kind = ?, engine = ?, backrest_plan = ?, repository_id = ?, schedule = ?, enabled = ?, config = ?, sort_order = ?, updated_at = ? WHERE id = ?`,
			j.Name, j.Kind, j.Engine, j.BackrestPlan, nullID(j.RepositoryID), j.Schedule, boolInt(j.Enabled), toJSON(j.Config), j.SortOrder, Now(), j.ID); err != nil {
			return conflict(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM job_sources WHERE job_id = ?`, j.ID); err != nil {
			return err
		}
	}
	for _, sid := range j.SourceIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO job_sources (job_id, source_id) SELECT ?, id FROM sources WHERE id = ?`, j.ID, sid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteJob removes a job (its runs stay, without the job).
func (s *Store) DeleteJob(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- copies ----------

const copyCols = `id, job_id, name, mode, connection_id, path, target_repository_id, enabled, options, sort_order, last_ok_at, last_error, last_duration_ms, created_at`

func scanCopy(row interface{ Scan(...any) error }) (*Copy, error) {
	var c Copy
	var conn, target sql.NullInt64
	var opts string
	var enabled int
	if err := row.Scan(&c.ID, &c.JobID, &c.Name, &c.Mode, &conn, &c.Path, &target, &enabled, &opts, &c.SortOrder, &c.LastOKAt, &c.LastError, &c.LastDurationMS, &c.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	c.ConnectionID = idPtr(conn)
	c.TargetRepositoryID = idPtr(target)
	c.Enabled = enabled == 1
	fromJSON(opts, &c.Options)
	return &c, nil
}

// ListCopies returns a job's copies in order.
func (s *Store) ListCopies(ctx context.Context, jobID int64) ([]Copy, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+copyCols+` FROM copies WHERE job_id = ? ORDER BY sort_order, id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Copy{}
	for rows.Next() {
		c, err := scanCopy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// GetCopy returns one copy.
func (s *Store) GetCopy(ctx context.Context, id int64) (*Copy, error) {
	return scanCopy(s.DB.QueryRowContext(ctx, `SELECT `+copyCols+` FROM copies WHERE id = ?`, id))
}

// SaveCopy inserts or updates a copy.
func (s *Store) SaveCopy(ctx context.Context, c *Copy) error {
	if c.ID == 0 {
		if c.SortOrder == 0 {
			_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM copies WHERE job_id = ?`, c.JobID).Scan(&c.SortOrder)
		}
		res, err := s.DB.ExecContext(ctx, `INSERT INTO copies (job_id, name, mode, connection_id, path, target_repository_id, enabled, options, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.JobID, c.Name, c.Mode, nullID(c.ConnectionID), c.Path, nullID(c.TargetRepositoryID), boolInt(c.Enabled), toJSON(c.Options), c.SortOrder)
		if err != nil {
			return conflict(err)
		}
		c.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE copies SET name = ?, mode = ?, connection_id = ?, path = ?, target_repository_id = ?, enabled = ?, options = ?, sort_order = ? WHERE id = ?`,
		c.Name, c.Mode, nullID(c.ConnectionID), c.Path, nullID(c.TargetRepositoryID), boolInt(c.Enabled), toJSON(c.Options), c.SortOrder, c.ID)
	return err
}

// SetCopyResult records how a copy went.
func (s *Store) SetCopyResult(ctx context.Context, id int64, ok bool, errMsg string, d time.Duration) {
	if ok {
		_, _ = s.DB.ExecContext(ctx, `UPDATE copies SET last_ok_at = ?, last_error = '', last_duration_ms = ? WHERE id = ?`, Now(), d.Milliseconds(), id)
		return
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE copies SET last_error = ?, last_duration_ms = ? WHERE id = ?`, errMsg, d.Milliseconds(), id)
}

// DeleteCopy removes a copy (the copied data is not touched).
func (s *Store) DeleteCopy(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM copies WHERE id = ?`, id)
	return err
}

// ---------- runs ----------

const runCols = `r.id, r.job_id, r.repository_id, r.kind, r.trigger, r.status, r.title, r.started_at, r.finished_at, r.duration_ms, r.snapshot_id,
	r.bytes_added, r.bytes_total, r.files_new, r.files_changed, r.summary, r.error, r.steps, r.user_id, COALESCE(j.name, ''), COALESCE(p.name, '')`

func scanRun(row interface{ Scan(...any) error }, withLog bool) (*Run, error) {
	var r Run
	var job, repo, user sql.NullInt64
	var steps string
	dest := []any{&r.ID, &job, &repo, &r.Kind, &r.Trigger, &r.Status, &r.Title, &r.StartedAt, &r.FinishedAt, &r.DurationMS, &r.SnapshotID,
		&r.BytesAdded, &r.BytesTotal, &r.FilesNew, &r.FilesChanged, &r.Summary, &r.Error, &steps, &user, &r.JobName, &r.RepoName}
	if withLog {
		dest = append(dest, &r.Log)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, notFound(err)
	}
	r.JobID, r.RepositoryID, r.UserID = idPtr(job), idPtr(repo), idPtr(user)
	r.Steps = []Step{}
	fromJSON(steps, &r.Steps)
	return &r, nil
}

// RunFilter narrows ListRuns.
type RunFilter struct {
	JobID        int64
	RepositoryID int64
	Kind         string
	Status       string
	Before       int64 // id
	Limit        int
}

// ListRuns returns runs newest first (without logs).
func (s *Store) ListRuns(ctx context.Context, f RunFilter) ([]Run, error) {
	q := `SELECT ` + runCols + ` FROM runs r LEFT JOIN jobs j ON j.id = r.job_id LEFT JOIN repositories p ON p.id = r.repository_id WHERE 1=1`
	args := []any{}
	if f.JobID > 0 {
		q += ` AND r.job_id = ?`
		args = append(args, f.JobID)
	}
	if f.RepositoryID > 0 {
		q += ` AND r.repository_id = ?`
		args = append(args, f.RepositoryID)
	}
	if f.Kind != "" {
		q += ` AND r.kind IN (` + placeholders(strings.Split(f.Kind, ","), &args) + `)`
	}
	if f.Status != "" {
		q += ` AND r.status IN (` + placeholders(strings.Split(f.Status, ","), &args) + `)`
	}
	if f.Before > 0 {
		q += ` AND r.id < ?`
		args = append(args, f.Before)
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	q += ` ORDER BY r.id DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func placeholders(vals []string, args *[]any) string {
	ph := make([]string, 0, len(vals))
	for _, v := range vals {
		ph = append(ph, "?")
		*args = append(*args, strings.TrimSpace(v))
	}
	return strings.Join(ph, ",")
}

// GetRun returns one run with its log.
func (s *Store) GetRun(ctx context.Context, id int64) (*Run, error) {
	return scanRun(s.DB.QueryRowContext(ctx, `SELECT `+runCols+`, r.log FROM runs r LEFT JOIN jobs j ON j.id = r.job_id LEFT JOIN repositories p ON p.id = r.repository_id WHERE r.id = ?`, id), true)
}

// LastRun returns a job's newest run of one of the kinds ("" = any).
func (s *Store) LastRun(ctx context.Context, jobID int64, kinds string, finishedOnly bool) (*Run, error) {
	q := `SELECT ` + runCols + ` FROM runs r LEFT JOIN jobs j ON j.id = r.job_id LEFT JOIN repositories p ON p.id = r.repository_id WHERE r.job_id = ?`
	args := []any{jobID}
	if kinds != "" {
		q += ` AND r.kind IN (` + placeholders(strings.Split(kinds, ","), &args) + `)`
	}
	if finishedOnly {
		q += ` AND r.status <> 'running'`
	}
	q += ` ORDER BY r.id DESC LIMIT 1`
	return scanRun(s.DB.QueryRowContext(ctx, q, args...), false)
}

// LastGoodRun returns a job's newest successful (or warning) run of the kinds.
func (s *Store) LastGoodRun(ctx context.Context, jobID int64, kinds string) (*Run, error) {
	q := `SELECT ` + runCols + ` FROM runs r LEFT JOIN jobs j ON j.id = r.job_id LEFT JOIN repositories p ON p.id = r.repository_id WHERE r.job_id = ? AND r.status IN ('success','warning')`
	args := []any{jobID}
	if kinds != "" {
		q += ` AND r.kind IN (` + placeholders(strings.Split(kinds, ","), &args) + `)`
	}
	q += ` ORDER BY r.id DESC LIMIT 1`
	return scanRun(s.DB.QueryRowContext(ctx, q, args...), false)
}

// CreateRun starts a run.
func (s *Store) CreateRun(ctx context.Context, r *Run) error {
	if r.StartedAt == "" {
		r.StartedAt = Now()
	}
	if r.Status == "" {
		r.Status = "running"
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO runs (job_id, repository_id, kind, trigger, status, title, started_at, steps, user_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullID(r.JobID), nullID(r.RepositoryID), r.Kind, r.Trigger, r.Status, r.Title, r.StartedAt, toJSON(r.Steps), nullID(r.UserID))
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

// UpdateRun stores a run's progress or outcome.
func (s *Store) UpdateRun(ctx context.Context, r *Run) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE runs SET status = ?, title = ?, finished_at = ?, duration_ms = ?, snapshot_id = ?, bytes_added = ?, bytes_total = ?,
		files_new = ?, files_changed = ?, summary = ?, error = ?, steps = ?, log = ? WHERE id = ?`,
		r.Status, r.Title, r.FinishedAt, r.DurationMS, r.SnapshotID, r.BytesAdded, r.BytesTotal, r.FilesNew, r.FilesChanged, r.Summary, r.Error, toJSON(r.Steps), r.Log, r.ID)
	return err
}

// AbandonRunning marks runs left "running" by a restart as cancelled.
func (s *Store) AbandonRunning(ctx context.Context) int64 {
	res, err := s.DB.ExecContext(ctx, `UPDATE runs SET status = 'cancelled', error = 'the app restarted while this was running', finished_at = ? WHERE status = 'running'`, Now())
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}

// PruneRuns drops runs older than days (keeping the newest per job).
func (s *Store) PruneRuns(ctx context.Context, days int) {
	cut := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM runs WHERE started_at < ? AND id NOT IN (SELECT MAX(id) FROM runs GROUP BY job_id, kind)`, cut)
}

// ---------- coverage ----------

// ListCoverageSkips returns the projects left out on purpose.
func (s *Store) ListCoverageSkips(ctx context.Context) ([]CoverageSkip, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT project, reason, created_at FROM coverage_skips ORDER BY project`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CoverageSkip{}
	for rows.Next() {
		var c CoverageSkip
		if rows.Scan(&c.Project, &c.Reason, &c.CreatedAt) == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// SetCoverageSkip leaves a project out (or, with skip false, takes it back).
func (s *Store) SetCoverageSkip(ctx context.Context, project, reason string, skip bool) error {
	if !skip {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM coverage_skips WHERE project = ?`, project)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO coverage_skips (project, reason) VALUES (?, ?) ON CONFLICT(project) DO UPDATE SET reason = excluded.reason`, project, reason)
	return err
}

// ---------- Backrest ----------

// BackrestSeen reports whether an operation was already told about, and
// marks it seen.
func (s *Store) BackrestSeen(ctx context.Context, opID string) bool {
	res, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO backrest_seen (op_id) VALUES (?)`, opID)
	if err != nil {
		return true
	}
	n, _ := res.RowsAffected()
	return n == 0
}
