package workflows

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type PRState struct {
	HeadSHA string
	Merged  bool
}

type ForgePoller interface {
	PollPR(context.Context, string, int) (PRState, error)
}

type SessionStatusInferer interface {
	TurnRunning(context.Context, string, string) (bool, bool)
}

type UsageSource interface {
	SessionUsage(context.Context, []string) (int64, float64, bool)
}

type triggerConfig struct {
	IntervalSeconds int    `json:"intervalSeconds,omitempty"`
	CronExpr        string `json:"cron,omitempty"`
	PRNumber        int    `json:"prNumber,omitempty"`
	PollSeconds     int    `json:"pollSeconds,omitempty"`
	LastHeadSHA     string `json:"lastHeadSHA,omitempty"`
	Merged          bool   `json:"merged,omitempty"`
}

type cronSchedule struct {
	min, hour, dom, month, dow uint64
	domStar, dowStar           bool
}

var cronBounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

func parseCron(expr string) (*cronSchedule, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields (got %d): %q", len(fields), expr)
	}
	masks := [5]uint64{}
	for i, field := range fields {
		mask, err := parseCronField(field, cronBounds[i][0], cronBounds[i][1])
		if err != nil {
			return nil, fmt.Errorf("cron field %d (%q): %w", i+1, field, err)
		}
		masks[i] = mask
	}
	return &cronSchedule{masks[0], masks[1], masks[2], masks[3], masks[4], fields[2] == "*", fields[4] == "*"}, nil
}

func parseCronField(field string, lo, hi int) (uint64, error) {
	var mask uint64
	for _, part := range strings.Split(field, ",") {
		rangePart, step := part, 1
		if slash := strings.IndexByte(part, '/'); slash >= 0 {
			rangePart = part[:slash]
			var err error
			step, err = strconv.Atoi(part[slash+1:])
			if err != nil || step <= 0 {
				return 0, fmt.Errorf("invalid step %q", part[slash+1:])
			}
		}
		start, end := lo, hi
		switch {
		case rangePart == "*":
		case strings.ContainsRune(rangePart, '-'):
			dash := strings.IndexByte(rangePart, '-')
			var err error
			start, err = strconv.Atoi(rangePart[:dash])
			if err != nil {
				return 0, fmt.Errorf("invalid range %q", rangePart)
			}
			end, err = strconv.Atoi(rangePart[dash+1:])
			if err != nil {
				return 0, fmt.Errorf("invalid range %q", rangePart)
			}
		default:
			value, err := strconv.Atoi(rangePart)
			if err != nil {
				return 0, fmt.Errorf("invalid value %q", rangePart)
			}
			start, end = value, value
		}
		if start < lo || end > hi || start > end {
			return 0, fmt.Errorf("value out of range [%d,%d]", lo, hi)
		}
		for value := start; value <= end; value += step {
			mask |= 1 << uint(value)
		}
	}
	return mask, nil
}

func (c *cronSchedule) matches(t time.Time) bool {
	if c.min&(1<<uint(t.Minute())) == 0 || c.hour&(1<<uint(t.Hour())) == 0 || c.month&(1<<uint(t.Month())) == 0 {
		return false
	}
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(t.Weekday())) != 0
	if c.domStar {
		return c.dowStar || dowOK
	}
	if c.dowStar {
		return domOK
	}
	return domOK || dowOK
}

func (c *cronSchedule) prev(now time.Time) (time.Time, bool) {
	for t, n := now.Truncate(time.Minute), 0; n < 366*24*60; t, n = t.Add(-time.Minute), n+1 {
		if c.matches(t) {
			return t, true
		}
	}
	return time.Time{}, false
}

func nextCron(expr string, now time.Time) (time.Time, bool, error) {
	schedule, err := parseCron(expr)
	if err != nil {
		return time.Time{}, false, err
	}
	for t, n := now.Truncate(time.Minute).Add(time.Minute), 0; n < 366*24*60; t, n = t.Add(time.Minute), n+1 {
		if schedule.matches(t) {
			return t, true, nil
		}
	}
	return time.Time{}, false, nil
}
