// Package schedule turns a cron job's schedule into a systemd OnCalendar value. It accepts 5-field cron
// ("*/15 * * * *"), the shorthands @hourly @daily @weekly @monthly @yearly, systemd's own words (hourly,
// daily, ...), and systemd calendar expressions ("Mon..Fri *-*-* 09:00:00"). Output only ever contains
// characters that are safe in a unit file.
package schedule

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var (
	words   = map[string]string{"minutely": "minutely", "hourly": "hourly", "daily": "daily", "weekly": "weekly", "monthly": "monthly", "yearly": "yearly", "annually": "yearly", "quarterly": "quarterly"}
	calRE   = regexp.MustCompile(`^[A-Za-z0-9 ,.:*/~-]{1,100}$`)
	fieldRE = regexp.MustCompile(`^[0-9*/,-]+$`)
	days    = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
)

// OnCalendar converts s, or explains why it can't.
func OnCalendar(s string) (string, error) {
	s = strings.TrimSpace(s)
	if w, ok := words[strings.TrimPrefix(strings.ToLower(s), "@")]; ok {
		return w, nil
	}
	if f := strings.Fields(s); len(f) == 5 && allCron(f) {
		return fromCron(f)
	}
	if !calRE.MatchString(s) {
		return "", errors.New("schedule: use 5-field cron (\"*/15 * * * *\"), @daily and friends, or a systemd calendar expression")
	}
	return s, nil // checked by systemd-analyze before use
}

func allCron(f []string) bool {
	for _, x := range f {
		if !fieldRE.MatchString(x) {
			return false
		}
	}
	return true
}

// field converts one cron field; start is the first value of a "*/n" step (0 for minutes, 1 for days).
func field(x string, start, lo, hi int) (string, error) {
	if x == "*" {
		return "*", nil
	}
	var parts []string
	for _, p := range strings.Split(x, ",") {
		base, step, hasStep := strings.Cut(p, "/")
		if hasStep {
			n, err := strconv.Atoi(step)
			if err != nil || n < 1 || n > hi {
				return "", errors.New("bad step " + p)
			}
		}
		switch {
		case base == "*":
			base = strconv.Itoa(start)
		case strings.Contains(base, "-"):
			a, b, _ := strings.Cut(base, "-")
			if !inRange(a, lo, hi) || !inRange(b, lo, hi) {
				return "", errors.New("bad range " + p)
			}
			base = a + ".." + b
		default:
			if !inRange(base, lo, hi) {
				return "", errors.New("bad value " + p)
			}
		}
		if hasStep {
			parts = append(parts, base+"/"+step)
		} else {
			parts = append(parts, base)
		}
	}
	return strings.Join(parts, ","), nil
}

func inRange(s string, lo, hi int) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= lo && n <= hi
}

func fromCron(f []string) (string, error) {
	min, err := field(f[0], 0, 0, 59)
	if err != nil {
		return "", err
	}
	hour, err := field(f[1], 0, 0, 23)
	if err != nil {
		return "", err
	}
	dom, err := field(f[2], 1, 1, 31)
	if err != nil {
		return "", err
	}
	mon, err := field(f[3], 1, 1, 12)
	if err != nil {
		return "", err
	}
	if f[2] != "*" && f[4] != "*" {
		// cron runs when EITHER matches; systemd would require both
		return "", errors.New("schedule: a day of month and a day of week together mean \"either\" in cron; split it into two jobs or use systemd calendar syntax")
	}
	dow := ""
	if f[4] != "*" {
		var ds []string
		for _, p := range strings.Split(f[4], ",") {
			if strings.Contains(p, "/") {
				return "", errors.New("schedule: steps in the day-of-week field aren't supported")
			}
			a, b, isRange := strings.Cut(p, "-")
			if !inRange(a, 0, 7) || (isRange && !inRange(b, 0, 7)) {
				return "", errors.New("bad day of week " + p)
			}
			ai, _ := strconv.Atoi(a)
			if isRange {
				bi, _ := strconv.Atoi(b)
				ds = append(ds, days[ai]+".."+days[bi])
			} else {
				ds = append(ds, days[ai])
			}
		}
		dow = strings.Join(ds, ",") + " "
	}
	return dow + "*-" + mon + "-" + dom + " " + hour + ":" + min + ":00", nil
}
