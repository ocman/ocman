package factory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type watchdogStoreFake struct {
	nativeStore
	questions []string
	resumed   time.Time
	resumeErr error
}

func (f *watchdogStoreFake) FactoryAttemptLastResumedAt(context.Context, string) (time.Time, error) {
	return f.resumed, f.resumeErr
}

func (f *watchdogStoreFake) CreateFactoryRecoveryGate(_ context.Context, _, question, reason string, _ []string, _ time.Time) (model.RecoveryGate, error) {
	f.questions = append(f.questions, reason+": "+question)
	return model.RecoveryGate{}, nil
}

type activityFake struct {
	ImplementationLauncher
	last    time.Time
	waiting bool
	err     error
	probes  int
}

func (f *activityFake) ImplementationActivity(context.Context, PlanningSession) (time.Time, bool, error) {
	f.probes++
	return f.last, f.waiting, f.err
}

func TestPauseIdleAttempt(t *testing.T) {
	old := time.Now().Add(-2 * factoryIdleTimeout)
	for _, tc := range []struct {
		name    string
		started time.Time
		probe   activityFake
		running bool
		want    bool
		resumed time.Time
	}{
		{"recently resumed attempt waits a full interval", old, activityFake{last: old}, false, false, time.Now().Add(-time.Minute)},
		{"long-resumed attempt can pause again", old, activityFake{last: old}, false, true, old.Add(time.Minute)},
		{"idle session pauses", old, activityFake{last: old}, false, true, time.Time{}},
		{"activity before start counts from start", old, activityFake{}, false, true, time.Time{}},
		{"recent activity keeps running", old, activityFake{last: time.Now()}, false, false, time.Time{}},
		{"waiting on a human prompt", old, activityFake{last: old, waiting: true}, false, false, time.Time{}},
		{"probe error", old, activityFake{last: old, err: errors.New("gone")}, false, false, time.Time{}},
		{"young attempt is not probed", time.Now(), activityFake{last: old}, false, false, time.Time{}},
		{"formula checks running", old, activityFake{last: old}, true, false, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &watchdogStoreFake{resumed: tc.resumed}
			probe := tc.probe
			s := &NativeService{store: store, implementation: &probe}
			if tc.running {
				s.checks = map[string]verificationCheck{"att-1": {head: "abc"}}
			}
			attempt := model.FactoryAttempt{ID: "att-1", StartedAt: tc.started.UnixMilli()}
			if err := s.pauseIdleAttempt(t.Context(), attempt); err != nil {
				t.Fatal(err)
			}
			if got := len(store.questions) == 1; got != tc.want {
				t.Fatalf("paused = %v, want %v (%v)", got, tc.want, store.questions)
			}
			if tc.want && !strings.Contains(store.questions[0], "Factory watchdog") {
				t.Fatalf("gate = %q", store.questions[0])
			}
		})
	}
}

type nudgingActivityFake struct {
	activityFake
	messages []string
	err      error
}

func (f *nudgingActivityFake) RunVerificationChecks(context.Context, string, string, string, []string) (bool, string, error) {
	return false, "", nil
}

func (f *nudgingActivityFake) NotifyImplementationSession(_ context.Context, _ PlanningSession, message string) error {
	f.messages = append(f.messages, message)
	return f.err
}

func TestPauseIdleAttemptNudgesBeforeGating(t *testing.T) {
	old := time.Now().Add(-2 * factoryIdleTimeout)
	store := &watchdogStoreFake{}
	probe := &nudgingActivityFake{activityFake: activityFake{last: old}}
	s := &NativeService{store: store, implementation: probe}
	attempt := model.FactoryAttempt{ID: "att-1", StartedAt: old.UnixMilli()}
	if err := s.pauseIdleAttempt(t.Context(), attempt); err != nil {
		t.Fatal(err)
	}
	if len(probe.messages) != 1 || !strings.Contains(probe.messages[0], "request_recovery") || len(store.questions) != 0 {
		t.Fatalf("first idle: nudges %v, gates %v", probe.messages, store.questions)
	}
	// Still idle a full interval after the nudge: gate, do not nudge again.
	s.idleNudgedAt["att-1"] = old.Add(time.Minute)
	s.idleProbedAt = nil
	if err := s.pauseIdleAttempt(t.Context(), attempt); err != nil {
		t.Fatal(err)
	}
	if len(probe.messages) != 1 || len(store.questions) != 1 {
		t.Fatalf("after nudge: nudges %v, gates %v", probe.messages, store.questions)
	}
	// A failed nudge falls back to the gate.
	failing := &nudgingActivityFake{activityFake: activityFake{last: old}, err: errors.New("gone")}
	store = &watchdogStoreFake{}
	if err := (&NativeService{store: store, implementation: failing}).pauseIdleAttempt(t.Context(), attempt); err != nil || len(store.questions) != 1 {
		t.Fatalf("failed nudge: err %v, gates %v", err, store.questions)
	}
}

func TestPauseIdleAttemptThrottlesProbes(t *testing.T) {
	probe := &activityFake{last: time.Now()}
	s := &NativeService{store: &watchdogStoreFake{}, implementation: probe}
	attempt := model.FactoryAttempt{ID: "att-1", StartedAt: time.Now().Add(-2 * factoryIdleTimeout).UnixMilli()}
	for range 3 {
		if err := s.pauseIdleAttempt(t.Context(), attempt); err != nil {
			t.Fatal(err)
		}
	}
	if probe.probes != 1 {
		t.Fatalf("probed %d times within one interval", probe.probes)
	}
	if err := (&NativeService{store: verificationStoreFake{}, implementation: probe}).pauseIdleAttempt(t.Context(), attempt); err != nil {
		t.Fatalf("store without recovery support = %v", err)
	}
}

func TestPauseIdleAttemptSurfacesResumeLookupErrors(t *testing.T) {
	store := &watchdogStoreFake{resumeErr: errors.New("db down")}
	s := &NativeService{store: store, implementation: &activityFake{}}
	attempt := model.FactoryAttempt{ID: "att-1", StartedAt: time.Now().Add(-2 * factoryIdleTimeout).UnixMilli()}
	if err := s.pauseIdleAttempt(t.Context(), attempt); err == nil {
		t.Fatal("resume lookup error was swallowed")
	}
}
