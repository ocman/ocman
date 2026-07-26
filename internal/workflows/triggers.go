package workflows

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
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

// Cron parsing is delegated to robfig/cron, the same implementation
// prompt schedules already use. A single engine means one set of
// semantics, and it brings timezone support the hand-rolled parser could
// not express.

// cronSchedule wraps a parsed expression with the location it is
// evaluated in.
type cronSchedule struct {
	inner cron.Schedule
}

// parseCron compiles a 5-field expression. An empty timezone keeps the
// historical behaviour of evaluating in the server's local time.
func parseCron(expression, timezone string) (*cronSchedule, error) {
	if strings.TrimSpace(expression) == "" {
		return nil, fmt.Errorf("cron expression is required")
	}
	location := time.Local
	if timezone != "" {
		loaded, err := time.LoadLocation(timezone)
		if err != nil {
			return nil, fmt.Errorf("unknown timezone %q: %w", timezone, err)
		}
		location = loaded
	}
	inner, err := cron.ParseStandard(expression)
	if err != nil {
		return nil, fmt.Errorf("invalid cron %q: %w", expression, err)
	}
	return &cronSchedule{inner: scheduleInLocation{inner: inner, location: location}}, nil
}

// next returns the first firing strictly after t.
func (c *cronSchedule) next(t time.Time) time.Time { return c.inner.Next(t) }

// firedSince reports whether the schedule was due in (since, now].
// Asking the schedule to step forward from the last firing is exact,
// unlike scanning minute by minute backwards, and it cannot miss a
// firing that a slow tick skipped over.
func (c *cronSchedule) firedSince(since, now time.Time) bool {
	next := c.next(since)
	return !next.After(now)
}

// scheduleInLocation evaluates a schedule in a fixed location, so a
// cron trigger means the same wall-clock time regardless of the
// server's own zone.
type scheduleInLocation struct {
	inner    cron.Schedule
	location *time.Location
}

func (s scheduleInLocation) Next(t time.Time) time.Time {
	return s.inner.Next(t.In(s.location))
}

func nextCron(expression, timezone string, now time.Time) (time.Time, bool, error) {
	schedule, err := parseCron(expression, timezone)
	if err != nil {
		return time.Time{}, false, err
	}
	next := schedule.next(now)
	return next, !next.IsZero(), nil
}
