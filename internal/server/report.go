package server

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/engine"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

// The size report: how much of the newest snapshot each app takes, as a
// small HTML page Homepage can show in an iframe tile, plus the repository
// total in a Homepage greeting line. Both are optional box settings.

var reportAlias = map[string]string{
	"firefly": "firefly-iii", "calibre-metadata": "calibre-web", "calibre-web-app": "calibre-web", "calibre-web-cwa": "calibre-web",
	"backups": "backup scripts", ".config": "systemd units",
}

type reportRow struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// serviceOf names the app a file in the snapshot belongs to.
func serviceOf(path, base, staging string) string {
	if staging != "" && strings.HasPrefix(path, staging+"/") {
		parts := strings.Split(strings.TrimPrefix(path, staging+"/"), "/")
		if len(parts) < 2 {
			return "system state"
		}
		kind, file := parts[0], parts[len(parts)-1]
		switch kind {
		case "system":
			return "system state"
		case "db", "volumes":
			name := strings.SplitN(file, ".", 2)[0]
			name = strings.TrimSuffix(name, "-db")
			if kind == "volumes" {
				name = strings.SplitN(name, "_", 2)[0]
			}
			return alias(name)
		default:
			return alias(kind)
		}
	}
	rel := strings.TrimPrefix(path, strings.TrimRight(base, "/")+"/")
	if rel == path {
		rel = strings.TrimPrefix(path, "/")
	}
	return alias(strings.SplitN(rel, "/", 2)[0])
}

func alias(n string) string {
	if a, ok := reportAlias[n]; ok {
		return a
	}
	return n
}

// reportBase is the folder most of the snapshot's paths sit directly in.
func reportBase(paths []string) string {
	count := map[string]int{}
	best, bestN := "/", 0
	for _, p := range paths {
		d := filepath.Dir(filepath.Clean(p))
		count[d]++
		if count[d] > bestN {
			best, bestN = d, count[d]
		}
	}
	return best
}

func (s *Server) buildReport(ctx context.Context, repo *store.Repository, spec engine.Repo) ([]reportRow, int64, int, error) {
	snaps, err := s.eng.Snapshots(ctx, spec)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(snaps) == 0 {
		return nil, 0, 0, fmt.Errorf("no snapshots yet")
	}
	latest := snaps[len(snaps)-1]
	base := reportBase(latest.Paths)
	staging := ""
	for _, p := range latest.Paths {
		if strings.HasSuffix(strings.TrimRight(p, "/"), "/staging") {
			staging = strings.TrimRight(p, "/")
		}
	}
	sizes := map[string]int64{}
	err = s.eng.LsAll(ctx, spec, latest.ID, func(n engine.Node) {
		if n.Type == "file" {
			sizes[serviceOf(n.Path, base, staging)] += n.Size
		}
	})
	if err != nil {
		return nil, 0, 0, err
	}
	rows := make([]reportRow, 0, len(sizes))
	for k, v := range sizes {
		rows = append(rows, reportRow{k, v})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Bytes > rows[j].Bytes })
	size, err := s.eng.Stats(ctx, spec)
	if err != nil {
		size = repo.Stats.Size
	}
	return rows, size, len(snaps), nil
}

// writeReport regenerates the Homepage report files (when configured).
func (s *Server) writeReport(ctx context.Context, repo *store.Repository, spec engine.Repo) (string, string, error) {
	htmlPath := s.store.BoxSetting(ctx, "report_html_path")
	yamlPath := s.store.BoxSetting(ctx, "report_widgets_yaml")
	if htmlPath == "" && yamlPath == "" {
		return "no report files are set (Settings → Integrations)", "skipped", nil
	}
	if want := s.store.BoxSetting(ctx, "report_repo_id"); want != "" && want != strconv.FormatInt(repo.ID, 10) {
		return "the report follows another repository (Settings → Integrations)", "skipped", nil
	}
	rows, size, count, err := s.buildReport(ctx, repo, spec)
	if err != nil {
		return "", "", err
	}
	var latest int64
	for _, r := range rows {
		latest += r.Bytes
	}
	if htmlPath != "" {
		if err := os.MkdirAll(filepath.Dir(htmlPath), 0o755); err != nil {
			return "", "", err
		}
		if err := os.WriteFile(htmlPath, []byte(reportHTML(rows, latest, size, count)), 0o644); err != nil {
			return "", "", err
		}
	}
	if yamlPath != "" {
		b, err := os.ReadFile(yamlPath)
		if err != nil {
			return "", "", err
		}
		line := fmt.Sprintf("    text: Backup %s MiB · %d snapshots", commaInt(size/1048576), count)
		re := regexp.MustCompile(`(?m)^    text: Backup .*$`)
		if re.Match(b) {
			b = re.ReplaceAll(b, []byte(line))
			if err := os.WriteFile(yamlPath, b, 0o644); err != nil {
				return "", "", err
			}
		}
	}
	_ = s.store.SetBoxSetting(ctx, "report_last", time.Now().UTC().Format(time.RFC3339))
	return fmt.Sprintf("%d apps, newest snapshot %s, repository %s", len(rows), engine.HumanBytes(latest), engine.HumanBytes(size)), "ok", nil
}

