package loops

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// fakeStatus is a SessionStatusInferer test fake.
type fakeStatus struct {
	running bool
	ok      bool
}

func (f fakeStatus) TurnRunning(_ context.Context, _, _ string) (bool, bool) {
	return f.running, f.ok
}

// fakeForge is a ForgePoller test fake.
type fakeForge struct {
	mu    sync.Mutex
	st    PRState
	err   error
	calls int
}

func (f *fakeForge) PollPR(_ context.Context, _ string, _ int) (PRState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.st, f.err
}

func TestScheduleTrigger_ShouldFire(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name        string
		lastFiredAt int64
		interval    int
		wantFire    bool
	}{
		{"first run fires", 0, 60, true},
		{"within interval throttles", now.UnixMilli(), 3600, false},
		{"after interval fires", now.Add(-2 * time.Hour).UnixMilli(), 3600, true},
		{"sub-floor interval clamped to 60s, still throttled", now.Add(-10 * time.Second).UnixMilli(), 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := state.Loop{LastFiredAt: tt.lastFiredAt}
			tc := TriggerConfig{IntervalSeconds: tt.interval}
			fire, _, _, err := scheduleTrigger{}.ShouldFire(context.Background(), l, tc, now)
			if err != nil {
				t.Fatalf("ShouldFire: %v", err)
			}
			if fire != tt.wantFire {
				t.Fatalf("fire=%v, want %v", fire, tt.wantFire)
			}
		})
	}
}

func TestTurnCompleteTrigger_ShouldFire(t *testing.T) {
	tests := []struct {
		name     string
		status   fakeStatus
		wantFire bool
	}{
		{"idle fires", fakeStatus{running: false, ok: true}, true},
		{"running does not fire", fakeStatus{running: true, ok: true}, false},
		{"status unknown does not fire", fakeStatus{ok: false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trig := turnCompleteTrigger{status: tt.status}
			fire, _, _, err := trig.ShouldFire(context.Background(), state.Loop{}, TriggerConfig{}, time.Now())
			if err != nil {
				t.Fatalf("ShouldFire: %v", err)
			}
			if fire != tt.wantFire {
				t.Fatalf("fire=%v, want %v", fire, tt.wantFire)
			}
		})
	}
}

func TestChildCompleteTrigger_ShouldFire(t *testing.T) {
	tests := []struct {
		name     string
		status   fakeStatus
		wantFire bool
	}{
		{"idle fires", fakeStatus{running: false, ok: true}, true},
		{"running does not fire", fakeStatus{running: true, ok: true}, false},
		{"status unknown does not fire", fakeStatus{ok: false}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trig := childCompleteTrigger{status: tt.status}
			fire, _, _, err := trig.ShouldFire(context.Background(), state.Loop{}, TriggerConfig{}, time.Now())
			if err != nil {
				t.Fatalf("ShouldFire: %v", err)
			}
			if fire != tt.wantFire {
				t.Fatalf("fire=%v, want %v", fire, tt.wantFire)
			}
		})
	}
}

