package engine

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (e *Engine) dockerArgs(args ...string) []string {
	if e.DockerHost != "" {
		return append([]string{"-H", e.DockerHost}, args...)
	}
	return args
}

// Output is where a capture is written: atomically (a temp file renamed
// into place, so a failed dump never replaces last night's good one).
type Output struct {
	Path     string
	Private  bool // 0600
	Compress bool // gzip
}

type outFile struct {
	f    *os.File
	gz   *gzip.Writer
	w    io.Writer
	tmp  string
	dest string
	n    *countWriter
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func openOut(o Output) (*outFile, error) {
	if err := os.MkdirAll(filepath.Dir(o.Path), 0o755); err != nil {
		return nil, err
	}
	mode := os.FileMode(0o644)
	if o.Private {
		mode = 0o600
	}
	tmp := o.Path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return nil, err
	}
	_ = f.Chmod(mode)
	of := &outFile{f: f, tmp: tmp, dest: o.Path}
	of.n = &countWriter{w: f}
	of.w = of.n
	if o.Compress {
		of.gz = gzip.NewWriter(of.n)
		of.w = of.gz
	}
	return of, nil
}

func (o *outFile) Write(p []byte) (int, error) { return o.w.Write(p) }

// finish renames the temp file into place (ok) or removes it.
func (o *outFile) finish(ok bool) (int64, error) {
	if o.gz != nil {
		_ = o.gz.Close()
	}
	err := o.f.Close()
	if !ok || err != nil {
		_ = os.Remove(o.tmp)
		return 0, err
	}
	if err := os.Rename(o.tmp, o.dest); err != nil {
		_ = os.Remove(o.tmp)
		return 0, err
	}
	return o.n.n, nil
}

// ContainerRunning reports whether a container exists and runs.
func (e *Engine) ContainerRunning(ctx context.Context, name string) bool {
	b, err := e.run(ctx, cmdSpec{name: e.DockerBin, args: e.dockerArgs("inspect", "-f", "{{.State.Running}}", name)})
	return err == nil && strings.TrimSpace(string(b)) == "true"
}

// ExecToFile runs a command in a container and writes its standard output
// to out. env is passed with -e (values are not logged).
func (e *Engine) ExecToFile(ctx context.Context, container string, cmd []string, env []string, user string, out Output, okCodes []int, log *Log) (int64, error) {
	args := []string{"exec", "-i"}
	if user != "" {
		args = append(args, "-u", user)
	}
	for _, kv := range env {
		args = append(args, "-e", kv)
	}
	args = append(args, container)
	args = append(args, cmd...)
	f, err := openOut(out)
	if err != nil {
		return 0, err
	}
	_, err = e.run(ctx, cmdSpec{name: e.DockerBin, args: e.dockerArgs(args...), stdout: f, log: log})
	if err != nil && codeOK(ExitCode(err), okCodes) {
		err = nil
	}
	n, ferr := f.finish(err == nil)
	if err == nil {
		err = ferr
	}
	return n, err
}

func codeOK(code int, ok []int) bool {
	for _, c := range ok {
		if c == code && code > 0 {
			return true
		}
	}
	return false
}

// ExportVolume writes a named volume as a gzipped tar.
func (e *Engine) ExportVolume(ctx context.Context, volume string, out Output, log *Log) (int64, error) {
	f, err := openOut(Output{Path: out.Path, Private: out.Private})
	if err != nil {
		return 0, err
	}
	_, err = e.run(ctx, cmdSpec{name: e.DockerBin, args: e.dockerArgs("run", "--rm", "--network", "none", "-u", "0", "-v", volume+":/data:ro",
		"--entrypoint", "tar", e.helper(), "czf", "-", "-C", "/data", "."), stdout: f, log: log})
	if ExitCode(err) == 1 {
		err = nil // "file changed as we read it": a live database in the volume; the archive is still whole
	}
	n, ferr := f.finish(err == nil)
	if err == nil {
		err = ferr
	}
	return n, err
}

func (e *Engine) helper() string {
	if e.HelperImage != "" {
		return e.HelperImage
	}
	return "alpine:3.21"
}

// ErrNoHostAccess is returned when a host command is asked for but HOST_ACCESS is off.
var ErrNoHostAccess = errors.New("this needs host access: set HOST_ACCESS=true for Pocket Backups (it runs a short-lived privileged helper container)")

