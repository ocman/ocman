package idleprojects

import (
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestScheduledRoutineDoesNotBlockButActiveRunDoes(t *testing.T) {
	m, _, _ := setup(t)
	routine := state.Routine{ID: "r", Platform: "test", Name: "future", Prompt: "work", Directory: "/repo", RemoteID: "local", Enabled: true, ScheduleKind: "once", NextDueAt: time.Now().Add(time.Hour).UnixMilli(), SessionMode: "new"}
	must(t, m.deps.Store.CreateRoutine(t.Context(), routine))
	_, _, idle, err := m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
	if err != nil || !idle {
		t.Fatal("future routine kept project alive", err)
	}
	_, claimed, err := m.deps.Store.ClaimRoutineRun(t.Context(), state.RoutineRun{ID: "run", RoutineID: "r", Trigger: "schedule", State: "running", OccurrenceAt: 1})
	if err != nil || !claimed {
		t.Fatal(err)
	}
	_, _, idle, err = m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
	if err != nil || idle {
		t.Fatal("active run was ignored", err)
	}
}

func TestCompletedRoutineSessionNeedsNoArchive(t *testing.T) {
	m, _, _ := setup(t)
	routine := state.Routine{ID: "r", Platform: "test", Name: "routine", Directory: "/repo", RemoteID: "local", SessionMode: "reuse"}
	must(t, m.deps.Store.CreateRoutine(t.Context(), routine))
	_, _, err := m.deps.Store.ClaimRoutineRun(t.Context(), state.RoutineRun{ID: "run", RoutineID: "r", State: "running", OccurrenceAt: 1})
	must(t, err)
	must(t, m.deps.Store.LinkRoutineRun(t.Context(), "run", "test", "s", 1, true))
	finished, err := m.deps.Store.FinishRoutineRun(t.Context(), "run", "success", "", "", 1, 0, false, false)
	if err != nil || !finished {
		t.Fatal("finish routine run", finished, err)
	}
	must(t, m.deps.Store.UnarchiveSession(t.Context(), "test", "s"))
	_, _, idle, err := m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
	if err != nil || !idle {
		t.Fatal("completed routine session prevented culling", err)
	}
}

func TestFactoryClaimsKeepProjectsAlive(t *testing.T) {
	for _, policy := range []string{`{"repository":"/repo"}`, `{"repository":"/other","projects":["/src/other","/repo"]}`} {
		m, _, _ := setup(t)
		_, err := m.deps.Store.SQL().ExecContext(t.Context(), `INSERT INTO factory_attempt (id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at) VALUES ('a', 'e', 'w', 1, 'prepared', ?, 1, 1)`, policy)
		must(t, err)
		_, _, idle, err := m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
		if err != nil || idle {
			t.Fatal("Factory claim was ignored", err)
		}
		_, err = m.deps.Store.SQL().ExecContext(t.Context(), `UPDATE factory_attempt SET phase='terminal', terminal_outcome='succeeded', session_platform='test', session_id='s' WHERE id='a'`)
		must(t, err)
		must(t, m.deps.Store.UnarchiveSession(t.Context(), "test", "s"))
		_, _, idle, err = m.eligible(t.Context(), m.deps.Targets()[0], "/repo", false)
		if err != nil || !idle {
			t.Fatal("completed Factory attempt prevented culling", err)
		}
	}
}
