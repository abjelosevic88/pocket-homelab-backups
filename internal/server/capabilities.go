package server

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// What Pocket Search, Pocket Day and Pocket Hub ask Pocket Backups (the
// capabilities value in links.go): one route each, in the shape every Pocket
// app answers in. They read what /jobs, /sources, /runs and /overview read and
// change nothing: no route here starts a backup, a restore or a prune, or
// touches a job. Backups are the box's, so only an admin gets jobs, sources
// and runs; for anyone else search and day are empty and the widget says
// what /overview already tells them (healthy or not, when the last good
// backup was), without names.

// runWords are the words the pages use for a run's outcome (web/src/lib/format.ts RUN_LABEL).
var runWords = map[string]string{"running": "Running", "success": "Done", "warning": "Done with warnings", "failed": "Failed", "cancelled": "Stopped", "skipped": "Skipped"}

// dayRunKinds are the runs that are a backup for Pocket Day and Hub (as in publishRun).
const dayRunKinds = "backup,post,pull"

func isAdmin(r *http.Request) bool {
	u := userFrom(r.Context())
	return u != nil && u.Role == "admin"
}

// hasWord says whether there is a letter or a digit to look for: "??" and
// "*" are not words, and would match everything by accident.
func hasWord(q string) bool {
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// dots joins the parts of a line that are there: "Every day at 03:00 · 1.2 GB".
func dots(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// sizeWords is a size as the pages write it: 1536 → "1.5 KB" (web/src/lib/format.ts fmtBytes).
func sizeWords(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	v, i := math.Abs(float64(n)), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	d := 1
	if i == 0 || v >= 100 {
		d = 0
	}
	sign := ""
	if n < 0 {
		sign = "-"
	}
	return sign + strconv.FormatFloat(v, 'f', d, 64) + " " + units[i]
}

// scheduleWords is a schedule in words, as the pages write it (web/src/lib/format.ts
// cronText): "Every day at 04:15"; anything else stays the line itself.
func scheduleWords(c string) string {
	s := strings.TrimSpace(c)
	if s == "" {
		return "Only when started by hand"
	}
	short := map[string]string{"@hourly": "Every hour", "@daily": "Every day at 00:00", "@midnight": "Every day at 00:00", "@weekly": "Every Sunday at 00:00", "@monthly": "On the 1st of every month at 00:00"}
	if w, ok := short[s]; ok {
		return w
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return s
	}
	num := func(v string) (int, bool) {
		n, err := strconv.Atoi(v)
		return n, err == nil && n >= 0
	}
	mi, h, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	m, ok := num(mi)
	if !ok {
		return s
	}
	if h == "*" && dom == "*" && mon == "*" && dow == "*" {
		return fmt.Sprintf("Every hour at :%02d", m)
	}
	hh, ok := num(h)
	if !ok {
		return s
	}
	at := fmt.Sprintf("%02d:%02d", hh, m)
	if dom == "*" && mon == "*" && dow == "*" {
		return "Every day at " + at
	}
	if d, ok := num(dow); ok && len(dow) == 1 && dom == "*" && mon == "*" {
		return "Every " + weekdays[d%7] + " at " + at
	}
	if _, ok := num(dom); ok && mon == "*" && dow == "*" {
		return "On day " + dom + " of every month at " + at
	}
	return s
}

// jobWhen is when a job runs, in words (web/src/lib/format.ts jobWhen).
func jobWhen(j *store.Job) string {
	if !j.Enabled {
		return "Off"
	}
	if j.Schedule == "" && j.Config.RunOnConnect {
		return "When its disk is plugged in"
	}
	return scheduleWords(j.Schedule)
}

// whenWords says when something happened, next to now, in the box's time:
// "last night at 03:12", "today at 10:20", "yesterday at 14:00", "on Tue 21
// Oct at 06:15" (web/src/lib/format.ts friendlyTime). Without the clock when
// clock is false.
func whenWords(t, now time.Time, clock bool) string {
	t, now = t.In(time.Local), now.In(time.Local)
	at := ""
	if clock {
		at = " at " + t.Format("15:04")
	}
	day := func(x time.Time) string { return x.Format("2006-01-02") }
	switch {
	case day(t) == day(now) && t.Hour() < 7 && now.Hour() >= 7:
		return "last night" + at
	case day(t) == day(now):
		return "today" + at
	case day(t) == day(now.AddDate(0, 0, -1)) && t.Hour() >= 20:
		return "last night" + at
	case day(t) == day(now.AddDate(0, 0, -1)):
		return "yesterday" + at
	case t.Year() != now.Year():
		return "on " + t.Format("Mon 2 Jan 2006") + at
	}
	return "on " + t.Format("Mon 2 Jan") + at
}

// runTime is when a run ended (or started, while it runs).
func runTime(r *store.Run) time.Time {
	return store.ParseTime(firstNonEmpty(r.FinishedAt, r.StartedAt))
}

// ---------- search ----------

// pocketSearch answers Pocket Search: the jobs and the sources whose name
// (or, for a source, its app) has the words, jobs first, names that start
// with them before names that only contain them. Snapshots and runs are left
// out: listing snapshots means reading every repository with restic, and a
// run is named after its job, which is already found.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	if !isAdmin(r) || !hasWord(q) {
		return nil, nil
	}
	ctx := r.Context()
	low := strings.ToLower(q)
	score := func(name string) float64 {
		n := strings.ToLower(name)
		switch {
		case n == low:
			return 4
		case strings.HasPrefix(n, low):
			return 3
		case strings.Contains(n, low):
			return 2
		}
		return 0
	}
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := []core.SearchItem{}
	byID := map[int64]*store.Job{}
	for i := range jobs {
		j := &jobs[i]
		byID[j.ID] = j
		sc := score(j.Name)
		if sc == 0 {
			continue
		}
		v := s.jobView(ctx, *j)
		last, size, date := "Has not run yet", "", ""
		if v.Last != nil {
			last = runWords[v.Last.Status] + " " + whenWords(runTime(v.Last), now, true)
			date = firstNonEmpty(v.Last.FinishedAt, v.Last.StartedAt)
		}
		if v.LastGood != nil && v.LastGood.BytesTotal > 0 {
			size = sizeWords(v.LastGood.BytesTotal)
		}
		out = append(out, core.SearchItem{ID: "job:" + strconv.FormatInt(j.ID, 10), Title: j.Name, Kind: "job", Date: date,
			URL: "/jobs/" + strconv.FormatInt(j.ID, 10), Snippet: dots(jobWhen(j), last, size), Score: sc + 0.5})
	}
	sources, err := s.store.ListSources(ctx)
	if err != nil {
		return nil, err
	}
	for i := range sources {
		x := &sources[i]
		sc := score(x.Name)
		if sc == 0 && x.Project != "" && strings.Contains(strings.ToLower(x.Project), low) {
			sc = 1
		}
		if sc == 0 {
			continue
		}
		// when: the schedule of the first job it is in that is on
		when := "In no job"
		if len(x.Jobs) > 0 {
			when = "Off"
			for _, id := range x.Jobs {
				if j := byID[id]; j != nil && j.Enabled {
					when = jobWhen(j)
					break
				}
			}
		}
		if !x.Enabled {
			when = "Off"
		}
		last, size := "Not backed up yet", ""
		switch {
		case x.LastError != "":
			last = "Failed"
		case x.LastOKAt != "":
			last = "Done " + whenWords(store.ParseTime(x.LastOKAt), now, true)
		}
		if x.LastSize > 0 {
			size = sizeWords(x.LastSize)
		}
		out = append(out, core.SearchItem{ID: "source:" + strconv.FormatInt(x.ID, 10), Title: x.Name, Kind: "source", Date: x.LastOKAt,
			URL: "/sources", Snippet: dots(when, last, size), Score: sc})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- day ----------

// runKindWord is what a run was, for a line: "backup", "cloud pull".
func runKindWord(kind string) string {
	if kind == "pull" {
		return "cloud pull"
	}
	return "backup"
}

// dayStatus is a run's outcome as a word of the day: succeeded, failed,
// stopped or running ("" for a run that was skipped: nothing happened).
func dayStatus(status string) string {
	switch status {
	case "success", "warning":
		return "succeeded"
	case "failed":
		return "failed"
	case "cancelled":
		return "stopped"
	case "running":
		return "running"
	}
	return ""
}

// pocketDay answers Pocket Day: the backups (and cloud pulls) that started
// on the day, as /runs lists them, each with how it went and how much it
// added. The day is the person's when the caller says their time zone, else
// the box's. Backrest's own log is not read: a backup Backrest ran through
// this app's hooks is already a run here.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	if !isAdmin(r) {
		return nil, nil
	}
	if tz == nil {
		tz = time.Local
	}
	start, err := time.ParseInLocation("2006-01-02", date, tz)
	if err != nil {
		return nil, err
	}
	runs, err := s.store.RunsStarted(r.Context(), start, start.AddDate(0, 0, 1), dayRunKinds)
	if err != nil {
		return nil, err
	}
	day := &core.Day{Date: date}
	counts := map[string]int{}
	var added int64
	var shown []store.Run
	for _, run := range runs {
		st := dayStatus(run.Status)
		if st == "" {
			continue
		}
		counts[st]++
		shown = append(shown, run)
		job := firstNonEmpty(run.JobName, run.Title)
		what := runKindWord(run.Kind)
		it := core.DayItem{ID: strconv.FormatInt(run.ID, 10), Kind: st, Title: strings.ToUpper(what[:1]) + what[1:] + " " + st,
			URL: "/runs/" + strconv.FormatInt(run.ID, 10), Done: st == "succeeded"}
		if run.Status == "warning" {
			it.Title += " with warnings"
		}
		size := ""
		if st == "succeeded" && run.BytesAdded > 0 {
			size = sizeWords(run.BytesAdded) + " added"
			added += run.BytesAdded
		}
		it.Sub = dots(job, size)
		if st == "failed" {
			it.Note = run.Error
		}
		began := store.ParseTime(run.StartedAt).In(tz)
		it.Time = began.Format("15:04")
		if end := store.ParseTime(run.FinishedAt); !end.IsZero() && end.In(tz).Format("2006-01-02") == date {
			it.End = end.In(tz).Format("15:04")
		}
		if run.DurationMS > 0 {
			it.Minutes = math.Round(float64(run.DurationMS)/6000) / 10
		}
		day.Items = append(day.Items, it)
	}
	if len(shown) == 0 {
		return nil, nil
	}
	day.Summary = daySummary(shown, counts, added)
	if added > 0 {
		day.Metrics = []core.Metric{{Label: "Added", Value: sizeWords(added)}}
	}
	return day, nil
}

