package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Repo is everything restic needs to open a repository.
type Repo struct {
	URI          string
	Password     string
	PasswordFile string
	Compression  string // auto | max | off
	Background   bool
}

func (e *Engine) resticEnv(r Repo) []string {
	env := []string{"RESTIC_REPOSITORY=" + r.URI, "RESTIC_PROGRESS_FPS=0.2"}
	if r.PasswordFile != "" && r.Password == "" {
		env = append(env, "RESTIC_PASSWORD_FILE="+r.PasswordFile)
	} else {
		env = append(env, "RESTIC_PASSWORD="+r.Password)
	}
	if e.CacheDir != "" {
		env = append(env, "RESTIC_CACHE_DIR="+e.CacheDir)
	}
	if e.TmpDir != "" {
		_ = os.MkdirAll(e.TmpDir, 0o700)
		env = append(env, "TMPDIR="+e.TmpDir)
	}
	if r.Compression != "" {
		env = append(env, "RESTIC_COMPRESSION="+r.Compression)
	}
	if e.RcloneConfig != "" {
		env = append(env, "RCLONE_CONFIG="+e.RcloneConfig)
	}
	return env
}

func (e *Engine) restic(ctx context.Context, r Repo, log *Log, args ...string) ([]byte, error) {
	return e.run(ctx, cmdSpec{name: e.ResticBin, args: args, env: e.resticEnv(r), log: log, background: r.Background})
}

// lockWait makes restic wait for a lock another process (Backrest, a
// check) holds instead of failing at once.
const lockWait = "--retry-lock=30m"

// IsRepo reports whether a repository exists there and opens.
func (e *Engine) IsRepo(ctx context.Context, r Repo) error {
	_, err := e.restic(ctx, r, nil, "cat", "config", "--no-lock")
	return err
}

// Init creates a repository.
func (e *Engine) Init(ctx context.Context, r Repo, log *Log) error {
	_, err := e.restic(ctx, r, log, "init")
	return err
}

// Snapshot is one restic snapshot.
type Snapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Paths    []string  `json:"paths"`
	Tags     []string  `json:"tags"`
	Summary  *struct {
		FilesNew            int64 `json:"files_new"`
		FilesChanged        int64 `json:"files_changed"`
		DataAdded           int64 `json:"data_added"`
		DataAddedPacked     int64 `json:"data_added_packed"`
		TotalFilesProcessed int64 `json:"total_files_processed"`
		TotalBytesProcessed int64 `json:"total_bytes_processed"`
	} `json:"summary,omitempty"`
}

// Snapshots lists the repository's snapshots, oldest first.
func (e *Engine) Snapshots(ctx context.Context, r Repo) ([]Snapshot, error) {
	b, err := e.restic(ctx, r, nil, "snapshots", "--json", "--no-lock")
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("restic snapshots: %w", err)
	}
	if out == nil {
		out = []Snapshot{}
	}
	return out, nil
}

// BackupOptions tune a snapshot.
type BackupOptions struct {
	Paths         []string
	Excludes      []string
	Tags          []string
	Host          string
	ExcludeCaches bool
	OnProgress    func(Progress)
}

// Progress is restic's status line.
type Progress struct {
	Percent    float64 `json:"percent_done"`
	TotalFiles int64   `json:"total_files"`
	FilesDone  int64   `json:"files_done"`
	TotalBytes int64   `json:"total_bytes"`
	BytesDone  int64   `json:"bytes_done"`
	Elapsed    int64   `json:"seconds_elapsed"`
	Remaining  int64   `json:"seconds_remaining"`
}

// BackupResult is restic's summary of a snapshot.
type BackupResult struct {
	SnapshotID   string `json:"snapshot_id"`
	FilesNew     int64  `json:"files_new"`
	FilesChanged int64  `json:"files_changed"`
	FilesSame    int64  `json:"files_unmodified"`
	DataAdded    int64  `json:"data_added"`
	DataPacked   int64  `json:"data_added_packed"`
	TotalFiles   int64  `json:"total_files_processed"`
	TotalBytes   int64  `json:"total_bytes_processed"`
	Duration     float64
	// Warnings are files restic could not read (exit code 3: the snapshot
	// exists but is incomplete).
	Warnings []string
}

