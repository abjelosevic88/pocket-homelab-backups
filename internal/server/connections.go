package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// Connections are the places outside the box backups go to (or come from):
// Google Drive, OneDrive, Dropbox, Backblaze B2, S3, an SFTP server (a NAS),
// WebDAV (Nextcloud) and Pocket Files through Pocket Core. Each becomes an
// rclone remote; restic reaches all of them through rclone.

type connKind struct {
	Kind   string  `json:"kind"`
	Name   string  `json:"name"`
	Auth   string  `json:"auth"` // oauth | form | core
	Ready  bool    `json:"ready"`
	Help   string  `json:"help"`
	Fields []field `json:"fields"`
}

type field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Secret      bool   `json:"secret,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
	Optional    bool   `json:"optional,omitempty"`
}

var connKinds = []struct{ Kind, Name string }{
	{"gdrive", "Google Drive"}, {"onedrive", "OneDrive"}, {"dropbox", "Dropbox"}, {"b2", "Backblaze B2"}, {"s3", "S3-compatible storage"},
	{"sftp", "SFTP server (a NAS)"}, {"webdav", "WebDAV (Nextcloud, ownCloud…)"}, {"pocketfiles", "Pocket Files"}, {"rclone", "Any rclone remote"},
}

func connName(kind string) string {
	for _, k := range connKinds {
		if k.Kind == kind {
			return k.Name
		}
	}
	return kind
}

func oauthKind(k string) bool { return k == "gdrive" || k == "onedrive" || k == "dropbox" }

func connFields(kind string) []field {
	switch kind {
	case "gdrive", "onedrive", "dropbox":
		return []field{{Key: "token", Label: "Token from rclone authorize", Secret: true, Optional: true,
			Help: "Instead of signing in here: run `rclone authorize \"" + map[string]string{"gdrive": "drive", "onedrive": "onedrive", "dropbox": "dropbox"}[kind] + "\"` on a computer with a browser and paste the token it prints."}}
	case "b2":
		return []field{{Key: "account", Label: "Application key ID"}, {Key: "key", Label: "Application key", Secret: true}, {Key: "bucket", Label: "Bucket"}}
	case "s3":
		return []field{{Key: "provider", Label: "Provider", Placeholder: "AWS, Cloudflare, Wasabi, Minio, Other"}, {Key: "endpoint", Label: "Endpoint", Optional: true, Placeholder: "https://s3.example.com"},
			{Key: "region", Label: "Region", Optional: true}, {Key: "account", Label: "Access key ID"}, {Key: "key", Label: "Secret access key", Secret: true}, {Key: "bucket", Label: "Bucket"}}
	case "sftp":
		return []field{{Key: "host", Label: "Host", Placeholder: "192.168.1.10"}, {Key: "port", Label: "Port", Optional: true, Placeholder: "22"}, {Key: "user", Label: "User"},
			{Key: "key_file", Label: "Private key file on this box", Optional: true, Placeholder: "/home/you/.ssh/id_ed25519"},
			{Key: "private_key", Label: "…or paste a private key", Secret: true, Optional: true}, {Key: "password", Label: "…or a password", Secret: true, Optional: true}}
	case "webdav":
		return []field{{Key: "url", Label: "WebDAV address", Placeholder: "https://cloud.example.com/remote.php/dav/files/you"}, {Key: "vendor", Label: "Server", Placeholder: "nextcloud, owncloud or other"},
			{Key: "user", Label: "User"}, {Key: "password", Label: "Password (an app password)", Secret: true}}
	case "rclone":
		return []field{{Key: "options", Label: "rclone options", Placeholder: "type = s3\nprovider = Other\n…", Help: "One option per line as in rclone.conf; secret ones (pass, key, token…) are sealed."}}
	}
	return []field{}
}