func TestPREventTrigger_ShouldFire(t *testing.T) {
	now := time.Now()
	t.Run("nil forge errors", func(t *testing.T) {
		trig := prEventTrigger{forge: nil}
		if _, _, _, err := trig.ShouldFire(context.Background(), state.Loop{}, TriggerConfig{PRNumber: 1}, now); err == nil {
			t.Fatal("expected error for nil forge")
		}
	})

	t.Run("missing pr_number errors", func(t *testing.T) {
		trig := prEventTrigger{forge: &fakeForge{}}
		if _, _, _, err := trig.ShouldFire(context.Background(), state.Loop{}, TriggerConfig{}, now); err == nil {
			t.Fatal("expected error for missing pr_number")
		}
	})

	t.Run("throttled within poll interval", func(t *testing.T) {
		forge := &fakeForge{}
		trig := prEventTrigger{forge: forge}
		l := state.Loop{LastFiredAt: now.UnixMilli()}
		fire, _, _, err := trig.ShouldFire(context.Background(), l, TriggerConfig{PRNumber: 1, PollSeconds: 3600}, now)
		if err != nil {
			t.Fatalf("ShouldFire: %v", err)
		}
		if fire {
			t.Fatal("expected throttle, got fire")
		}
		if forge.calls != 0 {
			t.Fatalf("expected no poll while throttled, got %d", forge.calls)
		}
	})

	t.Run("poll error propagates", func(t *testing.T) {
		forge := &fakeForge{err: errBoom}
		trig := prEventTrigger{forge: forge}
		if _, _, _, err := trig.ShouldFire(context.Background(), state.Loop{}, TriggerConfig{PRNumber: 1}, now); err == nil {
			t.Fatal("expected poll error to propagate")
		}
	})

	t.Run("first-seen baseline persists without firing", func(t *testing.T) {
		forge := &fakeForge{st: PRState{HeadSHA: "abc"}}
		trig := prEventTrigger{forge: forge}
		fire, _, newCfg, err := trig.ShouldFire(context.Background(), state.Loop{}, TriggerConfig{PRNumber: 1}, now)
		if err != nil {
			t.Fatalf("ShouldFire: %v", err)
		}
		if fire {
			t.Fatal("first-seen head SHA must not fire")
		}
		if newCfg == nil || newCfg.LastHeadSHA != "abc" {
			t.Fatalf("expected baseline head SHA persisted, got %+v", newCfg)
		}
	})

	t.Run("new commits fire", func(t *testing.T) {
		forge := &fakeForge{st: PRState{HeadSHA: "def"}}
		trig := prEventTrigger{forge: forge}
		fire, detail, newCfg, err := trig.ShouldFire(context.Background(), state.Loop{}, TriggerConfig{PRNumber: 7, LastHeadSHA: "abc"}, now)
		if err != nil {
			t.Fatalf("ShouldFire: %v", err)
		}
		if !fire {
			t.Fatal("expected fire on new commits")
		}
		if newCfg == nil || newCfg.LastHeadSHA != "def" {
			t.Fatalf("expected head SHA updated, got %+v", newCfg)
		}
		if detail == "" {
			t.Fatal("expected a detail string")
		}
	})

	t.Run("new comment and merge fire", func(t *testing.T) {
		forge := &fakeForge{st: PRState{LatestComment: 5, Merged: true}}
		trig := prEventTrigger{forge: forge}
		fire, _, newCfg, err := trig.ShouldFire(context.Background(), state.Loop{}, TriggerConfig{PRNumber: 1, SeenCommentID: 2}, now)
		if err != nil {
			t.Fatalf("ShouldFire: %v", err)
		}
		if !fire {
			t.Fatal("expected fire on new comment + merge")
		}
		if newCfg == nil || newCfg.SeenCommentID != 5 || !newCfg.Merged {
			t.Fatalf("expected comment id + merged persisted, got %+v", newCfg)
		}
	})

	t.Run("no change does not fire", func(t *testing.T) {
		forge := &fakeForge{st: PRState{HeadSHA: "abc", LatestComment: 2, Merged: true}}
		trig := prEventTrigger{forge: forge}
		fire, _, newCfg, err := trig.ShouldFire(context.Background(), state.Loop{},
			TriggerConfig{PRNumber: 1, LastHeadSHA: "abc", SeenCommentID: 2, Merged: true}, now)
		if err != nil {
			t.Fatalf("ShouldFire: %v", err)
		}
		if fire {
			t.Fatal("expected no fire when nothing changed")
		}
		if newCfg != nil {
			t.Fatalf("expected no config mutation, got %+v", newCfg)
		}
	})
}

