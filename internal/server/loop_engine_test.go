package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/loops"
	"github.com/NoUseFreak/ocman/internal/state"
)

// fakeLoopStore is a minimal loops.Store for engine tests: it returns a
// fixed set of active loops and records EvaluateOne dispatch via the
// service built from it.
type fakeLoopStore struct {
	active []state.Loop
}

func (f *fakeLoopStore) InsertLoop(state.Loop) error                    { return nil }
func (f *fakeLoopStore) UpdateLoop(state.Loop) error                    { return nil }
func (f *fakeLoopStore) SetLoopState(string, string, string) error      { return nil }
func (f *fakeLoopStore) GetLoop(id string) (*state.Loop, error)         { return nil, nil }
func (f *fakeLoopStore) ListLoops(string, string) ([]state.Loop, error) { return f.active, nil }
func (f *fakeLoopStore) ListActiveLoops() ([]state.Loop, error)         { return f.active, nil }
func (f *fakeLoopStore) InsertLoopIteration(state.LoopIteration) (int64, error) {
	return 1, nil
}
func (f *fakeLoopStore) UpdateLoopIteration(state.LoopIteration) error            { return nil }
func (f *fakeLoopStore) ListLoopIterations(string) ([]state.LoopIteration, error) { return nil, nil }
func (f *fakeLoopStore) ListChildSessionsByLoop(string) ([]state.ChildSession, error) {
	return nil, nil
}

func TestRunLoopEngine_TicksActiveLoops(t *testing.T) {
	sdb := openWatcherTestStateDB(t)

	// One active schedule loop ready to fire immediately.
	now := time.Now().UnixMilli()
	if err := sdb.InsertLoop(state.Loop{
		ID:             "loop_e1",
		Platform:       "opencode",
		RootSessionID:  "s1",
		TriggerType:    loops.TriggerSchedule,
		TriggerConfig:  `{"interval_seconds":60}`,
		ActionType:     loops.ActionPromptRoot,
		ActionTemplate: "tick",
		StopConditions: `{"max_iterations":10,"max_cost_usd":1}`,
		State:          "active",
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("InsertLoop: %v", err)
	}

	var mu sync.Mutex
	var spawns []string
	// Fresh prompt_root spawns a dedicated session via the launcher.
	launcher := launcherFunc(func(_ context.Context, req loops.SpawnRequest) (string, error) {
		mu.Lock()
		spawns = append(spawns, req.Prompt)
		mu.Unlock()
		return "loopsess_1", nil
	})

	srv := &Server{stateDB: sdb}
	// Override the service builder to use the real domain service over
	// the real stateDB but with fakes (no platform registry).
	prev := loopServiceFn
	loopServiceFn = func(s *Server) *loops.Service {
		return loops.NewService(loops.Deps{Store: sdb, Launcher: launcher})
	}
	t.Cleanup(func() { loopServiceFn = prev })

	srv.loopEngineTick(context.Background(), &inflightSet{m: map[string]bool{}}, make(chan struct{}, 2))

	mu.Lock()
	got := len(spawns)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("expected engine to fire 1 action (spawn), got %d", got)
	}

	l, _ := sdb.GetLoop("loop_e1")
	if l.Iteration != 1 {
		t.Fatalf("expected iteration advanced to 1, got %d", l.Iteration)
	}
}

func TestInflightSet_GuardsReentry(t *testing.T) {
	s := &inflightSet{m: map[string]bool{}}
	if !s.acquire("a") {
		t.Fatal("first acquire should succeed")
	}
	if s.acquire("a") {
		t.Fatal("second acquire while in-flight should fail")
	}
	s.release("a")
	if !s.acquire("a") {
		t.Fatal("acquire after release should succeed")
	}
}

// messengerFunc adapts a func to loops.Messenger.
type messengerFunc func(ctx context.Context, sessionID, prompt, model string) error

func (f messengerFunc) SendPrompt(ctx context.Context, sessionID, prompt, model string) error {
	return f(ctx, sessionID, prompt, model)
}

// launcherFunc adapts a func to loops.Launcher.
type launcherFunc func(ctx context.Context, req loops.SpawnRequest) (string, error)

func (f launcherFunc) Spawn(ctx context.Context, req loops.SpawnRequest) (string, error) {
	return f(ctx, req)
}
