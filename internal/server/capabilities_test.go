package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/config"
	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// fakeCapCore is a Pocket Core that vouches for link tokens
// pcx_<who>_<search|day|widget|read> from Pocket Hub (who@example.com) and
// remembers the manifest Pocket Backups registers with.
type fakeCapCore struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
}

func newCapHarness(t *testing.T) (*harness, *Server, *fakeCapCore) {
	t.Helper()
	fc := &fakeCapCore{}
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"read": "backups:read", "jobs": "jobs:read"}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fc.mu.Lock()
			fc.manifest = m
			fc.mu.Unlock()
			io.WriteString(w, `{}`)
		case "/api/v1/auth/introspect":
			var in struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			if len(parts) != 3 || parts[0] != "pcx" || scopes[parts[2]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			email := parts[1] + "@example.com"
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "audience": "backups", "scope": scopes[parts[2]],
				"user": map[string]any{"id": 1, "sub": email, "email": email, "name": parts[1], "role": "member"},
				"app":  map[string]any{"id": 9, "slug": "hub", "name": "Pocket Hub"}})
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(fc.Close)
	h, s := newHarnessWith(t, func(c *config.Config) { c.CoreURL, c.CoreRegistrationKey = fc.URL, "key" })
	return h, s, fc
}

// capItems are the rows of a capability answer.
func capItems(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	l, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("no items list in %v", m)
	}
	out := []map[string]any{}
	for _, v := range l {
		out = append(out, v.(map[string]any))
	}
	return out
}

func (h *harness) get(tok, path string) map[string]any {
	h.t.Helper()
	var m map[string]any
	if code := h.do(tok, "GET", path, nil, &m); code != 200 {
		h.t.Fatalf("GET %s = %d %v", path, code, m)
	}
	return m
}

// addJob makes a job (no repository: nothing here runs it).
func addJob(t *testing.T, s *Server, name, schedule string, enabled bool, sources ...int64) int64 {
	t.Helper()
	j := &store.Job{Name: name, Kind: "backup", Engine: "pocket", Schedule: schedule, Enabled: enabled, SourceIDs: sources}
	if err := s.store.SaveJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	return j.ID
}

