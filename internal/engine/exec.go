// Package engine runs the tools that do the work: restic for snapshots,
// rclone for clouds and copies, docker for databases in containers, and a
// short-lived privileged helper container for the few things that must
// happen on the host itself (system state, mounting a USB disk).
package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Engine holds where the tools are and where they may write.
type Engine struct {
	ResticBin    string
	RcloneBin    string
	DockerBin    string
	DockerHost   string
	TmpDir       string // restic temp packs: never a RAM-backed /tmp
	CacheDir     string
	RcloneConfig string // the rclone.conf every rclone call (and restic's rclone backend) uses
	ResticHost   string // the host name snapshots are recorded under
	HelperImage  string
	HostAccess   bool
	DevDiskDir   string // the host's /dev/disk, for "is the USB disk plugged in"
}

// Log collects a run's output: kept in memory (capped: the start and the
// newest part) for the run record and readable while the run is going.
type Log struct {
	mu      sync.Mutex
	head    []byte
	tail    []byte
	limit   int
	dropped bool
}

// NewLog makes a log that keeps at most about limit bytes.
func NewLog(limit int) *Log {
	if limit <= 0 {
		limit = 256 << 10
	}
	return &Log{limit: limit}
}

// Printf adds a line.
func (l *Log) Printf(format string, args ...any) {
	if l == nil {
		return
	}
	line := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	l.Write([]byte(line)) //nolint:errcheck
}

// Write implements io.Writer.
func (l *Log) Write(p []byte) (int, error) {
	if l == nil {
		return len(p), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	headMax := l.limit / 4
	if room := headMax - len(l.head); room > 0 && len(l.tail) == 0 {
		take := min(room, len(p))
		l.head = append(l.head, p[:take]...)
		p = p[take:]
	}
	l.tail = append(l.tail, p...)
	if tailMax := l.limit - headMax; len(l.tail) > tailMax {
		l.tail = append([]byte(nil), l.tail[len(l.tail)-tailMax:]...)
		l.dropped = true
	}
	return n, nil
}

// String returns the log so far.
func (l *Log) String() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dropped {
		return string(l.head) + "\n… (the middle of this log was cut) …\n" + string(l.tail)
	}
	return string(l.head) + string(l.tail)
}

// ExitError carries a command's exit code and the end of its error output.
type ExitError struct {
	Cmd    string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if i := strings.LastIndex(msg, "\n"); i >= 0 && len(msg) > 600 {
		msg = msg[i+1:]
	}
	if len(msg) > 600 {
		msg = msg[len(msg)-600:]
	}
	if msg == "" {
		return fmt.Sprintf("%s exited with code %d", e.Cmd, e.Code)
	}
	return fmt.Sprintf("%s: %s (exit %d)", e.Cmd, msg, e.Code)
}

// ExitCode returns the exit code of a failed command (-1 when it is not one).
func ExitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

// cmdSpec describes one command.
type cmdSpec struct {
	name       string
	args       []string
	env        []string
	stdin      io.Reader
	stdout     io.Writer // nil = captured and returned
	log        *Log      // stderr (and stdout lines when logOut) go here
	logOut     bool
	background bool // lowest CPU and disk priority
	onLine     func(string)
}

// tail keeps the last n bytes written.
type tail struct {
	b []byte
	n int
}

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > t.n {
		t.b = t.b[len(t.b)-t.n:]
	}
	return len(p), nil
}

func (e *Engine) run(ctx context.Context, c cmdSpec) ([]byte, error) {
	name, args := c.name, c.args
	if c.background {
		if p, err := exec.LookPath("ionice"); err == nil {
			args = append([]string{"-c", "3", "nice", "-n", "19", name}, args...)
			name = p
		} else {
			args = append([]string{"-n", "19", name}, args...)
			name = "nice"
		}
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(cleanEnv(), c.env...)
	cmd.Stdin = c.stdin
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// stop the whole group (restic starts rclone, docker exec keeps going otherwise)
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
		}
		return nil
	}
	cmd.WaitDelay = 30 * time.Second
	errTail := &tail{n: 8 << 10}
	var errW io.Writer = errTail
	if c.log != nil {
		errW = io.MultiWriter(errTail, c.log)
	}
	var out bytes.Buffer
	var pr *io.PipeReader
	var pw *io.PipeWriter
	var lines sync.WaitGroup
	switch {
	case c.stdout != nil:
		cmd.Stdout = c.stdout
	case c.onLine != nil || c.logOut:
		pr, pw = io.Pipe()
		cmd.Stdout = pw
		lines.Add(1)
		go func() {
			defer lines.Done()
			sc := bufio.NewScanner(pr)
			sc.Buffer(make([]byte, 64<<10), 4<<20)
			for sc.Scan() {
				line := sc.Text()
				out.WriteString(line)
				out.WriteByte('\n')
				if c.onLine != nil {
					c.onLine(line)
				} else if c.log != nil {
					c.log.Printf("%s", line)
				}
			}
			_, _ = io.Copy(io.Discard, pr)
		}()
	default:
		cmd.Stdout = &out
	}
	cmd.Stderr = errW
	err := cmd.Run()
	if pw != nil {
		_ = pw.Close()
		lines.Wait()
	}
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if ctx.Err() != nil {
			return out.Bytes(), fmt.Errorf("%s: stopped (%w)", baseName(c.name), ctx.Err())
		}
		if code < 0 {
			return out.Bytes(), fmt.Errorf("%s: %w", baseName(c.name), err)
		}
		return out.Bytes(), &ExitError{Cmd: baseName(c.name), Code: code, Stderr: string(errTail.b)}
	}
	return out.Bytes(), nil
}

// cleanEnv is the process environment without restic's own variables: the
// app's settings must never leak into restic (RESTIC_HOST would silently
// filter every snapshot list to one host).
func cleanEnv() []string {
	out := []string{}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "RESTIC_") {
			out = append(out, kv)
		}
	}
	return out
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Available reports which tools can be found.
func (e *Engine) Available() map[string]string {
	out := map[string]string{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if b, err := e.run(ctx, cmdSpec{name: e.ResticBin, args: []string{"version"}}); err == nil {
		out["restic"] = strings.TrimSpace(strings.SplitN(string(b), " compiled", 2)[0])
	}
	if b, err := e.run(ctx, cmdSpec{name: e.RcloneBin, args: []string{"version"}}); err == nil {
		out["rclone"] = strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	}
	if b, err := e.run(ctx, cmdSpec{name: e.DockerBin, args: e.dockerArgs("version", "--format", "{{.Server.Version}}")}); err == nil {
		out["docker"] = "Docker " + strings.TrimSpace(string(b))
	}
	return out
}
