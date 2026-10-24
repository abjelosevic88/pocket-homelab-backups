package engine

import (
	"testing"
	"time"
)

func TestCron(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 10, 24, 15, 7, 30, 0, loc)
	cases := map[string]time.Time{
		"15 4 * * *":   time.Date(2026, 10, 25, 4, 15, 0, 0, loc),
		"0 2 * * 0":    time.Date(2026, 10, 25, 2, 0, 0, 0, loc), // Sunday
		"*/20 * * * *": time.Date(2026, 10, 24, 15, 20, 0, 0, loc),
		"@monthly":     time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
		"30 3 1 * 1":   time.Date(2026, 10, 26, 3, 30, 0, 0, loc), // day 1 OR Monday
	}
	for spec, want := range cases {
		c, err := ParseCron(spec)
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if got := c.Next(from); !got.Equal(want) {
			t.Errorf("%s: next = %s, want %s", spec, got, want)
		}
	}
	for _, bad := range []string{"", "* * *", "61 * * * *", "* 24 * * *", "a b c d e"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestLogKeepsHeadAndTail(t *testing.T) {
	l := NewLog(1000)
	for i := 0; i < 200; i++ {
		l.Printf("line %03d with some padding", i)
	}
	s := l.String()
	if len(s) > 1100 {
		t.Fatalf("log grew to %d bytes", len(s))
	}
	if !contains(s, "line 000") || !contains(s, "line 199") || !contains(s, "cut") {
		t.Fatalf("log should keep the start and the end:\n%s", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestCronRepeatedHourRunsOnce(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Sarajevo")
	if err != nil {
		t.Skip("no zoneinfo")
	}
	c, _ := ParseCron("0 2 * * *")
	first := c.Next(time.Date(2026, 10, 24, 17, 0, 0, 0, loc)) // the night the clocks go back
	second := c.Next(first)
	if first.UTC().Hour() != 0 || second.Sub(first) < 24*time.Hour {
		t.Fatalf("02:00 on 25 Oct should come once: %s then %s", first.UTC(), second.UTC())
	}
}
