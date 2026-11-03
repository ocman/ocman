package factory

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/factory/model"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
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
	d := statetest.Open(t)
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
	svc := NewNativeWithExecution(nil, nil, nil, nil)
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
	d := statetest.Open(t)
	store := &dispatchCountingStore{DB: d}
	svc := NewNativeWithExecution(store, nil, &fakePlanningLauncher{}, &fakeImplementationLauncher{})
	done := make(chan struct{})
	go func() { defer close(done); svc.runDispatch(nil, 100*time.Millisecond) }()
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
			svc := NewNativeWithExecution(&dispatchCountingStore{deadline: tc.next, deadlineErr: tc.err}, nil, nil, nil)
			got := svc.nextDispatchDelay(t.Context(), 5*time.Minute)
			if got < tc.wantMin || got > tc.wantMax {
				t.Fatalf("delay = %v, want [%v,%v]", got, tc.wantMin, tc.wantMax)
			}
		})
	}
	if got := NewNativeWithExecution(&nativeStoreFake{}, nil, nil, nil).nextDispatchDelay(t.Context(), time.Minute); got != time.Minute {
		t.Fatalf("fallback delay = %v", got)
	}
}

func TestFactoryDispatchStopsAndResumes(t *testing.T) {
	store := &dispatchCountingStore{DB: statetest.Open(t), deadline: time.Now().Add(time.Hour)}
	svc := NewNativeWithExecution(store, nil, &fakePlanningLauncher{}, &fakeImplementationLauncher{})
	t.Cleanup(svc.Close)
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
	if err := svc.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	await(1)
	svc.StopDispatch()
	stopped := store.scans.Load()
	svc.wakeDispatch()
	time.Sleep(20 * time.Millisecond)
	if store.scans.Load() != stopped {
		t.Fatal("stopped dispatch still scanned")
	}
	svc.StopDispatch() // idempotent
	svc.ResumeDispatch()
	svc.ResumeDispatch() // one loop only
	await(stopped + 1)
	svc.Close()
	svc.ResumeDispatch()
	closed := store.scans.Load()
	svc.wakeDispatch()
	time.Sleep(20 * time.Millisecond)
	if store.scans.Load() != closed {
		t.Fatal("closed service resumed dispatch")
	}
}

type blockedPromptLauncher struct {
	*fakeImplementationLauncher
	entered chan context.Context
	release chan struct{}
}

func (l *blockedPromptLauncher) PromptImplementationSession(ctx context.Context, _ PlanningSession, _ ImplementationSessionRequest) error {
	l.entered <- ctx
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.release:
		return nil
	}
}

func TestFactoryStopDispatchPreservesAdmittedLaunch(t *testing.T) {
	for _, mode := range []string{"disable", "shutdown", "shutdown during disable"} {
		t.Run(mode, func(t *testing.T) {
			shutdown := mode != "disable"
			db := statetest.Open(t)
			launcher := &blockedPromptLauncher{
				fakeImplementationLauncher: &fakeImplementationLauncher{store: db},
				entered:                    make(chan context.Context, 2), release: make(chan struct{}),
			}
			// Approve two independent Epics without dispatching either yet.
			svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, nil)
			approvedEpic(t, svc, "First admission")
			approvedEpic(t, svc, "Second admission")
			if _, err := svc.SetCapacityPolicy(t.Context(), CapacityPolicy{GlobalCapacity: 2, ProjectCapacity: 2}); err != nil {
				t.Fatal(err)
			}
			svc.implementation = launcher
			t.Cleanup(svc.Close)
			// Release a blocked prompt before cleanup even if an assertion fails.
			t.Cleanup(func() { close(launcher.release) })
			if err := svc.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			var promptCtx context.Context
			select {
			case promptCtx = <-launcher.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("initial prompt did not start")
			}
			done := make(chan struct{})
			halt := svc.dispatchHalt
			go func() {
				defer close(done)
				if mode == "shutdown during disable" {
					stopped := make(chan struct{})
					go func() { svc.StopDispatch(); close(stopped) }()
					<-halt
					svc.Close()
					<-stopped
					return
				}
				if shutdown {
					svc.Close()
				} else {
					svc.StopDispatch()
				}
			}()
			if shutdown {
				select {
				case <-promptCtx.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("shutdown did not cancel the admitted launch")
				}
			} else {
				select {
				case <-halt:
				case <-time.After(5 * time.Second):
					t.Fatal("dispatch stop was not signaled")
				}
				select {
				case <-promptCtx.Done():
					t.Fatal("disabling Factory canceled the admitted initial prompt")
				case <-done:
					t.Fatal("StopDispatch did not join the admitted launch")
				case <-time.After(50 * time.Millisecond):
				}
				launcher.release <- struct{}{}
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("dispatch did not stop")
			}
			attempts, err := db.ListFactoryAttempts(t.Context(), "")
			if err != nil || len(attempts) != 1 {
				t.Fatalf("attempts after stop = %#v, %v; want only the admitted launch", attempts, err)
			}
			if !shutdown && (len(launcher.stops) != 0 || attempts[0].Phase != model.FactoryAttemptActive || attempts[0].Failure.Type != "") {
				t.Fatalf("disable aborted or failed the admitted launch: stops=%v, attempt=%#v", launcher.stops, attempts[0])
			}
		})
	}
}