func (s *Server) oauthApp(kind string) *oauthApp {
	c := s.cfg
	switch kind {
	case "gdrive":
		if c.GoogleClientID != "" && c.GoogleClientSecret != "" {
			return googleApp(c.GoogleClientID, c.GoogleClientSecret, "drive")
		}
	case "onedrive":
		if c.MicrosoftClientID != "" && c.MicrosoftClientSecret != "" {
			return microsoftApp(c.MicrosoftClientID, c.MicrosoftClientSecret, c.MicrosoftTenant)
		}
	case "dropbox":
		if c.DropboxAppKey != "" && c.DropboxAppSecret != "" {
			return dropboxApp(c.DropboxAppKey, c.DropboxAppSecret)
		}
	}
	return nil
}

func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) oauthRedirect(r *http.Request) string {
	return s.baseURL(r) + "/api/v1/connections/oauth/callback"
}

func (s *Server) oauthHelp(r *http.Request, kind string) string {
	redirect := s.oauthRedirect(r)
	switch kind {
	case "gdrive":
		return "Signing in needs a Google OAuth client of your own: in console.cloud.google.com enable the Google Drive API, set up the consent screen (External, and publish it — in Testing Google drops the sign-in after 7 days), create an OAuth client ID of type Web application with the redirect URI " + redirect + " and set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET for Pocket Backups. Or paste a token from `rclone authorize \"drive\"`."
	case "onedrive":
		return "Signing in needs an app registration of your own in entra.microsoft.com (personal and work accounts) with the Web redirect URI " + redirect + " and a client secret; set MICROSOFT_CLIENT_ID and MICROSOFT_CLIENT_SECRET. Or paste a token from `rclone authorize \"onedrive\"`."
	case "dropbox":
		return "Signing in needs a Dropbox app of your own (dropbox.com/developers/apps: Scoped access, Full Dropbox, files.content.write) with the redirect URI " + redirect + "; set DROPBOX_APP_KEY and DROPBOX_APP_SECRET. Or paste a token from `rclone authorize \"dropbox\"`."
	}
	return ""
}

func (s *Server) handleConnKinds(w http.ResponseWriter, r *http.Request) {
	out := []connKind{}
	for _, k := range connKinds {
		ck := connKind{Kind: k.Kind, Name: k.Name, Auth: "form", Ready: true, Fields: connFields(k.Kind)}
		switch {
		case oauthKind(k.Kind):
			ck.Auth = "oauth"
			ck.Ready = s.oauthApp(k.Kind) != nil
			ck.Help = s.oauthHelp(r, k.Kind)
		case k.Kind == "pocketfiles":
			ck.Auth = "core"
			ck.Ready = s.coreConnected()
			ck.Help = "Keeps backups in your Pocket Files, through Pocket Core: no password to type, and the files are yours there. " + coreHint
		}
		out = append(out, ck)
	}
	writeJSON(w, http.StatusOK, map[string]any{"kinds": out, "import_file": s.cfg.RcloneImport})
}

const coreHint = "Needs Pocket Core. Pocket Core connects your Pocket apps to each other and to your services: one sign-in for everyone, each person's own accounts, nothing to copy or paste. Set CORE_URL and CORE_REGISTRATION_KEY for Pocket Backups."

