package routines

import (
	"slices"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// fuzzZones are the time zones FuzzEncodeCron schedules in: UTC plus zones
// with DST transitions on both hemispheres. Arbitrary names would only
// exercise time.LoadLocation's file lookups.
var fuzzZones = []string{"", "UTC", "Europe/Brussels", "America/New_York", "Australia/Sydney", "Asia/Kolkata"}

// FuzzEncodeCron checks that an accepted cron schedule is actually due: its
// next occurrence is strictly after now (a schedule matching no date, such
// as February 31, is rejected rather than saved as never due), and storing
// and reloading the routine through InputFromRoutine yields the same
// configuration and due time.
func FuzzEncodeCron(f *testing.F) {
	for _, s := range []string{"0 0 31 2 *", "*/5 * * * *", "30 2 * * *", "0 0 29 2 1", "0-59/7 0-23/5 1-31 1-12 0-6", "0 0 30 2 *", "@daily", "0 0 * * 7"} {
		f.Add(s, "UTC", int64(1_700_000_000_000))
		f.Add(s, "Europe/Brussels", int64(1_711_846_800_000))
		f.Add(s, "America/New_York", int64(1_710_054_000_000))
	}
	f.Fuzz(func(t *testing.T, expr, zone string, nowMS int64) {
		if nowMS < 0 || nowMS > 4_000_000_000_000 || !slices.Contains(fuzzZones, zone) {
			return
		}
		now := time.UnixMilli(nowMS)
		start := time.Now()
		config, due, err := encodeSchedule(Schedule{Kind: ScheduleCron, Cron: expr, Timezone: zone}, now)
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("encodeSchedule(%q, %q) took %v", expr, zone, d)
		}
		if err != nil {
			return
		}
		if due <= nowMS {
			t.Fatalf("accepted cron %q in %q is due at %d, not after now %d", expr, zone, due, nowMS)
		}
		routine := state.Routine{ScheduleKind: ScheduleCron, ScheduleConfigJSON: config, Name: "n", Prompt: "p", Directory: "/d", SessionMode: SessionNew}
		input, err := InputFromRoutine(routine, now)
		if err != nil {
			t.Fatalf("reloading %s: %v", config, err)
		}
		config2, due2, err := encodeSchedule(input.Schedule, now)
		if err != nil || config2 != config || due2 != due {
			t.Fatalf("reloaded schedule %s due %d (%v), saved %s due %d", config2, due2, err, config, due)
		}
	})
}