// daySummary is the day in one line: "Nightly backup succeeded, 1.2 GB
// added", "3 backups succeeded, 2.4 GB added", "3 backups: 2 succeeded, 1
// failed, 2.4 GB added".
func daySummary(runs []store.Run, counts map[string]int, added int64) string {
	size := ""
	if added > 0 {
		size = ", " + sizeWords(added) + " added"
	}
	if len(runs) == 1 {
		run := runs[0]
		what := runKindWord(run.Kind)
		name := strings.TrimSpace(run.JobName)
		switch {
		case name == "":
			name = strings.ToUpper(what[:1]) + what[1:]
		case !strings.Contains(strings.ToLower(name), what):
			name += " " + what
		}
		return name + " " + dayStatus(run.Status) + size
	}
	n := strconv.Itoa(len(runs)) + " backups"
	var parts []string
	for _, st := range []string{"succeeded", "failed", "stopped", "running"} {
		if counts[st] == len(runs) {
			return n + " " + st + size
		}
		if counts[st] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
		}
	}
	return n + ": " + strings.Join(parts, ", ") + size
}

// ---------- widget ----------

// pocketWidget answers Pocket Hub: how each backup job that is on went last
// time, the failed ones first, then the overdue ones (two of their times went
// by without a good run, as /overview counts it), then the rest. Someone who
// is not an admin gets one line: whether the box is fine and when the last
// good backup was, what /overview shows them. The job list is /jobs's own
// (jobs on Backrest add Backrest's newest backup, as /jobs and /widget do).
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx := r.Context()
	jobs, err := s.store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	views := make([]jobView, 0, len(jobs))
	var lastGood time.Time
	for _, j := range jobs {
		v := s.jobView(ctx, j)
		views = append(views, v)
		if v.Enabled && v.LastGood != nil {
			if t := store.ParseTime(v.LastGood.FinishedAt); t.After(lastGood) {
				lastGood = t
			}
		}
	}
	fine := func() string {
		if lastGood.IsZero() {
			return "No backup has run yet"
		}
		when := whenWords(lastGood, now, true)
		if strings.HasPrefix(when, "today") || strings.HasPrefix(when, "last night") {
			when = "at " + lastGood.In(time.Local).Format("15:04")
		}
		return "All backups fine, last " + when
	}
	if !isAdmin(r) {
		health, issues := s.health(ctx, views)
		switch health {
		case "empty":
			return nil, nil
		case "ok":
			return &core.WidgetData{Line: fine()}, nil
		}
		return &core.WidgetData{Line: fmt.Sprintf("Backups need attention: %d %s", len(issues), plural(len(issues), "problem", "problems"))}, nil
	}

	type row struct {
		rank int // 0 failed, 1 overdue, 2 fine
		item core.WidgetItem
	}
	var rows []row
	var failed, late []string // names
	var failedAt time.Time
	for _, v := range views {
		if !v.Enabled {
			continue
		}
		it := core.WidgetItem{ID: strconv.FormatInt(v.ID, 10), Title: v.Name, URL: "/jobs/" + strconv.FormatInt(v.ID, 10)}
		rank := 2
		prev, isLate := overdue(v, now)
		switch {
		case v.Last != nil && v.Last.Status == "failed":
			rank = 0
			failedAt = runTime(v.Last)
			failed = append(failed, v.Name)
			it.Detail = "Failed " + whenWords(failedAt, now, true)
			if v.Running != nil {
				it.Detail += ", running again now"
			}
		case isLate:
			rank = 1
			late = append(late, v.Name)
			it.Detail = "Overdue: no good run since " + strings.TrimPrefix(whenWords(prev, now, true), "on ")
			if v.Running != nil {
				it.Detail += ", running now"
			}
		case v.Running != nil:
			it.Detail = "Running now"
		case v.Last != nil:
			size := ""
			if v.Last.BytesAdded > 0 && v.Last.Status != "cancelled" && v.Last.Status != "skipped" {
				size = sizeWords(v.Last.BytesAdded) + " added"
			}
			it.Detail = dots(runWords[v.Last.Status]+" "+whenWords(runTime(v.Last), now, true), size)
			it.Done = v.Last.Status == "success" || v.Last.Status == "warning"
		default:
			it.Detail = "Has not run yet"
		}
		rows = append(rows, row{rank, it})
	}
	if len(rows) == 0 {
		return nil, nil
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].rank < rows[j].rank })
	d := &core.WidgetData{URL: "/jobs", Items: []core.WidgetItem{}}
	for _, x := range rows {
		d.Items = append(d.Items, x.item)
	}

	var parts []string
	switch len(failed) {
	case 0:
	case 1:
		parts = append(parts, failed[0]+" failed "+whenWords(failedAt, now, false))
	default:
		parts = append(parts, fmt.Sprintf("%d backups failed", len(failed)))
	}
	switch len(late) {
	case 0:
	case 1:
		parts = append(parts, late[0]+" is overdue")
	default:
		parts = append(parts, fmt.Sprintf("%d backups are overdue", len(late)))
	}
	if len(parts) > 0 {
		d.Line = strings.Join(parts, ", ")
	} else {
		d.Line = fine()
	}

	d.Metrics = []core.Metric{{Label: "Jobs", Value: strconv.Itoa(len(rows))}}
	if len(failed) > 0 {
		d.Metrics = append(d.Metrics, core.Metric{Label: "Failed", Value: strconv.Itoa(len(failed))})
	}
	repos, _ := s.store.ListRepositories(ctx)
	var stored int64
	for _, x := range repos {
		stored += x.Stats.Size
	}
	if stored > 0 {
		d.Metrics = append(d.Metrics, core.Metric{Label: "Stored", Value: sizeWords(stored)})
	}
	return d, nil
}