func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	list, err := s.store.ListConnections(r.Context())
	if err != nil {
		s.fail(w, err, "connections")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type connIn struct {
	Name    string            `json:"name"`
	Kind    string            `json:"kind"`
	Config  store.ConnConfig  `json:"config"`
	Secret  map[string]string `json:"secret"`  // password, key, token, private_key
	Options string            `json:"options"` // kind rclone: rclone.conf lines
}

func (s *Server) handleSaveConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in connIn
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c := &store.Connection{}
	if id := idParam(r, "id"); id > 0 {
		var err error
		if c, err = s.store.GetConnection(ctx, id); err != nil {
			s.fail(w, err, "connection")
			return
		}
		in.Kind = c.Kind
	}
	if connName(in.Kind) == in.Kind {
		writeErr(w, http.StatusBadRequest, "unknown kind")
		return
	}
	sec, err := s.connSecret(c)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	imported, options := c.Config.Imported, c.Config.Options
	c.Kind, c.Config = in.Kind, in.Config
	c.Config.Imported = imported
	if c.Config.Options == nil {
		c.Config.Options = options
	}
	c.Name = strings.TrimSpace(firstNonEmpty(in.Name, connName(in.Kind)))
	for k, v := range in.Secret {
		switch k {
		case "password":
			sec.Password = v
		case "key":
			sec.Key = v
		case "private_key":
			sec.PrivateKey = v
		case "token":
			if strings.TrimSpace(v) != "" {
				if !json.Valid([]byte(strings.TrimSpace(v))) {
					writeErr(w, http.StatusBadRequest, "the token is not the JSON rclone authorize prints")
					return
				}
				sec.Token = strings.TrimSpace(v)
			}
		}
	}
	if in.Kind == "rclone" && in.Options != "" {
		c.Config.Options, sec.Options = splitOptions(in.Options)
	}
	if in.Kind == "pocketfiles" {
		if !s.coreConnected() {
			writeErr(w, http.StatusBadRequest, coreHint)
			return
		}
		u := userFrom(ctx)
		lt, err := s.core.ActAs(ctx, u.Email, "files", "files:read", "files:write")
		if err != nil {
			writeErr(w, http.StatusBadGateway, "Pocket Files through Pocket Core: "+err.Error())
			return
		}
		c.Config.URL = strings.TrimRight(lt.URL, "/")
		c.ActingUser = &u.ID
		c.Config.Account = u.Email
	}
	if oauthKind(in.Kind) && sec.Token == "" && c.ID == 0 {
		writeErr(w, http.StatusBadRequest, "sign in, or paste a token from rclone authorize")
		return
	}
	if c.Secret, err = s.sealConnSecret(sec); err != nil {
		s.fail(w, err, "seal")
		return
	}
	if err := s.store.SaveConnection(ctx, c); err != nil {
		s.fail(w, err, "save connection")
		return
	}
	if err := s.syncRclone(ctx); err != nil {
		s.log.Warn("rclone.conf", "err", err)
	}
	s.testConnection(ctx, c)
	c, _ = s.store.GetConnection(ctx, c.ID)
	s.touch()
	writeJSON(w, http.StatusOK, c)
}

var secretOption = []string{"pass", "key", "secret", "token", "password"}

func isSecretOption(k string) bool {
	k = strings.ToLower(k)
	if k == "key_file" || k == "pubkey_file" || k == "known_hosts_file" || k == "key_use_agent" {
		return false
	}
	for _, s := range secretOption {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

func splitOptions(text string) (plain, secret map[string]string) {
	plain, secret = map[string]string{}, map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if isSecretOption(k) {
			secret[k] = v
		} else {
			plain[k] = v
		}
	}
	return plain, secret
}

func (s *Server) testConnection(ctx context.Context, c *store.Connection) error {
	tc, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	root := remotePath(c, "")
	if c.Kind == "sftp" && c.Config.Root == "" {
		root = engine.RemoteName(c.ID) + ":"
	}
	err := s.eng.Test(tc, root)
	if err != nil {
		s.store.SetConnectionStatus(ctx, c.ID, "error", err.Error())
	} else {
		s.store.SetConnectionStatus(ctx, c.ID, "ok", "")
	}
	s.saveRefreshedTokens(ctx)
	return err
}

func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	c, err := s.store.GetConnection(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "connection")
		return
	}
	terr := s.testConnection(r.Context(), c)
	c, _ = s.store.GetConnection(r.Context(), c.ID)
	res := map[string]any{"ok": terr == nil, "connection": c}
	if terr != nil {
		res["error"] = terr.Error()
	} else if a, err := s.eng.About(r.Context(), engine.RemoteName(c.ID)+":"); err == nil && (a.Total > 0 || a.Used > 0) {
		res["about"] = a
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	err := s.store.DeleteConnection(r.Context(), idParam(r, "id"))
	if errors.Is(err, store.ErrInUse) {
		writeErr(w, http.StatusConflict, "a repository, copy or pull still uses this connection")
		return
	}
	if err != nil {
		s.fail(w, err, "delete connection")
		return
	}
	_ = s.syncRclone(r.Context())
	s.touch()
	w.WriteHeader(http.StatusNoContent)
}

