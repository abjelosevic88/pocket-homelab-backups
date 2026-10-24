package server

import (
	"context"
	"errors"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/config"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core): another
// Pocket app may read here for a person with a pcx_ token, only within these
// scopes. Every route not listed in linkRoutes stays closed to it.

var linkOffers = []core.Offer{
	{Scope: "backups:read", Name: "See whether the box is backed up", Description: "Read the backup status (healthy or not, when the last good backup was, what needs attention) — what Pocket Hub shows as a tile and Pocket Day notes when a backup failed."},
}

// linkUses: Pocket Files is a place backups can be kept.
var linkUses = []core.Use{
	{App: "files", Scopes: []string{"files:read", "files:write"}, Reason: "Keep backups (an encrypted restic repository, or a copy of one) in your Pocket Files"},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(overview|widget|changes)$`), "backups:read"},
}

var errLinkScope = errors.New("outside the link's scope")

func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token and maps it to the person it acts for.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" {
		return nil, http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, errLinkScope
	}
	s.linkApps.Store(tok, in.App)
	return s.coreIdentityUser(ctx, in)
}

func (s *Server) userEmail(ctx context.Context, uid int64) string {
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		return u.Email
	}
	return ""
}

// LinkToken returns a fresh Pocket Files link token for a pocketfiles
// connection, acting for the person who connected it.
func LinkToken(ctx context.Context, cfg *config.Config, st *store.Store, id string) (string, error) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "", errors.New("connection id must be a number")
	}
	c, err := st.GetConnection(ctx, n)
	if err != nil {
		return "", err
	}
	if c.Kind != "pocketfiles" || c.ActingUser == nil {
		return "", errors.New("not a Pocket Files connection")
	}
	u, err := st.GetUser(ctx, *c.ActingUser)
	if err != nil {
		return "", errors.New("the person who connected Pocket Files is gone")
	}
	cl := core.New(cfg.CoreURL, core.Options{TokenFile: cfg.DataDir + "/core.token"})
	lt, err := cl.ActAs(ctx, u.Email, "files", "files:read", "files:write")
	if err != nil {
		return "", err
	}
	return lt.AccessToken, nil
}
