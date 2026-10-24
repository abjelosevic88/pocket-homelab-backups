package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Remote is one rclone remote: a name and its options (secrets included,
// passwords already obscured the way rclone wants them).
type Remote struct {
	Name    string
	Options map[string]string
}

// RemoteName is how connection id appears in rclone.conf.
func RemoteName(connID int64) string { return fmt.Sprintf("c%d", connID) }

// WriteRcloneConfig replaces rclone.conf with these remotes (0600).
func (e *Engine) WriteRcloneConfig(remotes []Remote) error {
	if e.RcloneConfig == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(e.RcloneConfig), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Written by Pocket Backups from its connections — edit them in the app, not here.\n")
	for _, r := range remotes {
		fmt.Fprintf(&b, "\n[%s]\n", r.Name)
		keys := make([]string, 0, len(r.Options))
		for k := range r.Options {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if t, ok := r.Options["type"]; ok {
			fmt.Fprintf(&b, "type = %s\n", t)
		}
		for _, k := range keys {
			if k == "type" || r.Options[k] == "" {
				continue
			}
			fmt.Fprintf(&b, "%s = %s\n", k, strings.ReplaceAll(r.Options[k], "\n", ""))
		}
	}
	tmp := e.RcloneConfig + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.RcloneConfig)
}

// ParseRcloneConfig reads an rclone.conf into remotes (values as written).
func ParseRcloneConfig(path string) ([]Remote, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Remote
	var cur *Remote
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			out = append(out, Remote{Name: strings.TrimSpace(line[1 : len(line)-1]), Options: map[string]string{}})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur.Options[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out, sc.Err()
}

// Obscure turns a password into rclone's obscured form.
func (e *Engine) Obscure(ctx context.Context, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	b, err := e.run(ctx, cmdSpec{name: e.RcloneBin, args: []string{"obscure", "-"}, stdin: strings.NewReader(plain)})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Reveal turns an obscured rclone password back into the password.
func (e *Engine) Reveal(ctx context.Context, obscured string) (string, error) {
	if obscured == "" {
		return "", nil
	}
	b, err := e.run(ctx, cmdSpec{name: e.RcloneBin, args: []string{"reveal", obscured}})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (e *Engine) rcloneEnv() []string {
	env := []string{}
	if e.RcloneConfig != "" {
		env = append(env, "RCLONE_CONFIG="+e.RcloneConfig)
	}
	if e.CacheDir != "" {
		env = append(env, "RCLONE_CACHE_DIR="+filepath.Join(e.CacheDir, "rclone"))
	}
	return env
}

// Rclone runs rclone with the app's config.
func (e *Engine) Rclone(ctx context.Context, log *Log, args ...string) ([]byte, error) {
	return e.run(ctx, cmdSpec{name: e.RcloneBin, args: args, env: e.rcloneEnv(), log: log})
}

// SyncOptions tune a mirror.
type SyncOptions struct {
	Transfers    int
	HardDelete   bool
	DeleteBefore bool
	UseTrash     bool
	BwLimit      string
	Copy         bool // copy instead of sync: never delete at the destination
	Extra        []string
	Background   bool
}

// Sync makes dst an exact copy of src (rclone paths: "/local/dir" or "c3:dir").
func (e *Engine) Sync(ctx context.Context, src, dst string, o SyncOptions, log *Log) error {
	verb := "sync"
	if o.Copy {
		verb = "copy"
	}
	args := []string{verb, src, dst, "--stats", "1m", "--stats-one-line", "--stats-log-level", "NOTICE", "--retries", "3", "--low-level-retries", "20"}
	if o.Transfers > 0 {
		args = append(args, "--transfers", fmt.Sprint(o.Transfers))
	}
	if !o.Copy && o.DeleteBefore {
		args = append(args, "--delete-before")
	}
	if o.HardDelete && strings.Contains(dst, ":") {
		args = append(args, "--b2-hard-delete")
	}
	if strings.Contains(dst, ":") && !o.UseTrash {
		args = append(args, "--drive-use-trash=false")
	}
	if o.BwLimit != "" {
		args = append(args, "--bwlimit", o.BwLimit)
	}
	args = append(args, o.Extra...)
	_, err := e.run(ctx, cmdSpec{name: e.RcloneBin, args: args, env: e.rcloneEnv(), log: log, logOut: true, background: o.Background})
	return err
}

// Entry is one item of a remote folder.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"mod_time"`
}

// List lists a remote folder ("c3:path").
func (e *Engine) List(ctx context.Context, remotePath string, dirsOnly bool) ([]Entry, error) {
	args := []string{"lsjson", remotePath, "--no-mimetype"}
	if dirsOnly {
		args = append(args, "--dirs-only")
	}
	b, err := e.run(ctx, cmdSpec{name: e.RcloneBin, args: args, env: e.rcloneEnv()})
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name    string `json:"Name"`
		Path    string `json:"Path"`
		Size    int64  `json:"Size"`
		IsDir   bool   `json:"IsDir"`
		ModTime string `json:"ModTime"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(raw))
	for _, r := range raw {
		e := Entry{Name: r.Name, Path: r.Path, Size: r.Size, IsDir: r.IsDir}
		if t, err := time.Parse(time.RFC3339Nano, r.ModTime); err == nil {
			e.ModTime = t
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// About is a remote's space.
type About struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

// About reads a remote's quota (not every cloud has one).
func (e *Engine) About(ctx context.Context, remote string) (*About, error) {
	b, err := e.run(ctx, cmdSpec{name: e.RcloneBin, args: []string{"about", remote, "--json"}, env: e.rcloneEnv()})
	if err != nil {
		return nil, err
	}
	var a About
	return &a, json.Unmarshal(b, &a)
}

// Size returns the total size of a remote folder.
func (e *Engine) Size(ctx context.Context, remotePath string) (int64, error) {
	b, err := e.run(ctx, cmdSpec{name: e.RcloneBin, args: []string{"size", remotePath, "--json"}, env: e.rcloneEnv()})
	if err != nil {
		return 0, err
	}
	var s struct {
		Bytes int64 `json:"bytes"`
	}
	return s.Bytes, json.Unmarshal(b, &s)
}

// Test checks that a remote answers (lists its top folder).
func (e *Engine) Test(ctx context.Context, remote string) error {
	_, err := e.run(ctx, cmdSpec{name: e.RcloneBin, args: []string{"lsf", remote, "--max-depth", "1", "--dirs-only"}, env: e.rcloneEnv()})
	return err
}

// Mkdir makes a folder on a remote.
func (e *Engine) Mkdir(ctx context.Context, remotePath string) error {
	_, err := e.run(ctx, cmdSpec{name: e.RcloneBin, args: []string{"mkdir", remotePath}, env: e.rcloneEnv()})
	return err
}