// Backup makes a snapshot. A snapshot with unreadable files comes back with
// a result AND Warnings set (restic exit 3), not as an error.
func (e *Engine) Backup(ctx context.Context, r Repo, o BackupOptions, log *Log) (*BackupResult, error) {
	args := []string{"backup", "--json", lockWait}
	if o.Host != "" {
		args = append(args, "--host", o.Host)
	}
	for _, t := range o.Tags {
		args = append(args, "--tag", t)
	}
	for _, x := range o.Excludes {
		if strings.TrimSpace(x) != "" {
			args = append(args, "--exclude", x)
		}
	}
	if o.ExcludeCaches {
		args = append(args, "--exclude-caches")
	}
	args = append(args, o.Paths...)
	res := &BackupResult{}
	var lastPct float64 = -1
	_, err := e.run(ctx, cmdSpec{name: e.ResticBin, args: args, env: e.resticEnv(r), log: log, background: r.Background, onLine: func(line string) {
		var m struct {
			Type string `json:"message_type"`
			// error
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			During string `json:"during"`
			Item   string `json:"item"`
			// summary
			SnapshotID   string  `json:"snapshot_id"`
			FilesNew     int64   `json:"files_new"`
			FilesChanged int64   `json:"files_changed"`
			FilesSame    int64   `json:"files_unmodified"`
			DataAdded    int64   `json:"data_added"`
			DataPacked   int64   `json:"data_added_packed"`
			TotalFiles   int64   `json:"total_files_processed"`
			TotalBytes   int64   `json:"total_bytes_processed"`
			Duration     float64 `json:"total_duration"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			log.Printf("%s", line)
			return
		}
		switch m.Type {
		case "status":
			var p Progress
			if json.Unmarshal([]byte(line), &p) == nil {
				if o.OnProgress != nil {
					o.OnProgress(p)
				}
				if p.Percent-lastPct >= 0.1 {
					lastPct = p.Percent
					log.Printf("… %.0f%% (%d of %d files, %s of %s)", p.Percent*100, p.FilesDone, p.TotalFiles, HumanBytes(p.BytesDone), HumanBytes(p.TotalBytes))
				}
			}
		case "error":
			msg := m.Item
			if m.Error != nil {
				msg = m.Item + ": " + m.Error.Message
			}
			res.Warnings = append(res.Warnings, msg)
			log.Printf("could not read %s", msg)
		case "summary":
			res.SnapshotID, res.FilesNew, res.FilesChanged, res.FilesSame = m.SnapshotID, m.FilesNew, m.FilesChanged, m.FilesSame
			res.DataAdded, res.DataPacked, res.TotalFiles, res.TotalBytes, res.Duration = m.DataAdded, m.DataPacked, m.TotalFiles, m.TotalBytes, m.Duration
			log.Printf("snapshot %s saved: %d new, %d changed, %d unchanged files; %s added (%s stored); %s in all",
				short(m.SnapshotID), m.FilesNew, m.FilesChanged, m.FilesSame, HumanBytes(m.DataAdded), HumanBytes(m.DataPacked), HumanBytes(m.TotalBytes))
		case "verbose_status":
		default:
			log.Printf("%s", line)
		}
	}})
	if err != nil {
		if ExitCode(err) == 3 && res.SnapshotID != "" {
			return res, nil
		}
		return res, err
	}
	return res, nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ForgetPolicy is a retention policy in restic's terms.
type ForgetPolicy struct {
	KeepLast, KeepHourly, KeepDaily, KeepWeekly, KeepMonthly, KeepYearly int
	GroupByNone                                                          bool
	Prune                                                                bool
	Tags                                                                 []string // only snapshots with these tags
}

// Forget applies a retention policy (and prunes when asked).
func (e *Engine) Forget(ctx context.Context, r Repo, p ForgetPolicy, log *Log) error {
	args := []string{"forget", lockWait}
	add := func(flag string, n int) {
		if n > 0 {
			args = append(args, flag, strconv.Itoa(n))
		}
	}
	add("--keep-last", p.KeepLast)
	add("--keep-hourly", p.KeepHourly)
	add("--keep-daily", p.KeepDaily)
	add("--keep-weekly", p.KeepWeekly)
	add("--keep-monthly", p.KeepMonthly)
	add("--keep-yearly", p.KeepYearly)
	if len(args) == 2 {
		return errors.New("a retention policy needs at least one keep rule")
	}
	if p.GroupByNone {
		args = append(args, "--group-by", "")
	}
	if len(p.Tags) > 0 {
		args = append(args, "--tag", strings.Join(p.Tags, ","))
	}
	if p.Prune {
		args = append(args, "--prune")
	}
	_, err := e.run(ctx, cmdSpec{name: e.ResticBin, args: args, env: e.resticEnv(r), log: log, logOut: true, background: r.Background})
	return err
}

// Prune removes data no snapshot needs any more.
func (e *Engine) Prune(ctx context.Context, r Repo, log *Log) error {
	_, err := e.run(ctx, cmdSpec{name: e.ResticBin, args: []string{"prune", lockWait}, env: e.resticEnv(r), log: log, logOut: true, background: r.Background})
	return err
}

// Check verifies the repository; readPct > 0 also reads that share of the data.
func (e *Engine) Check(ctx context.Context, r Repo, readPct int, log *Log) error {
	args := []string{"check", lockWait}
	if readPct >= 100 {
		args = append(args, "--read-data")
	} else if readPct > 0 {
		args = append(args, fmt.Sprintf("--read-data-subset=%d%%", readPct))
	}
	_, err := e.run(ctx, cmdSpec{name: e.ResticBin, args: args, env: e.resticEnv(r), log: log, logOut: true, background: r.Background})
	return err
}

// Unlock removes stale locks.
func (e *Engine) Unlock(ctx context.Context, r Repo, log *Log) error {
	_, err := e.restic(ctx, r, log, "unlock")
	return err
}

// Stats returns the repository's stored size (raw data, after compression).
func (e *Engine) Stats(ctx context.Context, r Repo) (int64, error) {
	b, err := e.restic(ctx, r, nil, "stats", "--mode", "raw-data", "--json", "--no-lock")
	if err != nil {
		return 0, err
	}
	var st struct {
		TotalSize int64 `json:"total_size"`
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return 0, err
	}
	return st.TotalSize, nil
}

// Node is one entry of a snapshot.
type Node struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"` // file | dir | symlink | …
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	Mode  uint32    `json:"mode"`
	MTime time.Time `json:"mtime"`
}

// Ls lists one folder of a snapshot (not its subfolders).
func (e *Engine) Ls(ctx context.Context, r Repo, snapshot, dir string) ([]Node, error) {
	args := []string{"ls", "--json", "--no-lock", snapshot}
	if dir != "" && dir != "/" {
		args = append(args, dir)
	}
	b, err := e.restic(ctx, r, nil, args...)
	if err != nil {
		return nil, err
	}
	out := []Node{}
	for _, line := range strings.Split(string(b), "\n") {
		var n struct {
			Node
			StructType string `json:"struct_type"`
		}
		if json.Unmarshal([]byte(line), &n) != nil || n.StructType != "node" {
			continue
		}
		if dir != "" && dir != "/" && strings.TrimRight(n.Path, "/") == strings.TrimRight(dir, "/") {
			continue // the folder itself
		}
		out = append(out, n.Node)
	}
	return out, nil
}

// LsAll walks every file of a snapshot (for the size report).
func (e *Engine) LsAll(ctx context.Context, r Repo, snapshot string, fn func(Node)) error {
	_, err := e.run(ctx, cmdSpec{name: e.ResticBin, args: []string{"ls", "--json", "--no-lock", "-l", snapshot}, env: e.resticEnv(r), onLine: func(line string) {
		var n struct {
			Node
			StructType string `json:"struct_type"`
		}
		if json.Unmarshal([]byte(line), &n) == nil && n.StructType == "node" {
			fn(n.Node)
		}
	}})
	return err
}

// Dump writes one file of a snapshot (or a folder as a zip/tar archive) to w.
func (e *Engine) Dump(ctx context.Context, r Repo, snapshot, path, archive string, w io.Writer) error {
	args := []string{"dump", "--no-lock"}
	if archive != "" {
		args = append(args, "--archive", archive)
	}
	args = append(args, snapshot, path)
	_, err := e.run(ctx, cmdSpec{name: e.ResticBin, args: args, env: e.resticEnv(r), stdout: w})
	return err
}

// Restore puts files of a snapshot back into target (includes = paths in the snapshot).
func (e *Engine) Restore(ctx context.Context, r Repo, snapshot, target string, includes []string, overwrite string, log *Log) error {
	args := []string{"restore", "--no-lock", snapshot, "--target", target}
	for _, inc := range includes {
		args = append(args, "--include", inc)
	}
	if overwrite != "" {
		args = append(args, "--overwrite", overwrite)
	}
	_, err := e.run(ctx, cmdSpec{name: e.ResticBin, args: args, env: e.resticEnv(r), log: log, logOut: true, background: r.Background})
	return err
}

// CopySnapshots copies snapshots from one repository into another (restic copy).
func (e *Engine) CopySnapshots(ctx context.Context, from, to Repo, log *Log) error {
	env := e.resticEnv(to)
	env = append(env, "RESTIC_FROM_REPOSITORY="+from.URI)
	if from.PasswordFile != "" && from.Password == "" {
		env = append(env, "RESTIC_FROM_PASSWORD_FILE="+from.PasswordFile)
	} else {
		env = append(env, "RESTIC_FROM_PASSWORD="+from.Password)
	}
	_, err := e.run(ctx, cmdSpec{name: e.ResticBin, args: []string{"copy", lockWait}, env: env, log: log, logOut: true, background: to.Background})
	return err
}

// HumanBytes writes a size the way people read it.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