// HostRun runs a shell script on the host itself (all of the host's
// namespaces), as root or as runAs (a user name or uid). Its standard
// output goes to stdout (nil = returned).
func (e *Engine) HostRun(ctx context.Context, script, runAs string, stdout io.Writer, log *Log) ([]byte, error) {
	if !e.HostAccess {
		return nil, ErrNoHostAccess
	}
	inner := script
	if runAs != "" && runAs != "root" && runAs != "0" {
		// a login-like environment for the user: their HOME, PATH with nix,
		// and their systemd user manager
		inner = fmt.Sprintf(`U=%s; UID_=$(id -u "$U") || exit 70; GID_=$(id -g "$U"); H=$(getent passwd "$U" | cut -d: -f6)
exec setpriv --reuid="$UID_" --regid="$GID_" --init-groups env -i HOME="$H" USER="$(id -un "$U")" LOGNAME="$(id -un "$U")" \
  PATH="$H/.nix-profile/bin:/etc/profiles/per-user/$(id -un "$U")/bin:/nix/var/nix/profiles/default/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
  XDG_RUNTIME_DIR="/run/user/$UID_" DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$UID_/bus" LANG=C.UTF-8 /bin/sh -c %s`, shellQuote(runAs), shellQuote(script))
	}
	args := e.dockerArgs("run", "--rm", "-i", "--privileged", "--pid=host", "--net=host", "--ipc=host", "--uts=host", "-u", "0",
		"--entrypoint", "nsenter", e.helper(), "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", "/bin/sh", "-c", inner)
	return e.run(ctx, cmdSpec{name: e.DockerBin, args: args, stdout: stdout, log: log})
}

// HostToFile runs a host script and writes its output to out.
func (e *Engine) HostToFile(ctx context.Context, script, runAs string, out Output, okCodes []int, log *Log) (int64, error) {
	f, err := openOut(out)
	if err != nil {
		return 0, err
	}
	_, err = e.HostRun(ctx, script, runAs, f, log)
	if err != nil && codeOK(ExitCode(err), okCodes) {
		err = nil
	}
	n, ferr := f.finish(err == nil)
	if err == nil {
		err = ferr
	}
	return n, err
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Container is a running container as the coverage check sees it.
type Container struct {
	Name       string  `json:"name"`
	Image      string  `json:"image"`
	Project    string  `json:"project"`
	Service    string  `json:"service"`
	WorkingDir string  `json:"working_dir"`
	State      string  `json:"state"`
	Mounts     []Mount `json:"mounts"`
}

// Mount is one of a container's mounts.
type Mount struct {
	Type        string `json:"type"` // bind | volume | tmpfs
	Source      string `json:"source"`
	Name        string `json:"name,omitempty"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
}

// Containers lists the running containers with their compose labels and mounts.
func (e *Engine) Containers(ctx context.Context) ([]Container, error) {
	ids, err := e.run(ctx, cmdSpec{name: e.DockerBin, args: e.dockerArgs("ps", "-q", "--no-trunc")})
	if err != nil {
		return nil, err
	}
	list := strings.Fields(string(ids))
	if len(list) == 0 {
		return []Container{}, nil
	}
	b, err := e.run(ctx, cmdSpec{name: e.DockerBin, args: e.dockerArgs(append([]string{"inspect"}, list...)...)})
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name   string `json:"Name"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
		Mounts []struct {
			Type        string `json:"Type"`
			Name        string `json:"Name"`
			Source      string `json:"Source"`
			Destination string `json:"Destination"`
			RW          bool   `json:"RW"`
		} `json:"Mounts"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		c := Container{Name: strings.TrimPrefix(r.Name, "/"), Image: r.Config.Image, State: r.State.Status,
			Project: r.Config.Labels["com.docker.compose.project"], Service: r.Config.Labels["com.docker.compose.service"],
			WorkingDir: r.Config.Labels["com.docker.compose.project.working_dir"], Mounts: []Mount{}}
		for _, m := range r.Mounts {
			c.Mounts = append(c.Mounts, Mount{Type: m.Type, Source: m.Source, Name: m.Name, Destination: m.Destination, RW: m.RW})
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ContainerEnv reads the names (not values) of a container's environment.
func (e *Engine) ContainerEnv(ctx context.Context, name string) ([]string, error) {
	b, err := e.run(ctx, cmdSpec{name: e.DockerBin, args: e.dockerArgs("inspect", "-f", "{{json .Config.Env}}", name)})
	if err != nil {
		return nil, err
	}
	var env []string
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		names = append(names, k)
	}
	return names, nil
}

// Volumes lists the named volumes.
func (e *Engine) Volumes(ctx context.Context) ([]string, error) {
	b, err := e.run(ctx, cmdSpec{name: e.DockerBin, args: e.dockerArgs("volume", "ls", "-q")})
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(b)), nil
}