func TestJoinReasons(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"new commits"}, "new commits"},
		{[]string{"new commits", "merged"}, "new commits, merged"},
		{[]string{"a", "b", "c"}, "a, b, c"},
	}
	for _, tt := range tests {
		if got := joinReasons(tt.in); got != tt.want {
			t.Fatalf("joinReasons(%v)=%q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTriggerFor_UnknownType(t *testing.T) {
	if _, err := triggerFor("bogus", nil, nil); err == nil {
		t.Fatal("expected error for unknown trigger type")
	}
}

func TestPerformAction_PromptChild(t *testing.T) {
	store := newMemStore()
	msg := &fakeMessenger{}
	svc := newService(store, msg)
	l := state.Loop{ID: "l1", ActionType: ActionPromptChild}

	t.Run("missing target errors", func(t *testing.T) {
		if _, err := svc.performAction(context.Background(), l, TriggerConfig{}, "p"); err == nil {
			t.Fatal("expected error for missing target_session_id")
		}
	})

	t.Run("prompts target", func(t *testing.T) {
		res, err := svc.performAction(context.Background(), l, TriggerConfig{TargetSessionID: "child1"}, "do it")
		if err != nil {
			t.Fatalf("performAction: %v", err)
		}
		if res.TargetSessionID != "child1" {
			t.Fatalf("expected target child1, got %q", res.TargetSessionID)
		}
		if len(msg.prompts) != 1 || msg.prompts[0] != "do it" {
			t.Fatalf("expected prompt sent, got %v", msg.prompts)
		}
	})
}

func TestPerformAction_SpawnChildAndWorktree(t *testing.T) {
	for _, at := range []string{ActionSpawnChild, ActionSpawnWorktree} {
		t.Run(at, func(t *testing.T) {
			store := newMemStore()
			launcher := &fakeLauncher{}
			svc := newServiceFull(store, &fakeMessenger{}, launcher)
			l := state.Loop{ID: "l1", ActionType: at, RootSessionID: "root"}
			res, err := svc.performAction(context.Background(), l, TriggerConfig{}, "spawn it")
			if err != nil {
				t.Fatalf("performAction: %v", err)
			}
			if res.ChildSessionID == "" {
				t.Fatal("expected a child session id")
			}
			if len(launcher.spawns) != 1 {
				t.Fatalf("expected 1 spawn, got %d", len(launcher.spawns))
			}
			wantWorktree := at == ActionSpawnWorktree
			if launcher.spawns[0].Worktree != wantWorktree {
				t.Fatalf("worktree=%v, want %v", launcher.spawns[0].Worktree, wantWorktree)
			}
		})
	}
}

func TestPerformAction_SpawnNoLauncher(t *testing.T) {
	svc := NewService(Deps{Store: newMemStore()})
	l := state.Loop{ID: "l1", ActionType: ActionSpawnChild}
	if _, err := svc.performAction(context.Background(), l, TriggerConfig{}, "x"); err == nil {
		t.Fatal("expected error when no launcher configured")
	}
}

func TestPerformAction_UnknownType(t *testing.T) {
	svc := newService(newMemStore(), &fakeMessenger{})
	l := state.Loop{ID: "l1", ActionType: "bogus"}
	if _, err := svc.performAction(context.Background(), l, TriggerConfig{}, "x"); err == nil {
		t.Fatal("expected error for unknown action type")
	}
}

func TestList_ReturnsViews(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	_ = store.InsertLoop(sampleLoopState("a", ""))
	_ = store.InsertLoop(sampleLoopState("b", ""))

	views, err := svc.List(context.Background(), LoopFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 loops, got %d", len(views))
	}
	// Decoded stop conditions should be populated from the stored JSON.
	if views[0].StopConditionsDecoded.MaxIterations == 0 {
		t.Fatal("expected decoded stop conditions in view")
	}
}

func TestGet_AssemblesDetail(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	_ = store.InsertLoop(sampleLoopState("parent", ""))
	_ = store.InsertLoop(sampleLoopState("sub", "parent"))
	_, _ = store.InsertLoopIteration(state.LoopIteration{LoopID: "parent", Seq: 1, Outcome: "ok"})
	store.kids = []state.ChildSession{{ID: "k1", LoopID: "parent"}}

	d, err := svc.Get(context.Background(), "parent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.ID != "parent" {
		t.Fatalf("expected parent loop, got %q", d.ID)
	}
	if len(d.Iterations) != 1 {
		t.Fatalf("expected 1 iteration, got %d", len(d.Iterations))
	}
	if len(d.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(d.Children))
	}
	if len(d.SubLoops) != 1 || d.SubLoops[0].ID != "sub" {
		t.Fatalf("expected 1 sub-loop 'sub', got %+v", d.SubLoops)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newService(newMemStore(), &fakeMessenger{})
	if _, err := svc.Get(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for missing loop")
	}
}

func TestStep_RunsOnceThenPauses(t *testing.T) {
	store := newMemStore()
	launcher := &fakeLauncher{}
	svc := newServiceFull(store, &fakeMessenger{}, launcher)
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 3600},
		ActionType:     ActionPromptRoot,
		ActionTemplate: "step {{iteration}}",
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	// Mark recently fired so only the manual Step (not the schedule) drives it.
	l, _ := store.GetLoop(v.ID)
	l.LastFiredAt = time.Now().UnixMilli()
	_ = store.UpdateLoop(*l)

	if err := svc.Step(context.Background(), v.ID); err != nil {
		t.Fatalf("Step: %v", err)
	}
	l2, _ := store.GetLoop(v.ID)
	if l2.State != StatePaused {
		t.Fatalf("expected paused after Step, got %s", l2.State)
	}
}

func TestStep_NotFound(t *testing.T) {
	svc := newService(newMemStore(), &fakeMessenger{})
	if err := svc.Step(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for missing loop")
	}
}
