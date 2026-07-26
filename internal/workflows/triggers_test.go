package workflows

import (
	"testing"
	"time"
)

// dueAt reports whether the schedule fires exactly at t.
func dueAt(schedule *cronSchedule, t time.Time) bool {
	return schedule.next(t.Add(-time.Minute)).Equal(t)
}

func TestCronListsRangesStepsAndDaySemantics(t *testing.T) {
	schedule, err := parseCron("*/15 9-17 * * 1-5", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		at   time.Time
		want bool
	}{
		{time.Date(2026, time.July, 13, 9, 30, 0, 0, time.UTC), true},
		{time.Date(2026, time.July, 13, 9, 31, 0, 0, time.UTC), false},
		// A Sunday, outside the 1-5 weekday range.
		{time.Date(2026, time.July, 12, 9, 30, 0, 0, time.UTC), false},
		{time.Date(2026, time.July, 13, 18, 30, 0, 0, time.UTC), false},
	} {
		if got := dueAt(schedule, test.at); got != test.want {
			t.Errorf("due at %s = %v, want %v", test.at, got, test.want)
		}
	}

	// Vixie semantics: a restricted day-of-month and day-of-week are
	// OR-ed, not AND-ed.
	dayOrWeekday, err := parseCron("0 0 1 * 1", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if !dueAt(dayOrWeekday, time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)) ||
		!dueAt(dayOrWeekday, time.Date(2026, time.September, 7, 0, 0, 0, 0, time.UTC)) ||
		dueAt(dayOrWeekday, time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("day-of-month and day-of-week did not use cron OR semantics")
	}
}

func TestCronRollsIntoNextYear(t *testing.T) {
	now := time.Date(2026, time.December, 31, 23, 59, 30, 0, time.UTC)
	got, ok, err := nextCron("0 0 1 1 *", "UTC", now)
	want := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err != nil || !ok || !got.Equal(want) {
		t.Fatalf("next cron = %s, %v, %v; want %s", got, ok, err, want)
	}
}

func TestCronRejectsInvalidExpressions(t *testing.T) {
	for _, expression := range []string{
		"",
		"* * * *",
		"60 * * * *",
		"nope * * * *",
	} {
		if _, err := parseCron(expression, "UTC"); err == nil {
			t.Errorf("parseCron(%q) succeeded", expression)
		}
	}
	if _, err := parseCron("0 3 * * *", "Mars/Olympus"); err == nil {
		t.Error("parseCron accepted an unknown timezone")
	}
}

// A cron trigger must mean the same wall-clock time wherever ocman runs.
func TestCronEvaluatesInItsOwnTimezone(t *testing.T) {
	brussels, err := parseCron("0 3 * * *", "Europe/Brussels")
	if err != nil {
		t.Fatal(err)
	}
	utc, err := parseCron("0 3 * * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	// Midwinter: Brussels is UTC+1, so its 03:00 is 02:00 UTC.
	from := time.Date(2027, time.January, 10, 0, 0, 0, 0, time.UTC)
	if got := brussels.next(from).UTC(); !got.Equal(time.Date(2027, time.January, 10, 2, 0, 0, 0, time.UTC)) {
		t.Errorf("Brussels next = %s", got)
	}
	if got := utc.next(from).UTC(); !got.Equal(time.Date(2027, time.January, 10, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("UTC next = %s", got)
	}
}

// Stepping forward from the last firing cannot miss a slot a slow tick
// skipped over, which a backwards minute scan could.
func TestCronFiredSinceCatchesSkippedSlots(t *testing.T) {
	schedule, err := parseCron("*/5 * * * *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	last := time.Date(2026, time.July, 13, 9, 0, 0, 0, time.UTC)
	if schedule.firedSince(last, last.Add(2*time.Minute)) {
		t.Error("fired before the next slot was due")
	}
	// The tick was late by an hour; the 09:05 slot still counts.
	if !schedule.firedSince(last, last.Add(time.Hour)) {
		t.Error("a slot skipped by a late tick was lost")
	}
}
