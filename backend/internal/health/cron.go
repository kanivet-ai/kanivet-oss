package health

import (
	"strconv"
	"strings"
	"time"
)

// CronPeriod returns the longest gap between two consecutive firings of a
// standard five-field schedule (or @hourly style macro) over a sample
// window, which is how late a run can be before it counts as missed.
func CronPeriod(spec string) (time.Duration, bool) {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "CRON_TZ=") || strings.HasPrefix(spec, "TZ=") {
		if i := strings.IndexByte(spec, ' '); i > 0 {
			spec = strings.TrimSpace(spec[i+1:])
		}
	}
	switch spec {
	case "@yearly", "@annually":
		return 366 * 24 * time.Hour, true
	case "@monthly":
		return 31 * 24 * time.Hour, true
	case "@weekly":
		return 7 * 24 * time.Hour, true
	case "@daily", "@midnight":
		return 24 * time.Hour, true
	case "@hourly":
		return time.Hour, true
	}
	if strings.HasPrefix(spec, "@every ") {
		d, err := time.ParseDuration(strings.TrimPrefix(spec, "@every "))
		return d, err == nil && d > 0
	}
	f := strings.Fields(spec)
	if len(f) != 5 {
		return 0, false
	}
	ranges := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	var sets [5]map[int]bool
	for i := range f {
		s, ok := cronField(f[i], ranges[i][0], ranges[i][1])
		if !ok {
			return 0, false
		}
		sets[i] = s
	}
	// Day-of-week 7 is Sunday too.
	if sets[4][7] {
		sets[4][0] = true
	}
	domAny, dowAny := f[2] == "*" || f[2] == "?", f[4] == "*" || f[4] == "?"
	match := func(t time.Time) bool {
		if !sets[0][t.Minute()] || !sets[1][t.Hour()] || !sets[3][int(t.Month())] {
			return false
		}
		dom, dow := sets[2][t.Day()], sets[4][int(t.Weekday())]
		if domAny || dowAny {
			return dom && dow
		}
		return dom || dow
	}
	// Walk a year in minutes over hours that can match; that is cheap enough
	// for the handful of CronJobs a cluster has.
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(1, 0, 0)
	var prev time.Time
	var maxGap time.Duration
	hits := 0
	for t := start; t.Before(end); {
		if !sets[3][int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !sets[1][t.Hour()] {
			t = t.Truncate(time.Hour).Add(time.Hour)
			continue
		}
		if match(t) {
			if !prev.IsZero() && t.Sub(prev) > maxGap {
				maxGap = t.Sub(prev)
			}
			prev = t
			hits++
		}
		t = t.Add(time.Minute)
	}
	if hits < 2 {
		if hits == 1 {
			return 366 * 24 * time.Hour, true
		}
		return 0, false
	}
	return maxGap, true
}

func cronField(field string, lo, hi int) (map[int]bool, bool) {
	out := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		step := 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return nil, false
			}
			step = n
			part = part[:i]
		}
		from, to := lo, hi
		switch {
		case part == "*" || part == "?":
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			x, err1 := cronValue(a)
			y, err2 := cronValue(b)
			if err1 != nil || err2 != nil {
				return nil, false
			}
			from, to = x, y
		default:
			x, err := cronValue(part)
			if err != nil {
				return nil, false
			}
			from = x
			if step == 1 {
				to = x
			}
		}
		if from < lo || to > hi+1 || from > to {
			return nil, false
		}
		for v := from; v <= to; v += step {
			out[v] = true
		}
	}
	return out, true
}

var cronNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

func cronValue(s string) (int, error) {
	if v, ok := cronNames[strings.ToLower(s)]; ok {
		return v, nil
	}
	return strconv.Atoi(s)
}