// addRun records a finished run of a job (status "running" leaves it open).
func addRun(t *testing.T, s *Server, job int64, kind, status string, started time.Time, took time.Duration, added, total int64, errMsg string) int64 {
	t.Helper()
	finished := ""
	if status != "running" {
		finished = started.Add(took).UTC().Format(time.RFC3339)
	}
	res, err := s.store.DB.Exec(`INSERT INTO runs (job_id, kind, trigger, status, title, started_at, finished_at, duration_ms, bytes_added, bytes_total, error, steps)
		VALUES (?, ?, 'schedule', ?, (SELECT name FROM jobs WHERE id = ?), ?, ?, ?, ?, ?, ?, '[]')`,
		job, kind, status, job, started.UTC().Format(time.RFC3339Nano), finished, took.Milliseconds(), added, total, errMsg)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// neverNow is a daily schedule twelve hours away, so the scheduler does not
// start the job during a test.
func neverNow() string { return fmt.Sprintf("0 %d * * *", (time.Now().Hour()+12)%24) }

const gib = 1 << 30

func TestPocketSearch(t *testing.T) {
	h, s := newHarness(t)
	admin := h.user(s, "owner@example.com", "admin")
	member := h.user(s, "member@example.com", "member")
	ctx := context.Background()
	db := &store.Source{Name: "Nextcloud database", Kind: "postgres", Enabled: true, Project: "nextcloud"}
	files := &store.Source{Name: "Photos folder", Kind: "paths", Enabled: true, Project: "immich"}
	for _, x := range []*store.Source{db, files} {
		if err := s.store.SaveSource(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	s.store.SetSourceResult(ctx, db.ID, true, "", 300<<20, time.Second)
	nightly := addJob(t, s, "Nightly", "15 3 * * *", true, db.ID)
	addJob(t, s, "Nextcloud to the cloud", "", true)
	ran := time.Now().Add(-2 * time.Hour)
	addRun(t, s, nightly, "backup", "success", ran, 12*time.Minute, 1288490189, 45*gib, "")

	all := capItems(t, h.get(admin, "/pocket/search?q=nightly"))
	if len(all) != 1 {
		t.Fatalf("nightly: %v", all)
	}
	want := "Every day at 03:15 · Done " + whenWords(ran.Add(12*time.Minute), time.Now(), true) + " · 45.0 GB"
	if j := all[0]; j["id"] != "job:"+strconv.FormatInt(nightly, 10) || j["kind"] != "job" || j["title"] != "Nightly" || j["url"] != "/jobs/"+strconv.FormatInt(nightly, 10) ||
		j["snippet"] != want {
		t.Fatalf("job: %v (want snippet %q)", j, want)
	}
	// jobs first, then sources; a name that starts with the words before one that has them
	got := capItems(t, h.get(admin, "/pocket/search?q=nextcloud"))
	if len(got) != 2 || got[0]["title"] != "Nextcloud to the cloud" || got[0]["snippet"] != "Only when started by hand · Has not run yet" ||
		got[1]["title"] != "Nextcloud database" || got[1]["kind"] != "source" || got[1]["url"] != "/sources" || got[1]["id"] != "source:"+strconv.FormatInt(db.ID, 10) ||
		!strings.HasPrefix(got[1]["snippet"].(string), "Every day at 03:15 · Done ") || !strings.HasSuffix(got[1]["snippet"].(string), " · 300 MB") {
		t.Fatalf("nextcloud: %v", got)
	}
	// a source found by its app, in no job, never backed up
	if got := capItems(t, h.get(admin, "/pocket/search?q=immich")); len(got) != 1 || got[0]["title"] != "Photos folder" || got[0]["snippet"] != "In no job · Not backed up yet" {
		t.Fatalf("immich: %v", got)
	}
	if got := capItems(t, h.get(admin, "/pocket/search?q=nextcloud&limit=1")); len(got) != 1 || got[0]["kind"] != "job" {
		t.Fatalf("limit 1: %v", got)
	}
	for _, q := range []string{"", "%3F%3F", "*", "nothing-like-this"} {
		if got := capItems(t, h.get(admin, "/pocket/search?q="+q)); len(got) != 0 {
			t.Fatalf("q=%q: %v", q, got)
		}
	}
	// backups are an admin's: a member finds nothing
	for _, q := range []string{"nightly", "nextcloud", "immich"} {
		if got := capItems(t, h.get(member, "/pocket/search?q="+q)); len(got) != 0 {
			t.Fatalf("a member finds %q: %v", q, got)
		}
	}
	if code := h.do("", "GET", "/pocket/search?q=nightly", nil, nil); code != 401 {
		t.Fatalf("without signing in: %d", code)
	}
}

func TestPocketDay(t *testing.T) {
	h, s := newHarness(t)
	admin := h.user(s, "owner@example.com", "admin")
	member := h.user(s, "member@example.com", "member")
	nightly := addJob(t, s, "Nightly", neverNow(), true)
	usb := addJob(t, s, "USB backup", "", true)
	sar, _ := time.LoadLocation("Europe/Sarajevo")
	at := func(d, hm string) time.Time {
		t0, err := time.ParseInLocation("2006-01-02 15:04", d+" "+hm, sar)
		if err != nil {
			t.Fatal(err)
		}
		return t0
	}
	ok := addRun(t, s, nightly, "backup", "success", at("2026-11-20", "03:00"), 12*time.Minute, 1288490189, 40*gib, "")
	bad := addRun(t, s, usb, "backup", "failed", at("2026-11-20", "21:30"), 90*time.Second, 0, 0, "the disk is not plugged in")
	addRun(t, s, nightly, "check", "success", at("2026-11-20", "05:00"), time.Minute, 0, 0, "")    // not a backup
	addRun(t, s, nightly, "backup", "skipped", at("2026-11-20", "06:00"), 0, 0, 0, "")             // nothing happened
	addRun(t, s, nightly, "backup", "success", at("2026-11-21", "00:10"), time.Minute, gib, 0, "") // the next day
	addRun(t, s, nightly, "backup", "success", at("2026-11-19", "23:50"), time.Minute, gib, 0, "") // the day before

	day := h.get(admin, "/pocket/day/2026-11-20?tz=Europe/Sarajevo")
	items := capItems(t, day)
	if day["date"] != "2026-11-20" || day["summary"] != "2 backups: 1 succeeded, 1 failed, 1.2 GB added" || len(items) != 2 {
		t.Fatalf("day: %v", day)
	}
	if m := day["metrics"].([]any); len(m) != 1 || m[0].(map[string]any)["value"] != "1.2 GB" {
		t.Fatalf("metrics: %v", day["metrics"])
	}
	first, second := items[0], items[1]
	if first["id"] != strconv.FormatInt(ok, 10) || first["time"] != "03:00" || first["end"] != "03:12" || first["minutes"] != 12.0 || first["kind"] != "succeeded" ||
		first["title"] != "Backup succeeded" || first["sub"] != "Nightly · 1.2 GB added" || first["done"] != true || first["url"] != "/runs/"+strconv.FormatInt(ok, 10) {
		t.Fatalf("the good run: %v", first)
	}
	if second["id"] != strconv.FormatInt(bad, 10) || second["time"] != "21:30" || second["minutes"] != 1.5 || second["kind"] != "failed" ||
		second["title"] != "Backup failed" || second["sub"] != "USB backup" || second["note"] != "the disk is not plugged in" || second["done"] != nil {
		t.Fatalf("the failed run: %v", second)
	}
	// one run: the line names the job; the day is the box's without tz
	if d := h.get(admin, "/pocket/day/2026-11-21?tz=Europe/Sarajevo"); d["summary"] != "Nightly backup succeeded, 1.0 GB added" {
		t.Fatalf("one run: %v", d)
	}
	if d := h.get(admin, "/pocket/day/2019-03-04"); d["summary"] != nil || len(capItems(t, d)) != 0 {
		t.Fatalf("an empty day: %v", d)
	}
	// a member's day has no backups in it
	if d := h.get(member, "/pocket/day/2026-11-20?tz=Europe/Sarajevo"); d["summary"] != nil || len(capItems(t, d)) != 0 {
		t.Fatalf("a member's day: %v", d)
	}
	if code := h.do(admin, "GET", "/pocket/day/yesterday", nil, nil); code != 400 {
		t.Fatalf("a bad day: %d", code)
	}
}

func TestDaySummaryWords(t *testing.T) {
	run := func(job, kind, status string) store.Run { return store.Run{JobName: job, Kind: kind, Status: status} }
	for _, c := range []struct {
		runs  []store.Run
		added int64
		want  string
	}{
		{[]store.Run{run("Nightly", "backup", "success")}, 1288490189, "Nightly backup succeeded, 1.2 GB added"},
		{[]store.Run{run("USB backup", "backup", "failed")}, 0, "USB backup failed"},
		{[]store.Run{run("Drive", "pull", "success")}, 0, "Drive cloud pull succeeded"},
		{[]store.Run{run("", "backup", "cancelled")}, 0, "Backup stopped"},
		{[]store.Run{run("A", "backup", "success"), run("B", "backup", "warning"), run("C", "backup", "success")}, 3 * gib, "3 backups succeeded, 3.0 GB added"},
		{[]store.Run{run("A", "backup", "failed"), run("B", "backup", "failed")}, 0, "2 backups failed"},
	} {
		counts := map[string]int{}
		for _, r := range c.runs {
			counts[dayStatus(r.Status)]++
		}
		if got := daySummary(c.runs, counts, c.added); got != c.want {
			t.Errorf("%v → %q, want %q", c.runs, got, c.want)
		}
	}
}

func TestWhenAndSizeWords(t *testing.T) {
	loc := time.Local
	now := time.Date(2026, 11, 27, 9, 30, 0, 0, loc)
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 11, 27, 3, 12, 0, 0, loc), "last night at 03:12"},
		{time.Date(2026, 11, 27, 8, 0, 0, 0, loc), "today at 08:00"},
		{time.Date(2026, 11, 26, 22, 0, 0, 0, loc), "last night at 22:00"},
		{time.Date(2026, 11, 26, 14, 0, 0, 0, loc), "yesterday at 14:00"},
		{time.Date(2026, 11, 24, 3, 0, 0, 0, loc), "on Tue 24 Nov at 03:00"},
		{time.Date(2025, 12, 31, 3, 0, 0, 0, loc), "on Wed 31 Dec 2025 at 03:00"},
	} {
		if got := whenWords(c.t, now, true); got != c.want {
			t.Errorf("%v → %q, want %q", c.t, got, c.want)
		}
	}
	if got := whenWords(time.Date(2026, 11, 27, 3, 12, 0, 0, loc), now, false); got != "last night" {
		t.Errorf("without the clock: %q", got)
	}
	for n, want := range map[int64]string{0: "0 B", 1536: "1.5 KB", 300 << 20: "300 MB", 1288490189: "1.2 GB", 15 * gib: "15.0 GB"} {
		if got := sizeWords(n); got != want {
			t.Errorf("sizeWords(%d) = %q, want %q", n, got, want)
		}
	}
	for c, want := range map[string]string{"": "Only when started by hand", "15 3 * * *": "Every day at 03:15", "0 4 * * 0": "Every Sunday at 04:00",
		"30 * * * *": "Every hour at :30", "0 2 1 * *": "On day 1 of every month at 02:00", "*/5 * * * *": "*/5 * * * *"} {
		if got := scheduleWords(c); got != want {
			t.Errorf("scheduleWords(%q) = %q, want %q", c, got, want)
		}
	}
}

