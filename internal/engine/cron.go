package engine

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five-field cron line (minute hour day month weekday),
// with the usual shortcuts (@daily, @weekly, @monthly, @hourly).
type Cron struct {
	min, hour, dom, mon, dow [64]bool
	domStar, dowStar         bool
}

// ParseCron reads a cron line.
func ParseCron(s string) (*Cron, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "@hourly":
		s = "0 * * * *"
	case "@daily", "@midnight":
		s = "0 0 * * *"
	case "@weekly":
		s = "0 0 * * 0"
	case "@monthly":
		s = "0 0 1 * *"
	case "@yearly", "@annually":
		s = "0 0 1 1 *"
	}
	f := strings.Fields(s)
	if len(f) != 5 {
		return nil, fmt.Errorf("a schedule has five fields: minute hour day month weekday (got %q)", s)
	}
	c := &Cron{domStar: f[2] == "*", dowStar: f[4] == "*"}
	specs := []struct {
		field    string
		set      *[64]bool
		min, max int
	}{{f[0], &c.min, 0, 59}, {f[1], &c.hour, 0, 23}, {f[2], &c.dom, 1, 31}, {f[3], &c.mon, 1, 12}, {strings.ReplaceAll(f[4], "7", "0"), &c.dow, 0, 6}}
	for _, sp := range specs {
		if err := parseField(sp.field, sp.set, sp.min, sp.max); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func parseField(field string, set *[64]bool, lo, hi int) error {
	for _, part := range strings.Split(field, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return fmt.Errorf("bad step in %q", field)
			}
			step, part = n, part[:i]
		}
		a, b := lo, hi
		if part != "*" {
			if i := strings.Index(part, "-"); i >= 0 {
				x, err1 := strconv.Atoi(part[:i])
				y, err2 := strconv.Atoi(part[i+1:])
				if err1 != nil || err2 != nil {
					return fmt.Errorf("bad range in %q", field)
				}
				a, b = x, y
			} else {
				x, err := strconv.Atoi(part)
				if err != nil {
					return fmt.Errorf("bad value in %q", field)
				}
				a, b = x, x
				if step > 1 {
					b = hi
				}
			}
		}
		if a < lo || b > hi || a > b {
			return fmt.Errorf("%q is out of range %d-%d", field, lo, hi)
		}
		for v := a; v <= b; v += step {
			set[v] = true
		}
	}
	return nil
}

func (c *Cron) dayMatches(t time.Time) bool {
	d, w := c.dom[t.Day()], c.dow[int(t.Weekday())]
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return w
	case c.dowStar:
		return d
	default:
		return d || w
	}
}

// Next returns the first time after t the schedule fires (in t's zone).
func (c *Cron) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if !c.mon[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.hour[t.Hour()] {
			t = t.Truncate(time.Hour).Add(time.Hour)
			continue
		}
		if !c.min[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// NextRun parses a schedule and returns its next time after now ("" or bad = zero).
func NextRun(schedule string, now time.Time) time.Time {
	if strings.TrimSpace(schedule) == "" {
		return time.Time{}
	}
	c, err := ParseCron(schedule)
	if err != nil {
		return time.Time{}
	}
	return c.Next(now)
}
