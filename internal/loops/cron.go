package loops

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSchedule is a parsed 5-field cron expression evaluated in the
// server's local time zone. Fields: minute hour day-of-month month
// day-of-week. Each field supports "*", integers, comma lists, "a-b"
// ranges, and "*/n" / "a-b/n" steps. Day-of-month and day-of-week match
// in the standard cron OR sense when both are restricted.
//
// ponytail: 5-field standard cron only — no "@daily" macros, no seconds,
// no L/W/# extensions. Add them only if a loop actually needs them.
type cronSchedule struct {
	min, hour, dom, month, dow uint64 // bitmasks; bit i set => value i allowed
	domStar, dowStar           bool   // whether the field was "*" (for OR semantics)
}

const (
	minField = iota
	hourField
	domField
	monthField
	dowField
)

// field bounds: [min,max] inclusive.
var cronBounds = [5][2]int{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day of month
	{1, 12}, // month
	{0, 6},  // day of week (0 = Sunday)
}

// parseCron parses a 5-field cron expression.
func parseCron(expr string) (*cronSchedule, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields (got %d): %q", len(fields), expr)
	}
	masks := [5]uint64{}
	for i, f := range fields {
		m, err := parseCronField(f, cronBounds[i][0], cronBounds[i][1])
		if err != nil {
			return nil, fmt.Errorf("cron field %d (%q): %w", i+1, f, err)
		}
		masks[i] = m
	}
	return &cronSchedule{
		min:     masks[minField],
		hour:    masks[hourField],
		dom:     masks[domField],
		month:   masks[monthField],
		dow:     masks[dowField],
		domStar: fields[domField] == "*",
		dowStar: fields[dowField] == "*",
	}, nil
}

// parseCronField turns one field into a bitmask of allowed values.
func parseCronField(f string, lo, hi int) (uint64, error) {
	var mask uint64
	for _, part := range strings.Split(f, ",") {
		rangePart, step := part, 1
		if slash := strings.IndexByte(part, '/'); slash >= 0 {
			rangePart = part[:slash]
			s, err := strconv.Atoi(part[slash+1:])
			if err != nil || s <= 0 {
				return 0, fmt.Errorf("invalid step %q", part[slash+1:])
			}
			step = s
		}

		start, end := lo, hi
		switch {
		case rangePart == "*":
			// full range
		case strings.IndexByte(rangePart, '-') >= 0:
			dash := strings.IndexByte(rangePart, '-')
			a, err1 := strconv.Atoi(rangePart[:dash])
			b, err2 := strconv.Atoi(rangePart[dash+1:])
			if err1 != nil || err2 != nil {
				return 0, fmt.Errorf("invalid range %q", rangePart)
			}
			start, end = a, b
		default:
			n, err := strconv.Atoi(rangePart)
			if err != nil {
				return 0, fmt.Errorf("invalid value %q", rangePart)
			}
			start, end = n, n
		}

		if start < lo || end > hi || start > end {
			return 0, fmt.Errorf("value out of range [%d,%d]", lo, hi)
		}
		for v := start; v <= end; v += step {
			mask |= 1 << uint(v)
		}
	}
	return mask, nil
}

// matches reports whether t (local time) satisfies the schedule.
func (c *cronSchedule) matches(t time.Time) bool {
	if c.min&(1<<uint(t.Minute())) == 0 {
		return false
	}
	if c.hour&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if c.month&(1<<uint(int(t.Month()))) == 0 {
		return false
	}
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(int(t.Weekday()))) != 0
	// Standard cron: if both dom and dow are restricted, match either;
	// if one is "*", that one doesn't constrain, so require the other.
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dowOK
	case c.dowStar:
		return domOK
	default:
		return domOK || dowOK
	}
}

// prev returns the latest scheduled time at or before `now` (minute
// resolution), searching back up to ~13 months. Returns false if none
// found (e.g. an impossible expression like Feb 30).
func (c *cronSchedule) prev(now time.Time) (time.Time, bool) {
	t := now.Truncate(time.Minute)
	// 366 days * 24h * 60m worst case; bound the scan.
	for i := 0; i < 366*24*60; i++ {
		if c.matches(t) {
			return t, true
		}
		t = t.Add(-time.Minute)
	}
	return time.Time{}, false
}