func TestPocketWidget(t *testing.T) {
	h, s := newHarness(t)
	admin := h.user(s, "owner@example.com", "admin")
	member := h.user(s, "member@example.com", "member")
	// nothing set up: nothing to show
	if w := h.get(admin, "/pocket/widget"); w["line"] != nil || len(capItems(t, w)) != 0 {
		t.Fatalf("without jobs: %v", w)
	}
	if w := h.get(member, "/pocket/widget"); w["line"] != nil || len(capItems(t, w)) != 0 {
		t.Fatalf("without jobs, a member: %v", w)
	}
	now := time.Now()
	photos := addJob(t, s, "Photos", "", true)
	usb := addJob(t, s, "USB disk", neverNow(), true)
	nightly := addJob(t, s, "Nightly", "", true)
	addJob(t, s, "Old", "", false)
	good := now.Add(-3 * time.Hour)
	addRun(t, s, photos, "backup", "success", good, 10*time.Minute, 2*gib, 0, "")
	addRun(t, s, usb, "backup", "success", now.Add(-5*24*time.Hour), time.Minute, 0, 0, "")
	failed := now.Add(-time.Hour)
	addRun(t, s, nightly, "backup", "failed", failed, time.Minute, 0, 0, "repository locked")

	w := h.get(admin, "/pocket/widget")
	want := "Nightly failed " + whenWords(failed.Add(time.Minute), now, false) + ", USB disk is overdue"
	if w["line"] != want || w["url"] != "/jobs" {
		t.Fatalf("widget: %v (want line %q)", w, want)
	}
	items := capItems(t, w)
	if len(items) != 3 || items[0]["title"] != "Nightly" || items[1]["title"] != "USB disk" || items[2]["title"] != "Photos" {
		t.Fatalf("failed first, then overdue, then fine, nothing that is off: %v", items)
	}
	if items[0]["detail"] != "Failed "+whenWords(failed.Add(time.Minute), now, true) || items[0]["url"] != "/jobs/"+strconv.FormatInt(nightly, 10) ||
		!strings.HasPrefix(items[1]["detail"].(string), "Overdue: no good run since ") ||
		items[2]["detail"] != "Done "+whenWords(good.Add(10*time.Minute), now, true)+" · 2.0 GB added" || items[2]["done"] != true {
		t.Fatalf("details: %v", items)
	}
	if m := w["metrics"].([]any); len(m) != 2 || m[0].(map[string]any)["value"] != "3" || m[1].(map[string]any)["label"] != "Failed" {
		t.Fatalf("metrics: %v", w["metrics"])
	}
	// a member sees how it stands, without names
	if wm := h.get(member, "/pocket/widget"); !strings.HasPrefix(fmt.Sprint(wm["line"]), "Backups need attention: ") || len(capItems(t, wm)) != 0 ||
		strings.Contains(fmt.Sprint(wm), "Nightly") {
		t.Fatalf("a member's widget: %v", wm)
	}

	// all good again
	addRun(t, s, nightly, "backup", "success", now.Add(-30*time.Minute), time.Minute, 0, 0, "")
	addRun(t, s, usb, "backup", "success", now.Add(-20*time.Minute), time.Minute, 0, 0, "")
	last := now.Add(-19 * time.Minute)
	fine := "All backups fine, last at " + last.Format("15:04")
	if when := whenWords(last, now, true); !strings.HasPrefix(when, "today") && !strings.HasPrefix(when, "last night") {
		fine = "All backups fine, last " + when
	}
	if w := h.get(admin, "/pocket/widget"); w["line"] != fine {
		t.Fatalf("all fine: %v (want %q)", w["line"], fine)
	}
	if w := h.get(member, "/pocket/widget"); w["line"] != fine || len(capItems(t, w)) != 0 {
		t.Fatalf("all fine, a member: %v", w)
	}
}