func commaInt(n int64) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func reportHTML(rows []reportRow, latest, repo int64, count int) string {
	var max int64 = 1
	if len(rows) > 0 && rows[0].Bytes > 0 {
		max = rows[0].Bytes
	}
	var b strings.Builder
	for _, r := range rows {
		mib := float64(r.Bytes) / 1048576
		val := fmt.Sprintf("%.1f", mib)
		if mib >= 1000 {
			val = commaInt(int64(mib))
		}
		if mib < 0.1 {
			val = "&lt;0.1"
		}
		pct := float64(r.Bytes) / float64(max) * 100
		if pct < 0.75 {
			pct = 0.75
		}
		fmt.Fprintf(&b, `<div class="row"><div class="name">%s</div><div class="track"><div class="bar" style="width:%.2f%%"></div></div><div class="val">%s</div></div>`,
			html.EscapeString(r.Name), pct, val)
	}
	return fmt.Sprintf(`<!doctype html>
<html><head><meta charset="utf-8"><style>
  body { margin:0; padding:10px 14px; background:transparent; font-family:system-ui,-apple-system,sans-serif; color:#f1f5f9; font-size:12px; }
  .head { display:flex; justify-content:space-between; align-items:baseline; margin-bottom:8px; color:#94a3b8; }
  .head b { color:#f1f5f9; font-weight:600; }
  .row { display:grid; grid-template-columns:11em 1fr 4.5em; gap:8px; align-items:center; padding:2px 0; }
  .name { color:#cbd5e1; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
  .track { background:rgba(255,255,255,.06); border-radius:0 4px 4px 0; height:8px; }
  .bar { background:#3987e5; height:8px; border-radius:0 4px 4px 0; }
  .val { text-align:right; color:#94a3b8; font-variant-numeric:tabular-nums; }
</style></head><body>
<div class="head"><span>Latest snapshot <b>%.1f MiB</b> &middot; repo <b>%.1f MiB</b> &middot; <b>%d</b> snapshots</span>
<span>MiB &middot; %s</span></div>
%s
</body></html>`, float64(latest)/1048576, float64(repo)/1048576, count, time.Now().Format("2006-01-02 15:04"), b.String())
}

// handleReport returns the size report of a repository's newest snapshot.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	repo, err := s.store.GetRepository(ctx, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "repository")
		return
	}
	if cached, ok := s.reportCache.Load(repo.ID); ok {
		c := cached.(cachedReport)
		if time.Since(c.at) < 30*time.Minute && !queryBool(r, "fresh") {
			writeJSON(w, http.StatusOK, c.body)
			return
		}
	}
	spec, err := s.repoSpec(ctx, repo)
	if err != nil {
		s.fail(w, err, "repository")
		return
	}
	if usbOf(repo) != nil && !engine.Mounted(repo.Options.USB.Mountpoint) {
		writeErr(w, http.StatusConflict, "the disk is not mounted")
		return
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	rows, size, count, err := s.buildReport(c, repo, spec)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	body := map[string]any{"rows": rows, "repository_size": size, "snapshots": count, "at": store.Now()}
	s.reportCache.Store(repo.ID, cachedReport{at: time.Now(), body: body})
	writeJSON(w, http.StatusOK, body)
}

type cachedReport struct {
	at   time.Time
	body map[string]any
}
