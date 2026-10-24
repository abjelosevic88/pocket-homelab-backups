package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
	_ "modernc.org/sqlite"
)

// Capture is how one source went.
type Capture struct {
	Output  string // the file written (relative to staging), "" for paths
	Size    int64
	Skipped bool
	Detail  string
}

// DefaultOutput is where a source's capture lands under the staging folder.
func DefaultOutput(src *store.Source) string {
	c := src.Config
	if c.Output != "" {
		return c.Output
	}
	slug := store.Slug(src.Name)
	if slug == "" {
		slug = fmt.Sprintf("source-%d", src.ID)
	}
	ext := ""
	if c.Compress {
		ext = ".gz"
	}
	switch src.Kind {
	case "sqlite":
		return "db/" + slug + "-db.sqlite3"
	case "postgres", "mariadb":
		return "db/" + slug + ".sql" + ext
	case "volume":
		return "volumes/" + firstNonEmpty(c.Volume, slug) + ".tar.gz"
	case "container_files":
		return "system/" + slug + ".tar" + ext
	default:
		return "system/" + slug + ".txt" + ext
	}
}

// SourceKinds are the kinds of sources there are.
var SourceKinds = []string{"paths", "sqlite", "postgres", "mariadb", "volume", "container_files", "container_command", "host_command"}

// NeedsStaging reports whether a kind writes into the staging folder.
func NeedsStaging(kind string) bool { return kind != "paths" }

// CaptureSource writes one source into the staging folder.
func (e *Engine) CaptureSource(ctx context.Context, src *store.Source, password, staging string, log *Log) (*Capture, error) {
	c := src.Config
	if src.Kind == "paths" {
		var missing []string
		for _, p := range c.Paths {
			if _, err := os.Stat(p); err != nil {
				missing = append(missing, p)
			}
		}
		if len(missing) > 0 {
			return &Capture{Detail: "missing: " + strings.Join(missing, ", ")}, fmt.Errorf("these folders do not exist: %s", strings.Join(missing, ", "))
		}
		return &Capture{Detail: fmt.Sprintf("%d folder(s)", len(c.Paths))}, nil
	}
	if staging == "" {
		return nil, errors.New("this job has no staging folder for dumps")
	}
	rel := DefaultOutput(src)
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return nil, fmt.Errorf("output %q must be a relative path inside the staging folder", rel)
	}
	out := Output{Path: filepath.Join(staging, rel), Private: c.Private, Compress: c.Compress}
	res := &Capture{Output: rel}
	var n int64
	var err error
	switch src.Kind {
	case "sqlite":
		if _, serr := os.Stat(c.Path); serr != nil {
			if c.SkipIfMissing {
				res.Skipped, res.Detail = true, "no database yet at "+c.Path
				return res, nil
			}
			return res, fmt.Errorf("database %s: %w", c.Path, serr)
		}
		n, err = SnapshotSQLite(ctx, c.Path, out)
	case "postgres", "mariadb", "container_files", "container_command":
		if c.Container == "" {
			return res, errors.New("which container?")
		}
		if !e.ContainerRunning(ctx, c.Container) {
			if c.SkipIfMissing {
				res.Skipped, res.Detail = true, c.Container+" is not running"
				return res, nil
			}
			return res, fmt.Errorf("container %s is not running", c.Container)
		}
		cmd, env, user := containerCommand(src, password)
		n, err = e.ExecToFile(ctx, c.Container, cmd, env, user, out, c.OKExitCodes, log)
	case "volume":
		if c.Volume == "" {
			return res, errors.New("which volume?")
		}
		out.Compress = false // the tar is gzipped already
		n, err = e.ExportVolume(ctx, c.Volume, out, log)
	case "host_command":
		if strings.TrimSpace(c.Command) == "" {
			return res, errors.New("which command?")
		}
		n, err = e.HostToFile(ctx, c.Command, c.RunAs, out, c.OKExitCodes, log)
	default:
		return res, fmt.Errorf("unknown kind %q", src.Kind)
	}
	res.Size = n
	if err == nil {
		res.Detail = HumanBytes(n)
		if n == 0 && src.Kind != "host_command" && src.Kind != "container_command" {
			return res, errors.New("the capture came out empty")
		}
	}
	return res, err
}

// containerCommand builds the command (and its secret environment) for a
// source that runs inside a container.
func containerCommand(src *store.Source, password string) (cmd []string, env []string, user string) {
	c := src.Config
	q := shellQuote
	switch src.Kind {
	case "postgres":
		u := `"$POSTGRES_USER"`
		if c.User != "" {
			u = q(c.User)
		}
		db := `"${POSTGRES_DB:-$POSTGRES_USER}"`
		if c.Database != "" {
			db = q(c.Database)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "exec pg_dump -U %s %s", u, db)
		for _, t := range c.ExcludeTableData {
			fmt.Fprintf(&b, " --exclude-table-data=%s", q(t))
		}
		for _, a := range c.ExtraArgs {
			b.WriteString(" " + q(a))
		}
		if password != "" {
			env = append(env, "PGPASSWORD="+password)
		}
		return []string{"sh", "-c", b.String()}, env, ""
	case "mariadb":
		u := "root"
		if c.User != "" {
			u = c.User
		}
		db := `"${MARIADB_DATABASE:-$MYSQL_DATABASE}"`
		if c.Database != "" {
			db = q(c.Database)
		}
		pw := ""
		switch {
		case password != "":
			env = append(env, "MYSQL_PWD="+password)
		case c.PasswordEnv != "":
			pw = fmt.Sprintf(` -p"$%s"`, safeEnvName(c.PasswordEnv))
		case u == "root":
			pw = ` -p"${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"`
		default:
			pw = ` -p"${MARIADB_PASSWORD:-$MYSQL_PASSWORD}"`
		}
		var b strings.Builder
		fmt.Fprintf(&b, `D=$(command -v mariadb-dump || command -v mysqldump); exec "$D" --single-transaction -u %s%s %s`, q(u), pw, db)
		for _, t := range c.ExcludeTableData {
			fmt.Fprintf(&b, " --ignore-table-data=%s", q(t))
		}
		for _, a := range c.ExtraArgs {
			b.WriteString(" " + q(a))
		}
		return []string{"sh", "-c", b.String()}, env, ""
	case "container_files":
		files := c.Files
		if len(files) == 0 {
			files = []string{"."}
		}
		args := []string{"tar", "cf", "-", "-C", firstNonEmpty(c.Path, "/")}
		args = append(args, files...)
		return args, nil, c.User
	default: // container_command
		return []string{"sh", "-c", c.Command}, nil, c.User
	}
}

func safeEnvName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SnapshotSQLite writes a consistent copy of a live SQLite database
// (VACUUM INTO from a read-only connection: never touches the original).
func SnapshotSQLite(ctx context.Context, src string, out Output) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(out.Path), 0o755); err != nil {
		return 0, err
	}
	tmp := out.Path + ".tmp"
	_ = os.Remove(tmp)
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro&_pragma=busy_timeout(30000)")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("snapshot %s: %w", src, err)
	}
	if out.Private {
		_ = os.Chmod(tmp, 0o600)
	} else {
		_ = os.Chmod(tmp, 0o644)
	}
	fi, err := os.Stat(tmp)
	if err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, out.Path); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return fi.Size(), nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
