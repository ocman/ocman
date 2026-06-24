package loops

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestParseCron_Errors(t *testing.T) {
	bad := []string{
		"",                // empty
		"* * * *",         // too few fields
		"* * * * * *",     // too many fields
		"60 * * * *",      // minute out of range
		"* 24 * * *",      // hour out of range
		"* * * * 7",       // dow out of range
		"*/0 * * * *",     // zero step
		"5-2 * * * *",     // inverted range
		"abc * * * *",     // non-numeric
	}
	for _, expr := range bad {
		if _, err := parseCron(expr); err == nil {
			t.Errorf("parseCron(%q): expected error, got nil", expr)
		}
	}
}

func TestCronSchedule_Matches(t *testing.T) {
	// 0 23 * * * → every day at 23:00.
	sched, err := parseCron("0 23 * * *")
	if err != nil {
		t.Fatalf("parseCron: %v", err)
	}
	at2300 := time.Date(2026, 6, 24, 23, 0, 0, 0, time.Local)
	if !sched.matches(at2300) {
		t.Errorf("expected match at 23:00")
	}
	if sched.matches(at2300.Add(time.Minute)) {
		t.Errorf("did not expect match at 23:01")
	}
	if sched.matches(at2300.Add(-time.Hour)) {
		t.Errorf("did not expect match at 22:00")
	}

	// Steps + lists + ranges.
	s2, err := parseCron("*/15 9-17 * * 1-5")
	if err != nil {
		t.Fatalf("parseCron: %v", err)
	}
	// 2026-06-24 is a Wednesday (weekday 3), in 1-5.
	if !s2.matches(time.Date(2026, 6, 24, 9, 30, 0, 0, time.Local)) {
		t.Errorf("expected match at Wed 09:30")
	}
	if s2.matches(time.Date(2026, 6, 24, 9, 31, 0, 0, time.Local)) {
		t.Errorf("did not expect match at 09:31 (not a 15-min step)")
	}
	// Saturday (weekday 6) excluded.
	if s2.matches(time.Date(2026, 6, 27, 9, 0, 0, 0, time.Local)) {
		t.Errorf("did not expect match on Saturday")
	}
}

func TestCronTrigger_ShouldFire(t *testing.T) {
	// Daily at 23:00.
	tc := TriggerConfig{CronExpr: "0 23 * * *"}
	now := time.Date(2026, 6, 24, 23, 0, 30, 0, time.Local) // 23:00:30

	// Never fired, and now is within the 23:00 minute → fire.
	fire, _, _, err := cronTrigger{}.ShouldFire(context.Background(), state.Loop{LastFiredAt: 0}, tc, now)
	if err != nil {
		t.Fatalf("ShouldFire: %v", err)
	}
	if !fire {
		t.Errorf("expected fire at 23:00 on never-fired loop")
	}

	// Never fired, now is NOT a scheduled minute → no fire (no backfire).
	fire, _, _, _ = cronTrigger{}.ShouldFire(context.Background(), state.Loop{LastFiredAt: 0}, tc,
		time.Date(2026, 6, 24, 12, 0, 0, 0, time.Local))
	if fire {
		t.Errorf("did not expect fire at noon on never-fired loop")
	}

	// Already fired yesterday at 23:00 → today's 23:00 is newer → fire.
	lastFired := time.Date(2026, 6, 23, 23, 0, 0, 0, time.Local).UnixMilli()
	fire, _, _, _ = cronTrigger{}.ShouldFire(context.Background(), state.Loop{LastFiredAt: lastFired}, tc, now)
	if !fire {
		t.Errorf("expected fire: today's scheduled time is newer than last fire")
	}

	// Fired today at 23:00 already, now 23:05 → no new scheduled slot → no fire.
	firedToday := time.Date(2026, 6, 24, 23, 0, 0, 0, time.Local).UnixMilli()
	fire, _, _, _ = cronTrigger{}.ShouldFire(context.Background(), state.Loop{LastFiredAt: firedToday}, tc,
		time.Date(2026, 6, 24, 23, 5, 0, 0, time.Local))
	if fire {
		t.Errorf("did not expect a second fire within the same day")
	}

	// Invalid expr surfaces an error.
	if _, _, _, err := (cronTrigger{}).ShouldFire(context.Background(), state.Loop{}, TriggerConfig{CronExpr: "nope"}, now); err == nil {
		t.Errorf("expected error for invalid cron expr")
	}
}