// handleBrowseConnection lists a folder of a connection (the folder picker).
func (s *Server) handleBrowseConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	c, err := s.store.GetConnection(r.Context(), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "connection")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	p := r.URL.Query().Get("path")
	entries, err := s.eng.List(ctx, remotePath(c, p), queryBool(r, "dirs"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": p, "entries": entries})
}

// ---------- import from an existing rclone.conf ----------

var rcloneKind = map[string]string{"drive": "gdrive", "onedrive": "onedrive", "dropbox": "dropbox", "b2": "b2", "s3": "s3", "sftp": "sftp", "webdav": "webdav"}

func (s *Server) handleImportable(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.cfg.RcloneImport == "" {
		writeJSON(w, http.StatusOK, map[string]any{"file": "", "remotes": []any{}})
		return
	}
	remotes, err := engine.ParseRcloneConfig(s.cfg.RcloneImport)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	conns, _ := s.store.ListConnections(r.Context())
	out := []map[string]any{}
	for _, rm := range remotes {
		imported := false
		for _, c := range conns {
			if c.Config.Imported == "rclone.conf: "+rm.Name {
				imported = true
			}
		}
		kind := rcloneKind[rm.Options["type"]]
		if kind == "" {
			kind = "rclone"
		}
		out = append(out, map[string]any{"name": rm.Name, "type": rm.Options["type"], "kind": kind, "scope": rm.Options["scope"], "imported": imported})
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": s.cfg.RcloneImport, "remotes": out})
}

func (s *Server) handleImportConnections(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var in struct {
		Names []string `json:"names"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	remotes, err := engine.ParseRcloneConfig(s.cfg.RcloneImport)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	want := map[string]bool{}
	for _, n := range in.Names {
		want[n] = true
	}
	added := []store.Connection{}
	for _, rm := range remotes {
		if !want[rm.Name] {
			continue
		}
		c, err := s.importRemote(ctx, rm)
		if err != nil {
			writeErr(w, http.StatusBadRequest, rm.Name+": "+err.Error())
			return
		}
		added = append(added, *c)
	}
	_ = s.syncRclone(ctx)
	for i := range added {
		_ = s.testConnection(ctx, &added[i])
	}
	s.touch()
	writeJSON(w, http.StatusOK, added)
}

func (s *Server) importRemote(ctx context.Context, rm engine.Remote) (*store.Connection, error) {
	o := map[string]string{}
	for k, v := range rm.Options {
		o[k] = v
	}
	kind := rcloneKind[o["type"]]
	if kind == "" {
		kind = "rclone"
	}
	c := &store.Connection{Name: rm.Name, Kind: kind, Config: store.ConnConfig{Imported: "rclone.conf: " + rm.Name}}
	var sec store.ConnSecret
	take := func(k string) string {
		v := o[k]
		delete(o, k)
		return v
	}
	reveal := func(k string) (string, error) {
		v := take(k)
		if v == "" {
			return "", nil
		}
		return s.eng.Reveal(ctx, v)
	}
	var err error
	switch kind {
	case "gdrive", "onedrive", "dropbox":
		take("type")
		sec.Token, sec.ClientID, sec.ClientSecret = take("token"), take("client_id"), take("client_secret")
		if kind == "gdrive" {
			c.Config.Scope = take("scope")
		}
	case "b2":
		take("type")
		c.Config.Account, sec.Key = take("account"), take("key")
	case "s3":
		take("type")
		c.Config.Provider, c.Config.Endpoint, c.Config.Region = take("provider"), take("endpoint"), take("region")
		c.Config.Account, sec.Key = take("access_key_id"), take("secret_access_key")
	case "sftp":
		take("type")
		c.Config.Host, c.Config.User, c.Config.KeyFile = take("host"), take("user"), take("key_file")
		if p := take("port"); p != "" {
			c.Config.Port, _ = strconv.Atoi(p)
		}
		if sec.Password, err = reveal("pass"); err != nil {
			return nil, err
		}
		sec.PrivateKey = strings.ReplaceAll(take("key_pem"), `\n`, "\n")
	case "webdav":
		take("type")
		c.Config.URL, c.Config.Vendor, c.Config.User = take("url"), take("vendor"), take("user")
		if sec.Password, err = reveal("pass"); err != nil {
			return nil, err
		}
	}
	plain, secret := map[string]string{}, map[string]string{}
	for k, v := range o {
		if isSecretOption(k) {
			secret[k] = v
		} else {
			plain[k] = v
		}
	}
	if len(plain) > 0 {
		c.Config.Options = plain
	}
	if len(secret) > 0 {
		sec.Options = secret
	}
	if c.Secret, err = s.sealConnSecret(sec); err != nil {
		return nil, err
	}
	if err := s.store.SaveConnection(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ---------- OAuth sign-in ----------

type oauthApp struct {
	Kind, AuthURL, TokenURL, Scopes, ClientID, ClientSecret string
	Extra                                                   url.Values
}

func googleApp(id, secret, scope string) *oauthApp {
	return &oauthApp{Kind: "gdrive", AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", ClientID: id, ClientSecret: secret,
		Scopes: "openid email https://www.googleapis.com/auth/" + scope, Extra: url.Values{"access_type": {"offline"}, "prompt": {"consent"}}}
}

func microsoftApp(id, secret, tenant string) *oauthApp {
	if tenant == "" {
		tenant = "common"
	}
	return &oauthApp{Kind: "onedrive", AuthURL: "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/authorize", TokenURL: "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/token",
		ClientID: id, ClientSecret: secret, Scopes: "offline_access User.Read Files.ReadWrite.All", Extra: url.Values{"prompt": {"select_account"}}}
}

func dropboxApp(key, secret string) *oauthApp {
	return &oauthApp{Kind: "dropbox", AuthURL: "https://www.dropbox.com/oauth2/authorize", TokenURL: "https://api.dropboxapi.com/oauth2/token", ClientID: key, ClientSecret: secret,
		Scopes: "account_info.read files.metadata.read files.metadata.write files.content.read files.content.write", Extra: url.Values{"token_access_type": {"offline"}}}
}

type oauthToken struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

func (a *oauthApp) exchange(ctx context.Context, hc *http.Client, code, redirect string) (*oauthToken, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {a.ClientID}, "client_secret": {a.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &out)
	if res.StatusCode != 200 || out.AccessToken == "" {
		return nil, fmt.Errorf("sign-in failed: %s", firstNonEmpty(out.Description, out.Error, res.Status))
	}
	return &oauthToken{AccessToken: out.AccessToken, TokenType: "Bearer", RefreshToken: out.RefreshToken, Expiry: time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)}, nil
}

const oauthCookie = "pbk_oauth"

// handleOAuthStart sends the browser to the cloud's sign-in.
func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	kind := chi.URLParam(r, "kind")
	if !oauthKind(kind) {
		writeErr(w, http.StatusNotFound, "unknown cloud")
		return
	}
	app := s.oauthApp(kind)
	if app == nil {
		writeErr(w, http.StatusBadRequest, s.oauthHelp(r, kind))
		return
	}
	scope := r.URL.Query().Get("scope")
	if kind == "gdrive" {
		switch scope {
		case "drive.file", "drive.readonly":
		default:
			scope = "drive"
		}
		app = googleApp(app.ClientID, app.ClientSecret, scope)
	}
	conn, _ := strconv.ParseInt(r.URL.Query().Get("connection"), 10, 64)
	name := strings.ReplaceAll(firstNonEmpty(r.URL.Query().Get("name"), connName(kind)), "|", " ")
	state := store.RandomToken(16)
	payload := fmt.Sprintf("%s|%d|%s|%s|%d|%s", state, me(r), kind, scope, conn, name) + "|" + strconv.FormatInt(time.Now().Unix(), 10)
	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.sign(payload),
		Path: "/api/v1/connections/oauth", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 900})
	q := url.Values{"client_id": {app.ClientID}, "redirect_uri": {s.oauthRedirect(r)}, "response_type": {"code"}, "scope": {app.Scopes}, "state": {state}}
	for k, v := range app.Extra {
		q[k] = v
	}
	target := app.AuthURL + "?" + q.Encode()
	if r.URL.Query().Get("json") != "" {
		writeJSON(w, http.StatusOK, map[string]string{"url": target})
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	back := func(id int64, msg string) {
		http.SetCookie(w, &http.Cookie{Name: oauthCookie, Path: "/api/v1/connections/oauth", MaxAge: -1, HttpOnly: true, Secure: s.cfg.SecureCookies})
		q := url.Values{}
		if id > 0 {
			q.Set("connected", strconv.FormatInt(id, 10))
		}
		if msg != "" {
			q.Set("error", msg)
		}
		http.Redirect(w, r, s.baseURL(r)+"/places?"+q.Encode(), http.StatusFound)
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		back(0, "the sign-in did not finish: "+firstNonEmpty(q.Get("error_description"), e))
		return
	}
	c, err := r.Cookie(oauthCookie)
	if err != nil {
		back(0, "the sign-in took too long or started in another browser — try again")
		return
	}
	val, sig, _ := strings.Cut(c.Value, ".")
	raw, derr := base64.RawURLEncoding.DecodeString(val)
	parts := strings.Split(string(raw), "|")
	if derr != nil || sig == "" || s.sign(string(raw)) != sig || len(parts) != 7 {
		back(0, "the sign-in could not be verified — try again")
		return
	}
	uid, _ := strconv.ParseInt(parts[1], 10, 64)
	kind, scope, name := parts[2], parts[3], parts[5]
	connID, _ := strconv.ParseInt(parts[4], 10, 64)
	at, _ := strconv.ParseInt(parts[6], 10, 64)
	if parts[0] != q.Get("state") || uid != me(r) || time.Since(time.Unix(at, 0)) > 15*time.Minute {
		back(0, "the sign-in could not be verified — try again")
		return
	}
	app := s.oauthApp(kind)
	if app == nil {
		back(0, connName(kind)+" is not set up on this box")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	tok, err := app.exchange(ctx, s.http, q.Get("code"), s.oauthRedirect(r))
	if err != nil {
		back(0, err.Error())
		return
	}
	if tok.RefreshToken == "" {
		back(0, connName(kind)+" gave no lasting sign-in — remove Pocket Backups from the account's connected apps and connect again")
		return
	}
	tb, _ := json.Marshal(tok)
	conn := &store.Connection{Kind: kind, Name: name}
	if connID > 0 {
		if conn, err = s.store.GetConnection(ctx, connID); err != nil || conn.Kind != kind {
			back(0, "that connection is gone")
			return
		}
	}
	sec, _ := s.connSecret(conn)
	sec.Token, sec.ClientID, sec.ClientSecret = string(tb), app.ClientID, app.ClientSecret
	if kind == "gdrive" {
		conn.Config.Scope = scope
	}
	if kind == "onedrive" {
		if err := s.onedriveDrive(ctx, tok.AccessToken, conn); err != nil {
			back(0, err.Error())
			return
		}
	}
	if conn.Secret, err = s.sealConnSecret(sec); err != nil {
		back(0, "could not store the sign-in")
		return
	}
	if err := s.store.SaveConnection(ctx, conn); err != nil {
		back(0, err.Error())
		return
	}
	_ = s.syncRclone(ctx)
	_ = s.testConnection(ctx, conn)
	s.touch()
	back(conn.ID, "")
}

// onedriveDrive finds the account's drive (rclone needs its id and type).
func (s *Server) onedriveDrive(ctx context.Context, access string, c *store.Connection) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.microsoft.com/v1.0/me/drive", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	res, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var d struct {
		ID        string `json:"id"`
		DriveType string `json:"driveType"`
		Owner     struct {
			User struct {
				DisplayName string `json:"displayName"`
			} `json:"user"`
		} `json:"owner"`
	}
	if err := json.NewDecoder(res.Body).Decode(&d); err != nil || d.ID == "" {
		return errors.New("could not find the OneDrive of this account")
	}
	if c.Config.Options == nil {
		c.Config.Options = map[string]string{}
	}
	c.Config.Options["drive_id"], c.Config.Options["drive_type"] = d.ID, d.DriveType
	c.Config.Account = d.Owner.User.DisplayName
	return nil
}
