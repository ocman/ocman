package factory

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type dispatchCountingStore struct {
	*state.DB
	scans       atomic.Int32
	deadline    time.Time
	deadlineErr error
}

func (s *dispatchCountingStore) NextFactoryDispatchAt(context.Context, time.Duration) (time.Time, error) {
	return s.deadline, s.deadlineErr
}

func (s *dispatchCountingStore) ListFactoryEpics(ctx context.Context) ([]model.NativeEpic, error) {
	s.scans.Add(1)
	return s.DB.ListFactoryEpics(ctx)
}

func (s *dispatchCountingStore) ListFactoryDispatchEpics(ctx context.Context) ([]model.NativeEpic, error) {
	s.scans.Add(1)
	return s.DB.ListFactoryDispatchEpics(ctx)
}

func TestFactoryDoesNotScanEverySecond(t *testing.T) {
	d, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := &dispatchCountingStore{DB: d}
	svc := NewNativeWithExecution(store, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, &fakeImplementationLauncher{})
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	time.Sleep(2200 * time.Millisecond)
	if scans := store.scans.Load(); scans != 2 {
		t.Fatalf("idle Factory scanned %d times in 2.2s, want startup dispatch and attention scans only", scans)
	}
}

func TestFactoryIdleWakeIsScopedAndCoalesced(t *testing.T) {
	svc := NewNative(nil)
	session := PlanningSession{Platform: "opencode", ID: "implementation"}
	svc.rememberImplementationSessions([]model.FactoryAttempt{{Session: session, Phase: model.FactoryAttemptActive, FrozenPolicy: model.FactoryAttemptPolicy{Profile: "factory-implement/v1"}}})
	for _, platform := range []string{"", "r-other:opencode"} {
		svc.NotifySessionIdle(platform, session.ID)
	}
	svc.NotifySessionIdle("opencode", "ordinary")
	if len(svc.dispatchWake) != 0 {
		t.Fatal("ordinary or wrong-owner idle woke Factory")
	}
	for range 10 {
		svc.NotifySessionIdle(session.Platform, session.ID)
	}
	if len(svc.dispatchWake) != 1 {
		t.Fatal("Factory idle wakes were not coalesced")
	}
	<-svc.dispatchWake
	svc.rememberImplementationSessions(nil)
	svc.NotifySessionIdle(session.Platform, session.ID)
	if len(svc.dispatchWake) != 0 {
		t.Fatal("finished attempt remained tracked")
	}
}

func TestFactoryDispatchRecoveryAndWake(t *testing.T) {
	d, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := &dispatchCountingStore{DB: d}
	svc := NewNativeWithExecution(store, nil, &fakePlanningLauncher{}, &fakeImplementationLauncher{})
	done := make(chan struct{})
	go func() { defer close(done); svc.runDispatch(100 * time.Millisecond) }()
	t.Cleanup(func() { svc.Close(); <-done })
	await := func(want int32) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for store.scans.Load() < want {
			if time.Now().After(deadline) {
				t.Fatalf("only %d scans, want %d", store.scans.Load(), want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	await(1)
	svc.wakeDispatch()
	await(2)
	await(3) // Recovery still runs if no event arrives.
}

func TestFactoryNextDispatchDelay(t *testing.T) {
	for _, tc := range []struct {
		name             string
		next             time.Time
		err              error
		wantMin, wantMax time.Duration
	}{
		{name: "no deadline", wantMin: 5 * time.Minute, wantMax: 5 * time.Minute},
		{name: "retry deadline", next: time.Now().Add(30 * time.Second), wantMin: 25 * time.Second, wantMax: 30 * time.Second},
		{name: "due now", next: time.Now().Add(-time.Second), wantMin: time.Second, wantMax: time.Second},
		{name: "beyond recovery", next: time.Now().Add(time.Hour), wantMin: 5 * time.Minute, wantMax: 5 * time.Minute},
		{name: "read failed", err: errors.New("deadline failed"), wantMin: 15 * time.Second, wantMax: 15 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewNative(&dispatchCountingStore{deadline: tc.next, deadlineErr: tc.err})
			got := svc.nextDispatchDelay(t.Context(), 5*time.Minute)
			if got < tc.wantMin || got > tc.wantMax {
				t.Fatalf("delay = %v, want [%v,%v]", got, tc.wantMin, tc.wantMax)
			}
		})
	}
	if got := NewNative(&nativeStoreFake{}).nextDispatchDelay(t.Context(), time.Minute); got != time.Minute {
		t.Fatalf("fallback delay = %v", got)
	}
}
