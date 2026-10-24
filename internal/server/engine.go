package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// connSecret opens a connection's sealed secret.
func (s *Server) connSecret(c *store.Connection) (store.ConnSecret, error) {
	var sec store.ConnSecret
	if c.Secret == "" {
		return sec, nil
	}
	plain, err := s.open(c.Secret)
	if err != nil {
		return sec, err
	}
	return sec, json.Unmarshal([]byte(plain), &sec)
}

// sealConnSecret seals a connection secret for the database.
func (s *Server) sealConnSecret(sec store.ConnSecret) (string, error) {
	b, _ := json.Marshal(sec)
	if string(b) == "{}" {
		return "", nil
	}
	return s.seal(string(b))
}

// remotePath joins a connection's root folder and a path into "cN:path".
func remotePath(c *store.Connection, p string) string {
	root := strings.Trim(c.Config.Root, "/")
	name := engine.RemoteName(c.ID) + ":"
	switch c.Kind {
	case "sftp":
		// absolute paths are absolute on the server; others go under the root
		if !strings.HasPrefix(p, "/") && c.Config.Root != "" {
			p = strings.TrimRight(c.Config.Root, "/") + "/" + strings.TrimLeft(p, "/")
		}
		return name + p
	case "b2", "s3":
		return name + joinClean(c.Config.Bucket, root, p)
	default:
		return name + joinClean(root, p)
	}
}

