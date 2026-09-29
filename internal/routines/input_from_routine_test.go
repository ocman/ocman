package routines

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestInputFromRoutineRoundTrips(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	for _, schedule := range []Schedule{
		{Kind: ScheduleNone},
		{Kind: ScheduleTimeout, Timeout: time.Hour},
		{Kind: ScheduleOnce, At: now.Add(time.Hour)},
		{Kind: ScheduleCron, Cron: "0 9 * * *", Timezone: "Europe/Brussels"},
	} {
		in := Input{Name: "r", Prompt: "p", Directory: "/repo", RemoteID: "local", Agent: "plan", SessionMode: SessionReuse, Schedule: schedule, Enabled: true,
			PermissionRules: []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}}}
		stored, err := buildRoutine(in, now)
		if err != nil {
			t.Fatal(err)
		}
		later := now.Add(10 * time.Minute)
		back, err := InputFromRoutine(stored, later)
		if err != nil {
			t.Fatal(err)
		}
		rebuilt, err := buildRoutine(back, later)
		if err != nil {
			t.Fatalf("%s: %v", schedule.Kind, err)
		}
		if rebuilt.ScheduleConfigJSON != stored.ScheduleConfigJSON || rebuilt.PermissionRulesJSON != stored.PermissionRulesJSON || back.Agent != "plan" || !back.Enabled {
			t.Fatalf("%s: stored %s / %s, rebuilt %s / %s", schedule.Kind, stored.ScheduleConfigJSON, stored.PermissionRulesJSON, rebuilt.ScheduleConfigJSON, rebuilt.PermissionRulesJSON)
		}
		if schedule.Kind == ScheduleTimeout && rebuilt.NextDueAt != stored.NextDueAt {
			t.Fatalf("timeout due moved: %d -> %d", stored.NextDueAt, rebuilt.NextDueAt)
		}
	}
}
