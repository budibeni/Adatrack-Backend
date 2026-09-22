package controllers

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSpec is a minimal 5-field cron matcher (minute hour dom month dow). It
// supports `*`, `*/N`, comma lists, ranges and single values — enough for the
// documented `MEDIA_CLEANUP_CRON` default (`0 3 * * *`) without pulling a cron
// library in.
type cronSpec struct {
	minute, hour, dom, month, dow map[int]bool
}

// parseCron parses and validates a cron expression.
func parseCron(expr string) (*cronSpec, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("MEDIA_CLEANUP_CRON must have 5 fields (minute hour dom month dow), got %q", expr)
	}
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	sets := [5]map[int]bool{}
	for i, field := range fields {
		set, err := parseCronField(field, bounds[i][0], bounds[i][1])
		if err != nil {
			return nil, fmt.Errorf("MEDIA_CLEANUP_CRON field %d: %w", i+1, err)
		}
		sets[i] = set
	}
	return &cronSpec{
		minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
	}, nil
}

// parseCronField expands one field into the set of values it matches.
func parseCronField(field string, minV, maxV int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty value")
		}
		step := 1
		base := part
		if idx := strings.Index(part, "/"); idx >= 0 {
			base = part[:idx]
			parsed, err := strconv.Atoi(part[idx+1:])
			if err != nil || parsed <= 0 {
				return nil, fmt.Errorf("invalid step in %q", part)
			}
			step = parsed
		}
		start, end := minV, maxV
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			parts := strings.SplitN(base, "-", 2)
			from, err1 := strconv.Atoi(parts[0])
			to, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid range %q", base)
			}
			start, end = from, to
		default:
			val, err := strconv.Atoi(base)
			if err != nil {
				return nil, fmt.Errorf("invalid value %q", base)
			}
			start, end = val, val
		}
		if start < minV || end > maxV || start > end {
			return nil, fmt.Errorf("value out of range in %q (allowed %d-%d)", field, minV, maxV)
		}
		for v := start; v <= end; v += step {
			out[v] = true
		}
	}
	return out, nil
}

// matches reports whether a timestamp falls inside the schedule.
func (c *cronSpec) matches(t time.Time) bool {
	dow := int(t.Weekday())
	// 7 is the conventional alias for Sunday.
	return c.minute[t.Minute()] && c.hour[t.Hour()] && c.dom[t.Day()] &&
		c.month[int(t.Month())] && (c.dow[dow] || (dow == 0 && c.dow[7]))
}

// retentionScheduler decides when the sweep runs: an explicit fixed interval
// (MEDIA_RETENTION_SWEEP_SEC, dev/E2E) takes precedence over the cron schedule.
type retentionScheduler struct {
	interval time.Duration
	spec     *cronSpec
	lastRun  time.Time
}

// newRetentionScheduler builds the scheduler from the settings (validated at boot).
func newRetentionScheduler(s Settings) (*retentionScheduler, error) {
	if s.SweepOverrideSeconds > 0 {
		return &retentionScheduler{interval: time.Duration(s.SweepOverrideSeconds) * time.Second}, nil
	}
	spec, err := parseCron(s.CleanupCron)
	if err != nil {
		return nil, err
	}
	return &retentionScheduler{spec: spec}, nil
}

// due reports whether a sweep should start now (at most once per interval or
// per matching minute).
func (r *retentionScheduler) due(now time.Time) bool {
	if r.interval > 0 {
		if r.lastRun.IsZero() || now.Sub(r.lastRun) >= r.interval {
			r.lastRun = now
			return true
		}
		return false
	}
	if r.spec == nil || !r.spec.matches(now) {
		return false
	}
	if !r.lastRun.IsZero() && r.lastRun.Truncate(time.Minute).Equal(now.Truncate(time.Minute)) {
		return false
	}
	r.lastRun = now
	return true
}