// A link token with one pocket:* scope reaches its one route and nothing else.
func TestPocketLinkTokens(t *testing.T) {
	h, s, fc := newCapHarness(t)
	h.user(s, "owner@example.com", "admin")
	h.user(s, "member@example.com", "member")
	nightly := addJob(t, s, "Nightly", neverNow(), true)
	addRun(t, s, nightly, "backup", "success", time.Now().Add(-time.Hour), time.Minute, gib, 0, "")
	today := time.Now().Format("2006-01-02")
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{"pcx_owner_search", "GET", "/pocket/search?q=nightly", 200},
		{"pcx_owner_day", "GET", "/pocket/day/" + today, 200},
		{"pcx_owner_widget", "GET", "/pocket/widget", 200},
		{"pcx_owner_widget", "GET", "/auth/me", 200},
		// another capability's route
		{"pcx_owner_search", "GET", "/pocket/day/" + today, 403},
		{"pcx_owner_search", "GET", "/pocket/widget", 403},
		{"pcx_owner_day", "GET", "/pocket/search?q=nightly", 403},
		{"pcx_owner_day", "GET", "/pocket/widget", 403},
		{"pcx_owner_widget", "GET", "/pocket/search?q=nightly", 403},
		{"pcx_owner_widget", "GET", "/pocket/day/" + today, 403},
		// nothing can be kept here: there is no such route
		{"pcx_owner_capture", "POST", "/pocket/capture", 404},
		{"pcx_owner_capture", "GET", "/pocket/widget", 403},
		// the rest of the app, reading and starting things
		{"pcx_owner_widget", "GET", "/overview", 403},
		{"pcx_owner_widget", "GET", "/widget", 403},
		{"pcx_owner_search", "GET", "/jobs", 403},
		{"pcx_owner_search", "GET", "/sources", 403},
		{"pcx_owner_day", "GET", "/runs", 403},
		{"pcx_owner_widget", "POST", "/jobs/" + strconv.FormatInt(nightly, 10) + "/run", 403},
		{"pcx_owner_widget", "POST", "/scheduled-jobs/job-" + strconv.FormatInt(nightly, 10) + "/run", 403},
		{"pcx_owner_search", "POST", "/repositories/1/restore", 403},
		{"pcx_owner_day", "POST", "/repositories/1/action", 403},
		// the app's own scopes do not open the new routes
		{"pcx_owner_read", "GET", "/pocket/search?q=nightly", 403},
		{"pcx_owner_read", "GET", "/pocket/day/" + today, 403},
		{"pcx_owner_read", "GET", "/pocket/widget", 403},
		{"pcx_owner_jobs", "GET", "/pocket/widget", 403},
		{"pcx_owner_read", "GET", "/widget", 200},
		{"pcx_nobody", "GET", "/pocket/widget", 401},
	} {
		if code := h.do(c.tok, c.method, c.path, map[string]any{"request_id": "req-0000000000000001", "text": "x"}, nil); code != c.want {
			t.Errorf("%s %s %s → %d, want %d", c.tok, c.method, c.path, code, c.want)
		}
	}
	// the token acts for its person: the admin sees the backups, a member does not
	if got := capItems(t, h.get("pcx_owner_search", "/pocket/search?q=nightly")); len(got) != 1 || got[0]["title"] != "Nightly" {
		t.Fatalf("the admin through the link: %v", got)
	}
	if got := capItems(t, h.get("pcx_member_search", "/pocket/search?q=nightly")); len(got) != 0 {
		t.Fatalf("a member through the link: %v", got)
	}
	if d := h.get("pcx_owner_day", "/pocket/day/"+today); len(capItems(t, d)) == 0 {
		t.Fatalf("the admin's day through the link: %v", d)
	}
	if d := h.get("pcx_member_day", "/pocket/day/"+today); len(capItems(t, d)) != 0 {
		t.Fatalf("a member's day through the link: %v", d)
	}
	if w := h.get("pcx_owner_widget", "/pocket/widget"); len(capItems(t, w)) != 1 {
		t.Fatalf("the admin's widget through the link: %v", w)
	}
	if w := h.get("pcx_member_widget", "/pocket/widget"); len(capItems(t, w)) != 0 {
		t.Fatalf("a member's widget through the link: %v", w)
	}
	// nothing ran, nothing changed
	if runs, _ := s.store.ListRuns(context.Background(), store.RunFilter{}); len(runs) != 1 {
		t.Fatalf("the links started something: %v", runs)
	}

	// the manifest: category, capabilities and the pocket:* offers
	var m map[string]any
	for i := 0; i < 100 && m == nil; i++ {
		fc.mu.Lock()
		m = fc.manifest
		fc.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	if m == nil {
		t.Fatal("never registered")
	}
	if m["category"] != "System" {
		t.Fatalf("category: %v", m["category"])
	}
	search, _ := m["search"].(map[string]any)
	if search == nil || fmt.Sprint(search["bangs"]) != "[backups backup]" || fmt.Sprint(search["kinds"]) != "[job source]" {
		t.Fatalf("search: %v", m["search"])
	}
	if d, _ := m["day"].(map[string]any); d == nil || d["label"] != "Backups" || d["one"] != "backup" || d["many"] != "backups" {
		t.Fatalf("day: %v", m["day"])
	}
	if w, _ := m["widget"].(map[string]any); w == nil || w["title"] != "Backups" {
		t.Fatalf("widget: %v", m["widget"])
	}
	if m["capture"] != nil {
		t.Fatalf("nothing can be kept here: %v", m["capture"])
	}
	offers := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offers[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{core.ScopeSearch, core.ScopeDay, core.ScopeWidget, "backups:read", "jobs:read", "jobs:run"} {
		if !offers[sc] {
			t.Errorf("the manifest does not offer %s: %v", sc, offers)
		}
	}
	if offers[core.ScopeCapture] {
		t.Error("the manifest offers pocket:capture")
	}
}

func TestPocketLinkScopes(t *testing.T) {
	req := func(m, p string) *http.Request { return httptest.NewRequest(m, p, nil) }
	for _, c := range []struct {
		scope string
		r     *http.Request
		want  bool
	}{
		{core.ScopeSearch, req("GET", "/api/v1/pocket/search"), true},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/2026-11-20"), true},
		{core.ScopeWidget, req("GET", "/api/v1/pocket/widget"), true},
		{core.ScopeCapture, req("POST", "/api/v1/pocket/capture"), false},
		{core.ScopeWidget, req("POST", "/api/v1/pocket/widget"), false},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/2026-11-20/x"), false},
		{"backups:read", req("GET", "/api/v1/pocket/widget"), false},
		{"backups:read", req("GET", "/api/v1/widget"), true},
		{core.ScopeWidget, req("GET", "/api/v1/widget"), false},
	} {
		if got := linkAllows([]string{c.scope}, c.r); got != c.want {
			t.Errorf("%s %s %s → %v, want %v", c.scope, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}