func joinClean(parts ...string) string {
	out := []string{}
	for _, p := range parts {
		if p = strings.Trim(p, "/"); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

// rcloneRemote turns a connection into rclone options.
func (s *Server) rcloneRemote(ctx context.Context, c *store.Connection) (engine.Remote, error) {
	sec, err := s.connSecret(c)
	if err != nil {
		return engine.Remote{}, err
	}
	o := map[string]string{}
	cfg := c.Config
	switch c.Kind {
	case "gdrive":
		o["type"] = "drive"
		o["scope"] = firstNonEmpty(cfg.Scope, "drive")
		o["client_id"], o["client_secret"], o["token"] = sec.ClientID, sec.ClientSecret, sec.Token
	case "onedrive":
		o["type"] = "onedrive"
		o["client_id"], o["client_secret"], o["token"] = sec.ClientID, sec.ClientSecret, sec.Token
	case "dropbox":
		o["type"] = "dropbox"
		o["client_id"], o["client_secret"], o["token"] = sec.ClientID, sec.ClientSecret, sec.Token
	case "b2":
		o["type"] = "b2"
		o["account"], o["key"] = cfg.Account, sec.Key
	case "s3":
		o["type"] = "s3"
		o["provider"] = firstNonEmpty(cfg.Provider, "Other")
		o["access_key_id"], o["secret_access_key"] = cfg.Account, sec.Key
		o["endpoint"], o["region"] = cfg.Endpoint, cfg.Region
	case "sftp":
		o["type"] = "sftp"
		o["host"], o["user"] = cfg.Host, cfg.User
		if cfg.Port > 0 {
			o["port"] = strconv.Itoa(cfg.Port)
		}
		o["key_file"] = cfg.KeyFile
		if sec.PrivateKey != "" {
			o["key_pem"] = strings.ReplaceAll(strings.TrimSpace(sec.PrivateKey), "\n", `\n`)
		}
		if sec.Password != "" {
			if o["pass"], err = s.eng.Obscure(ctx, sec.Password); err != nil {
				return engine.Remote{}, err
			}
		}
	case "webdav":
		o["type"] = "webdav"
		o["url"], o["vendor"], o["user"] = cfg.URL, firstNonEmpty(cfg.Vendor, "other"), cfg.User
		if sec.Password != "" {
			if o["pass"], err = s.eng.Obscure(ctx, sec.Password); err != nil {
				return engine.Remote{}, err
			}
		}
	case "pocketfiles":
		// Pocket Files through Pocket Core: a five-minute link token is the
		// bearer token; rclone asks this binary for a new one when it expires.
		exe, _ := os.Executable()
		o["type"] = "webdav"
		o["vendor"] = "other"
		o["url"] = strings.TrimRight(cfg.URL, "/") + "/dav"
		o["bearer_token_command"] = fmt.Sprintf("%s link-token %d", firstNonEmpty(exe, "pocket-backups"), c.ID)
	case "rclone":
	default:
		return engine.Remote{}, fmt.Errorf("unknown connection kind %q", c.Kind)
	}
	for k, v := range cfg.Options {
		o[k] = v
	}
	for k, v := range sec.Options {
		o[k] = v
	}
	if o["type"] == "" {
		return engine.Remote{}, errors.New("an rclone connection needs a type option")
	}
	return engine.Remote{Name: engine.RemoteName(c.ID), Options: o}, nil
}

// syncRclone writes every connection into rclone.conf (called whenever a
// connection changes, and on start).
func (s *Server) syncRclone(ctx context.Context) error {
	s.rcloneMu.Lock()
	defer s.rcloneMu.Unlock()
	conns, err := s.store.ListConnections(ctx)
	if err != nil {
		return err
	}
	remotes := make([]engine.Remote, 0, len(conns))
	for i := range conns {
		r, err := s.rcloneRemote(ctx, &conns[i])
		if err != nil {
			s.log.Warn("connection left out of rclone.conf", "connection", conns[i].Name, "err", err)
			continue
		}
		remotes = append(remotes, r)
	}
	return s.eng.WriteRcloneConfig(remotes)
}

// saveRefreshedTokens copies OAuth tokens rclone refreshed (it writes them
// into rclone.conf) back into the sealed connection secrets.
func (s *Server) saveRefreshedTokens(ctx context.Context) {
	if s.eng.RcloneConfig == "" {
		return
	}
	s.rcloneMu.Lock()
	defer s.rcloneMu.Unlock()
	remotes, err := engine.ParseRcloneConfig(s.eng.RcloneConfig)
	if err != nil {
		return
	}
	for _, r := range remotes {
		tok := r.Options["token"]
		if tok == "" || !strings.HasPrefix(r.Name, "c") {
			continue
		}
		id, err := strconv.ParseInt(r.Name[1:], 10, 64)
		if err != nil {
			continue
		}
		c, err := s.store.GetConnection(ctx, id)
		if err != nil {
			continue
		}
		sec, err := s.connSecret(c)
		if err != nil {
			continue
		}
		if c.Kind == "rclone" {
			if sec.Options == nil || sec.Options["token"] == tok {
				continue
			}
			sec.Options["token"] = tok
		} else {
			if sec.Token == tok {
				continue
			}
			sec.Token = tok
		}
		if sealed, err := s.sealConnSecret(sec); err == nil {
			_ = s.store.SetConnectionSecret(ctx, id, sealed)
			s.log.Debug("saved a refreshed sign-in", "connection", c.Name)
		}
	}
}

// repoSpec builds what restic needs to open a repository.
func (s *Server) repoSpec(ctx context.Context, r *store.Repository) (engine.Repo, error) {
	spec := engine.Repo{Compression: r.Options.Compression, Background: r.Options.Background}
	switch r.Location {
	case "local", "usb":
		if !filepath.IsAbs(r.Path) {
			return spec, fmt.Errorf("repository %s: the path must be absolute", r.Name)
		}
		spec.URI = r.Path
	case "uri":
		spec.URI = r.Path
	case "connection":
		if r.ConnectionID == nil {
			return spec, fmt.Errorf("repository %s has no connection", r.Name)
		}
		c, err := s.store.GetConnection(ctx, *r.ConnectionID)
		if err != nil {
			return spec, fmt.Errorf("repository %s: its connection is gone", r.Name)
		}
		spec.URI = "rclone:" + remotePath(c, r.Path)
	default:
		return spec, fmt.Errorf("unknown location %q", r.Location)
	}
	if r.Password != "" {
		pw, err := s.open(r.Password)
		if err != nil {
			return spec, err
		}
		spec.Password = pw
	} else if r.PasswordFile != "" {
		spec.PasswordFile = r.PasswordFile
	} else {
		return spec, fmt.Errorf("repository %s has no password", r.Name)
	}
	return spec, nil
}

// repoLock serialises this app's own work on one repository (restic's
// locks still guard against Backrest and anything else).
func (s *Server) repoLock(id int64) *sync.Mutex {
	s.locksMu.Lock()
	defer s.locksMu.Unlock()
	m, ok := s.repoLocks[id]
	if !ok {
		m = &sync.Mutex{}
		s.repoLocks[id] = m
	}
	return m
}

// usbOf returns a repository's disk (nil when it is not on one).
func usbOf(r *store.Repository) *engine.USB {
	if r.Location != "usb" || r.Options.USB == nil {
		return nil
	}
	u := r.Options.USB
	return &engine.USB{UUID: u.UUID, Label: u.Label, Mountpoint: u.Mountpoint, PowerOff: u.PowerOff}
}

// refreshRepoStats reads snapshot count, newest snapshot and size.
func (s *Server) refreshRepoStats(ctx context.Context, r *store.Repository) (*store.RepoStats, error) {
	spec, err := s.repoSpec(ctx, r)
	if err != nil {
		return nil, err
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	snaps, err := s.eng.Snapshots(c, spec)
	if err != nil {
		s.store.SetRepoStatus(ctx, r.ID, "error", err.Error())
		return nil, err
	}
	st := store.RepoStats{Snapshots: len(snaps)}
	if len(snaps) > 0 {
		st.Latest = snaps[len(snaps)-1].Time.UTC().Format(time.RFC3339)
	}
	if size, err := s.eng.Stats(c, spec); err == nil {
		st.Size = size
	} else {
		st.Size = r.Stats.Size
	}
	if r.Location == "local" || r.Location == "usb" {
		if free, pct, err := engine.DiskSpace(r.Path); err == nil {
			st.DiskFree, st.DiskUsed = free, pct
		}
	}
	s.store.SetRepoStats(ctx, r.ID, st)
	s.store.SetRepoStatus(ctx, r.ID, "ok", "")
	s.snapCache.Delete(r.ID)
	return &st, nil
}
