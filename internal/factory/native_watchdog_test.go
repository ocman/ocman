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
	nativeRecoveryStore
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
