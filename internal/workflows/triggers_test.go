package workflows

import (
	"testing"
	"time"
)

func TestCronListsRangesStepsAndDaySemantics(t *testing.T) {
	schedule, err := parseCron("*/15 9-17 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		at   time.Time
		want bool
	}{
		{time.Date(2026, time.July, 13, 9, 30, 0, 0, time.UTC), true},
		{time.Date(2026, time.July, 13, 9, 31, 0, 0, time.UTC), false},
		{time.Date(2026, time.July, 12, 9, 30, 0, 0, time.UTC), false},
		{time.Date(2026, time.July, 13, 18, 30, 0, 0, time.UTC), false},
	} {
		if got := schedule.matches(test.at); got != test.want {
			t.Errorf("matches(%s) = %v, want %v", test.at, got, test.want)
		}
	}

	dayOrWeekday, err := parseCron("0 0 1 * 1")
	if err != nil {
		t.Fatal(err)
	}
	if !dayOrWeekday.matches(time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)) ||
		!dayOrWeekday.matches(time.Date(2026, time.September, 7, 0, 0, 0, 0, time.UTC)) ||
		dayOrWeekday.matches(time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("day-of-month and day-of-week did not use cron OR semantics")
	}
}

func TestCronRollsIntoNextYear(t *testing.T) {
	now := time.Date(2026, time.December, 31, 23, 59, 30, 0, time.UTC)
	got, ok, err := nextCron("0 0 1 1 *", now)
	want := time.Date(2027, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err != nil || !ok || !got.Equal(want) {
		t.Fatalf("next cron = %s, %v, %v; want %s", got, ok, err, want)
	}
}

func TestCronRejectsInvalidFields(t *testing.T) {
	for _, expression := range []string{
		"* * * *",
		"*/0 * * * *",
		"60 * * * *",
		"5-2 * * * *",
		"nope * * * *",
	} {
		if _, err := parseCron(expression); err == nil {
			t.Errorf("parseCron(%q) succeeded", expression)
		}
	}
}
