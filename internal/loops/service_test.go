package loops

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

var errNoLoop = errors.New("loop not found")

// memStore is an in-memory Store for domain tests.
type memStore struct {
	mu    sync.Mutex
	loops map[string]state.Loop
	iters []state.LoopIteration
	kids  []state.ChildSession
	nextI int64
}

func newMemStore() *memStore {
	return &memStore{loops: map[string]state.Loop{}}
}

func (m *memStore) InsertLoop(l state.Loop) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loops[l.ID] = l
	return nil
}
func (m *memStore) UpdateLoop(l state.Loop) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loops[l.ID] = l
	return nil
}
func (m *memStore) SetLoopState(id, st, summary string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.loops[id]
	l.State = st
	if summary != "" {
		l.LastSummary = summary
	}
	m.loops[id] = l
	return nil
}
func (m *memStore) GetLoop(id string) (*state.Loop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.loops[id]
	if !ok {
		return nil, errNoLoop
	}
	cp := l
	return &cp, nil
}
func (m *memStore) ListLoops(root, dir string) ([]state.Loop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []state.Loop
	for _, l := range m.loops {
		out = append(out, l)
	}
	return out, nil
}
func (m *memStore) ListActiveLoops() ([]state.Loop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []state.Loop
	for _, l := range m.loops {
		if l.State == StateActive {
			out = append(out, l)
		}
	}
	return out, nil
}
func (m *memStore) InsertLoopIteration(it state.LoopIteration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextI++
	it.ID = m.nextI
	m.iters = append(m.iters, it)
	return it.ID, nil
}
func (m *memStore) UpdateLoopIteration(it state.LoopIteration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.iters {
		if m.iters[i].ID == it.ID {
			m.iters[i].Outcome = it.Outcome
			m.iters[i].Summary = it.Summary
			m.iters[i].TargetSessionID = it.TargetSessionID
			m.iters[i].ChildSessionID = it.ChildSessionID
			m.iters[i].CompletedAt = it.CompletedAt
		}
	}
	return nil
}
func (m *memStore) ListLoopIterations(id string) ([]state.LoopIteration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []state.LoopIteration
	for _, it := range m.iters {
		if it.LoopID == id {
			out = append(out, it)
		}
	}
	return out, nil
}
func (m *memStore) ListChildSessionsByLoop(id string) ([]state.ChildSession, error) {
	var out []state.ChildSession
	for _, c := range m.kids {
		// A zero LoopID kid matches any loop (back-compat with tests that
		// don't set it); otherwise filter by the requested loop id.
		if c.LoopID == "" || c.LoopID == id {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memStore) ListLoopsByParent(parentID string) ([]state.Loop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []state.Loop
	for _, l := range m.loops {
		if l.ParentLoopID == parentID {
			out = append(out, l)
		}
	}
	return out, nil
}

// fakeMessenger records prompts sent.
type fakeMessenger struct {
	mu      sync.Mutex
	prompts []string
	models  []string
	fail    bool
}

func (f *fakeMessenger) SendPrompt(_ context.Context, _ string, p, model string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errBoom
	}
	f.prompts = append(f.prompts, p)
	f.models = append(f.models, model)
	return nil
}

var errBoom = &boomErr{}

type boomErr struct{}

func (*boomErr) Error() string { return "boom" }

// fakeLauncher records spawn requests and returns incrementing session
// ids. prompt_root spawns a dedicated session via the launcher (OQ5).
type fakeLauncher struct {
	mu     sync.Mutex
	spawns []SpawnRequest
	n      int
	fail   bool
}

func (f *fakeLauncher) Spawn(_ context.Context, req SpawnRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return "", errBoom
	}
	f.spawns = append(f.spawns, req)
	f.n++
	return fmt.Sprintf("loopsess_%d", f.n), nil
}

// newService wires a messenger AND a launcher: prompt_root needs the
// launcher to create the dedicated loop session, the messenger to
// re-prompt it in reuse mode.
func newService(store Store, msg Messenger) *Service {
	return NewService(Deps{Store: store, Messenger: msg, Launcher: &fakeLauncher{}})
}

func newServiceFull(store Store, msg Messenger, launcher Launcher) *Service {
	return NewService(Deps{Store: store, Messenger: msg, Launcher: launcher})
}

func TestStop_MaxIterations_NoAction(t *testing.T) {
	sc := StopConditions{MaxIterations: 2, MaxCostUSD: 5}
	l := state.Loop{Iteration: 2, CreatedAt: time.Now().UnixMilli()}
	dec := evaluateStop(l, sc, time.Now())
	if !dec.Stop || dec.TerminalState != StateCompleted {
		t.Fatalf("expected stop at max iterations, got %+v", dec)
	}
}

func TestStop_CostBudget(t *testing.T) {
	sc := StopConditions{MaxIterations: 100, MaxCostUSD: 5}
	l := state.Loop{Iteration: 1, CostUSD: 5.01, CreatedAt: time.Now().UnixMilli()}
	dec := evaluateStop(l, sc, time.Now())
	if !dec.Stop {
		t.Fatalf("expected stop at cost budget")
	}
}

func TestStop_ErrorStreak(t *testing.T) {
	sc := StopConditions{MaxIterations: 100, MaxCostUSD: 5, ErrorStreak: 3}
	l := state.Loop{ErrorStreak: 3, CreatedAt: time.Now().UnixMilli()}
	dec := evaluateStop(l, sc, time.Now())
	if !dec.Stop || dec.TerminalState != StateErrored {
		t.Fatalf("expected errored stop, got %+v", dec)
	}
}

func TestStop_Duration(t *testing.T) {
	sc := StopConditions{MaxIterations: 100, MaxCostUSD: 5, MaxDuration: "1h"}
	created := time.Now().Add(-2 * time.Hour)
	l := state.Loop{CreatedAt: created.UnixMilli()}
	dec := evaluateStop(l, sc, time.Now())
	if !dec.Stop {
		t.Fatalf("expected stop after duration")
	}
}

func TestStop_Duration_RecurringExempt(t *testing.T) {
	// Cron/schedule/pr_event loops are exempt from the lifetime duration
	// cap: a daily cron created hours ago and fired since must keep going.
	sc := StopConditions{MaxIterations: 100, MaxCostUSD: 5, MaxDuration: "1h"}
	now := time.Now()
	for _, tt := range []string{TriggerCron, TriggerSchedule, TriggerPREvent} {
		l := state.Loop{
			TriggerType: tt,
			CreatedAt:   now.Add(-5 * time.Hour).UnixMilli(),
			LastFiredAt: now.Add(-2 * time.Hour).UnixMilli(),
		}
		if dec := evaluateStop(l, sc, now); dec.Stop {
			t.Fatalf("recurring trigger %q should be exempt from lifetime duration, got %+v", tt, dec)
		}
	}
}

func TestStop_Duration_OneShotStillCapped(t *testing.T) {
	// One-shot triggers (child_complete, turn_complete) keep the lifetime cap.
	sc := StopConditions{MaxIterations: 100, MaxCostUSD: 5, MaxDuration: "1h"}
	now := time.Now()
	for _, tt := range []string{TriggerChildComplete, TriggerTurnComplete} {
		l := state.Loop{TriggerType: tt, CreatedAt: now.Add(-2 * time.Hour).UnixMilli()}
		if dec := evaluateStop(l, sc, now); !dec.Stop {
			t.Fatalf("one-shot trigger %q should stop on lifetime duration", tt)
		}
	}
}

func strptr(s string) *string { return &s }

func TestUpdate_EditsSafeFields(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		ActionTemplate: "old",
		Model:          "anthropic/claude-haiku-4-5",
		SessionMode:    SessionModeFresh,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})

	out, err := svc.Update(context.Background(), v.ID, LoopUpdate{
		ActionTemplate: strptr("new template"),
		Model:          strptr("openai/gpt-5.5"),
		SessionMode:    strptr(SessionModeReuse),
		TriggerConfig:  &TriggerConfig{IntervalSeconds: 1800},
		StopConditions: &StopConditions{MaxIterations: 20, MaxCostUSD: 3},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if out.ActionTemplate != "new template" {
		t.Fatalf("template not updated: %q", out.ActionTemplate)
	}
	if out.Model != "openai/gpt-5.5" {
		t.Fatalf("model not updated: %q", out.Model)
	}
	if out.SessionMode != SessionModeReuse {
		t.Fatalf("session mode not updated: %q", out.SessionMode)
	}
	if out.TriggerConfigDecoded.IntervalSeconds != 1800 {
		t.Fatalf("interval not updated: %d", out.TriggerConfigDecoded.IntervalSeconds)
	}
	if out.StopConditionsDecoded.MaxIterations != 20 {
		t.Fatalf("max iterations not updated: %d", out.StopConditionsDecoded.MaxIterations)
	}
}

func TestUpdate_RejectsBudgetRemoval(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	_, err := svc.Update(context.Background(), v.ID, LoopUpdate{
		StopConditions: &StopConditions{MaxIterations: 5}, // no budget
	})
	if err == nil {
		t.Fatal("expected Update to reject removing the budget")
	}
}

func TestUpdate_RejectsTerminalLoop(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	_ = store.SetLoopState(v.ID, StateCompleted, "done")
	if _, err := svc.Update(context.Background(), v.ID, LoopUpdate{Title: strptr("x")}); err == nil {
		t.Fatal("expected Update to reject a terminal loop")
	}
}

// fakeUsage sums fixed per-session values for the ids it's given.
type fakeUsage struct {
	perSessionCost   float64
	perSessionTokens int64
	lastIDs          []string
	ok               bool
}

func (f *fakeUsage) SessionUsage(_ context.Context, ids []string) (int64, float64, bool) {
	f.lastIDs = ids
	if !f.ok {
		return 0, 0, false
	}
	return int64(len(ids)) * f.perSessionTokens, float64(len(ids)) * f.perSessionCost, true
}

func TestRefreshUsage_AggregatesTreeIncludingSubLoops(t *testing.T) {
	store := newMemStore()
	usage := &fakeUsage{perSessionCost: 1.5, perSessionTokens: 100, ok: true}
	svc := NewService(Deps{Store: store, Messenger: &fakeMessenger{}, Launcher: &fakeLauncher{}, Usage: usage})

	// Parent loop with one child session.
	parent := sampleLoopState("parent", "")
	sub := sampleLoopState("sub", "parent")
	_ = store.InsertLoop(parent)
	_ = store.InsertLoop(sub)
	store.kids = []state.ChildSession{
		{ID: "sess_p1", LoopID: "parent"},
		{ID: "sess_s1", LoopID: "sub"},
		{ID: "sess_s2", LoopID: "sub"},
	}

	l, _ := store.GetLoop("parent")
	svc.refreshUsage(context.Background(), l)

	// 3 sessions across the tree (1 parent + 2 sub).
	if len(usage.lastIDs) != 3 {
		t.Fatalf("expected 3 session ids in tree, got %d: %v", len(usage.lastIDs), usage.lastIDs)
	}
	if l.CostUSD != 4.5 || l.TokensUsed != 300 {
		t.Fatalf("expected rolled-up cost 4.5/tokens 300, got %v/%d", l.CostUSD, l.TokensUsed)
	}
}

func TestRefreshUsage_ExcludesSessionsBeforeBaseline(t *testing.T) {
	store := newMemStore()
	usage := &fakeUsage{perSessionCost: 2, perSessionTokens: 50, ok: true}
	svc := NewService(Deps{Store: store, Messenger: &fakeMessenger{}, Launcher: &fakeLauncher{}, Usage: usage})

	loop := sampleLoopState("l1", "")
	loop.UsageBaselineAt = 1000 // only sessions created at/after 1000 count
	_ = store.InsertLoop(loop)
	store.kids = []state.ChildSession{
		{ID: "old", LoopID: "l1", CreatedAt: 500},  // before baseline → excluded
		{ID: "new1", LoopID: "l1", CreatedAt: 1000}, // at baseline → counted
		{ID: "new2", LoopID: "l1", CreatedAt: 1500}, // after baseline → counted
	}

	l, _ := store.GetLoop("l1")
	svc.refreshUsage(context.Background(), l)

	if len(usage.lastIDs) != 2 {
		t.Fatalf("expected 2 post-baseline sessions, got %d: %v", len(usage.lastIDs), usage.lastIDs)
	}
	if l.CostUSD != 4 { // 2 sessions * $2
		t.Fatalf("expected cost 4 (excluding pre-baseline), got %v", l.CostUSD)
	}
}

func TestRestart_ResetsBudgetBaselineSoOldSpendDoesNotReTrip(t *testing.T) {
	store := newMemStore()
	// Each session costs $6 — one prior-run session already exceeds the $5 cap.
	usage := &fakeUsage{perSessionCost: 6, perSessionTokens: 0, ok: true}
	now := int64(10_000)
	svc := NewService(Deps{
		Store: store, Messenger: &fakeMessenger{}, Launcher: &fakeLauncher{}, Usage: usage,
		Notify: nil,
	})
	svc.now = func() time.Time { return time.UnixMilli(now) }

	loop := sampleLoopState("l1", "")
	loop.StopConditions = `{"max_iterations":48,"max_cost_usd":5}`
	_ = store.InsertLoop(loop)
	// A completed run whose single session blew the budget.
	store.kids = []state.ChildSession{{ID: "old", LoopID: "l1", CreatedAt: 1000}}
	_ = store.SetLoopState("l1", StateCompleted, "reached cost budget ($5.00)")

	// Restart must succeed (not instantly re-trip on the old $6 session).
	if _, err := svc.Restart(context.Background(), "l1"); err != nil {
		t.Fatalf("Restart should succeed by moving the budget baseline: %v", err)
	}
	got, _ := store.GetLoop("l1")
	if got.State != StateActive {
		t.Fatalf("expected active after restart, got %q", got.State)
	}
	if got.UsageBaselineAt != now {
		t.Fatalf("expected usage baseline set to now (%d), got %d", now, got.UsageBaselineAt)
	}

	// On the next tick, the old session is excluded, so usage is $0 and a
	// fresh evaluation does not immediately stop on budget.
	l, _ := store.GetLoop("l1")
	svc.refreshUsage(context.Background(), l)
	if l.CostUSD != 0 {
		t.Fatalf("expected $0 against the new baseline, got %v", l.CostUSD)
	}
}

func TestRefreshUsage_UnavailableKeepsCachedValue(t *testing.T) {
	store := newMemStore()
	svc := NewService(Deps{Store: store, Usage: &fakeUsage{ok: false}})
	_ = store.InsertLoop(sampleLoopState("p", ""))
	store.kids = []state.ChildSession{{ID: "s1", LoopID: "p"}}
	l, _ := store.GetLoop("p")
	l.CostUSD = 9.9
	svc.refreshUsage(context.Background(), l)
	if l.CostUSD != 9.9 {
		t.Fatalf("expected cached cost preserved when usage unavailable, got %v", l.CostUSD)
	}
}

// sampleLoopState builds a minimal stored loop for usage tests.
func sampleLoopState(id, parent string) state.Loop {
	return state.Loop{
		ID: id, Platform: "opencode", RootSessionID: "root_" + id,
		ParentLoopID: parent, TriggerType: TriggerSchedule, ActionType: ActionPromptRoot,
		StopConditions: `{"max_iterations":10,"max_cost_usd":50}`, State: StateActive,
	}
}

func TestPauseResume_RoundTrip(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 10, MaxCostUSD: 1},
	})
	if err := svc.Pause(context.Background(), v.ID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if l, _ := store.GetLoop(v.ID); l.State != StatePaused {
		t.Fatalf("expected paused, got %s", l.State)
	}
	if err := svc.Resume(context.Background(), v.ID); err != nil {
		t.Fatalf("Resume paused loop: %v", err)
	}
	if l, _ := store.GetLoop(v.ID); l.State != StateActive {
		t.Fatalf("expected active after resume, got %s", l.State)
	}
}

func TestDelete_SoftDeletesAndHidesFromList(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	if err := svc.Delete(context.Background(), v.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	l, _ := store.GetLoop(v.ID)
	if l.State != StateDeleted {
		t.Fatalf("expected deleted state, got %s", l.State)
	}
	// A deleted loop can't be resumed.
	if err := svc.Resume(context.Background(), v.ID); err == nil {
		t.Fatal("expected resume to reject a deleted loop")
	}
}

func TestResume_RejectsCompletedLoop(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	_ = store.SetLoopState(v.ID, StateCompleted, "done")
	if err := svc.Resume(context.Background(), v.ID); err == nil {
		t.Fatal("expected resume to reject a completed loop")
	}
}

func TestResume_RejectsWhenWouldImmediatelyStop(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 3, MaxCostUSD: 1},
	})
	// Drive it to the iteration cap, then pause it.
	l, _ := store.GetLoop(v.ID)
	l.Iteration = 3
	_ = store.UpdateLoop(*l)
	_ = store.SetLoopState(v.ID, StatePaused, "")

	if err := svc.Resume(context.Background(), v.ID); err == nil {
		t.Fatal("expected resume to reject a loop already at its iteration cap")
	}
}

func TestRestart_RevivesCompletedLoopAndResetsCounters(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 10},
	})
	// Drive it to a terminal state with consumed run-state.
	l, _ := store.GetLoop(v.ID)
	l.Iteration = 5
	l.ErrorStreak = 2
	l.CostUSD = 7.5
	l.TokensUsed = 1234
	l.LastFiredAt = 999
	_ = store.UpdateLoop(*l)
	_ = store.SetLoopState(v.ID, StateCompleted, "hit cap")

	view, err := svc.Restart(context.Background(), v.ID)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if view.State != StateActive {
		t.Fatalf("expected active after restart, got %q", view.State)
	}
	got, _ := store.GetLoop(v.ID)
	if got.Iteration != 0 || got.ErrorStreak != 0 || got.CostUSD != 0 || got.TokensUsed != 0 {
		t.Fatalf("expected run-state reset, got iter=%d streak=%d cost=%v tokens=%d",
			got.Iteration, got.ErrorStreak, got.CostUSD, got.TokensUsed)
	}
	if got.CompletedAt != 0 || got.LastFiredAt != 0 {
		t.Fatalf("expected completedAt/lastFiredAt cleared, got %d/%d", got.CompletedAt, got.LastFiredAt)
	}
}

func TestRestart_RejectsActiveAndPausedAndDeleted(t *testing.T) {
	for _, st := range []string{StateActive, StatePaused, StateDeleted} {
		store := newMemStore()
		svc := newService(store, &fakeMessenger{})
		v, _ := svc.Create(context.Background(), LoopSpec{
			RootSessionID:  "s1",
			TriggerType:    TriggerSchedule,
			ActionType:     ActionPromptRoot,
			StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
		})
		_ = store.SetLoopState(v.ID, st, "")
		if _, err := svc.Restart(context.Background(), v.ID); err == nil {
			t.Fatalf("expected restart to reject state=%s", st)
		}
	}
}

func TestCreate_RejectsNoBudget(t *testing.T) {
	svc := newService(newMemStore(), &fakeMessenger{})
	_, err := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 10},
	})
	if err == nil {
		t.Fatal("expected create to reject loop without budget")
	}
}

// fakeDirResolver maps a session ID to a directory for backfill tests.
type fakeDirResolver struct{ dirs map[string]string }

func (f *fakeDirResolver) SessionDir(_ context.Context, sessionID string) (string, bool) {
	d, ok := f.dirs[sessionID]
	return d, ok
}

func TestCreate_BackfillsDirectoryFromRootSession(t *testing.T) {
	store := newMemStore()
	svc := NewService(Deps{
		Store:    store,
		Launcher: &fakeLauncher{},
		Dirs:     &fakeDirResolver{dirs: map[string]string{"s1": "/repo/e2e-suite"}},
	})
	v, err := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerCron,
		TriggerConfig:  TriggerConfig{CronExpr: "* * * * *"},
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
		// Directory intentionally omitted (MCP create_loop allows this).
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v.Directory != "/repo/e2e-suite" {
		t.Fatalf("expected directory backfilled to /repo/e2e-suite, got %q", v.Directory)
	}
}

func TestCreate_KeepsExplicitDirectory(t *testing.T) {
	store := newMemStore()
	svc := NewService(Deps{
		Store:    store,
		Launcher: &fakeLauncher{},
		Dirs:     &fakeDirResolver{dirs: map[string]string{"s1": "/repo/other"}},
	})
	v, err := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		Directory:      "/repo/explicit",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v.Directory != "/repo/explicit" {
		t.Fatalf("explicit directory must be preserved, got %q", v.Directory)
	}
}

func TestCreate_AnchorsToDirectoryWithoutRootSession(t *testing.T) {
	store := newMemStore()
	svc := NewService(Deps{Store: store, Launcher: &fakeLauncher{}})
	v, err := svc.Create(context.Background(), LoopSpec{
		Directory:      "/repo/project-anchored",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v.RootSessionID != "" {
		t.Fatalf("expected no root session, got %q", v.RootSessionID)
	}
	if v.Directory != "/repo/project-anchored" {
		t.Fatalf("expected directory anchor, got %q", v.Directory)
	}
}

func TestCreate_RejectsNoAnchor(t *testing.T) {
	svc := newService(newMemStore(), &fakeMessenger{})
	_, err := svc.Create(context.Background(), LoopSpec{
		TriggerType:    TriggerSchedule,
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	if err == nil {
		t.Fatal("expected create to reject a loop with neither root session nor directory")
	}
}

func TestCreate_RejectsTurnCompleteWithoutRootSession(t *testing.T) {
	svc := newService(newMemStore(), &fakeMessenger{})
	_, err := svc.Create(context.Background(), LoopSpec{
		Directory:      "/repo/x",
		TriggerType:    TriggerTurnComplete,
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	if err == nil {
		t.Fatal("expected turn_complete without a root session to be rejected")
	}
}

func TestEvaluateOne_ScheduleFiresAndAdvances(t *testing.T) {
	store := newMemStore()
	launcher := &fakeLauncher{}
	svc := newServiceFull(store, &fakeMessenger{}, launcher)
	v, err := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		ActionTemplate: "heartbeat {{iteration}}",
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Default session mode is fresh: prompt_root spawns a dedicated session.
	if v.SessionMode != SessionModeFresh {
		t.Fatalf("expected default fresh session mode, got %q", v.SessionMode)
	}
	l, _ := store.GetLoop(v.ID)
	advanced, err := svc.EvaluateOne(context.Background(), *l)
	if err != nil {
		t.Fatalf("EvaluateOne: %v", err)
	}
	if !advanced {
		t.Fatal("expected first schedule run to fire")
	}
	// Fresh mode spawns a dedicated session with the rendered prompt,
	// NOT the creator session "s1".
	if len(launcher.spawns) != 1 {
		t.Fatalf("expected 1 spawn, got %d", len(launcher.spawns))
	}
	if launcher.spawns[0].Prompt != "heartbeat 1" {
		t.Fatalf("expected rendered prompt, got %q", launcher.spawns[0].Prompt)
	}
	if launcher.spawns[0].ParentSession != "s1" {
		t.Fatalf("expected creator session as parent, got %q", launcher.spawns[0].ParentSession)
	}
	l2, _ := store.GetLoop(v.ID)
	if l2.Iteration != 1 {
		t.Fatalf("expected iteration 1, got %d", l2.Iteration)
	}
	if l2.LoopSessionID == "" {
		t.Fatal("expected the dedicated loop session id to be recorded")
	}
	// Immediate re-eval should NOT fire (within interval).
	advanced, _ = svc.EvaluateOne(context.Background(), *l2)
	if advanced {
		t.Fatal("expected schedule to throttle within interval")
	}
}

func TestEvaluateOne_FreshSpawnsEachIteration(t *testing.T) {
	store := newMemStore()
	launcher := &fakeLauncher{}
	svc := newServiceFull(store, &fakeMessenger{}, launcher)
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		SessionMode:    SessionModeFresh,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	// Two forced fires (TriggerNow bypasses the interval).
	if err := svc.TriggerNow(context.Background(), v.ID); err != nil {
		t.Fatalf("TriggerNow 1: %v", err)
	}
	if err := svc.TriggerNow(context.Background(), v.ID); err != nil {
		t.Fatalf("TriggerNow 2: %v", err)
	}
	if len(launcher.spawns) != 2 {
		t.Fatalf("fresh mode should spawn a new session each iteration, got %d", len(launcher.spawns))
	}
}

func TestEvaluateOne_ReuseSpawnsOnceThenReprompts(t *testing.T) {
	store := newMemStore()
	launcher := &fakeLauncher{}
	msg := &fakeMessenger{}
	svc := newServiceFull(store, msg, launcher)
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		SessionMode:    SessionModeReuse,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	if v.SessionMode != SessionModeReuse {
		t.Fatalf("expected reuse mode, got %q", v.SessionMode)
	}
	for i := 0; i < 3; i++ {
		if err := svc.TriggerNow(context.Background(), v.ID); err != nil {
			t.Fatalf("TriggerNow %d: %v", i, err)
		}
	}
	// Reuse spawns the dedicated session once, then re-prompts it.
	if len(launcher.spawns) != 1 {
		t.Fatalf("reuse should spawn once, got %d", len(launcher.spawns))
	}
	if len(msg.prompts) != 2 {
		t.Fatalf("reuse should re-prompt the session on subsequent fires, got %d", len(msg.prompts))
	}
}

func TestEvaluateOne_StopBeforeAction(t *testing.T) {
	store := newMemStore()
	msg := &fakeMessenger{}
	svc := newService(store, msg)
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 1, MaxCostUSD: 1},
	})
	l, _ := store.GetLoop(v.ID)
	l.Iteration = 1 // already at cap
	_ = store.UpdateLoop(*l)
	l, _ = store.GetLoop(v.ID)

	advanced, err := svc.EvaluateOne(context.Background(), *l)
	if err != nil {
		t.Fatalf("EvaluateOne: %v", err)
	}
	if advanced {
		t.Fatal("expected no action when at iteration cap")
	}
	if len(msg.prompts) != 1 {
		// Only the final-summary injection, not an action prompt.
		t.Fatalf("expected only the final summary prompt, got %d: %v", len(msg.prompts), msg.prompts)
	}
	l2, _ := store.GetLoop(v.ID)
	if l2.State != StateCompleted {
		t.Fatalf("expected completed, got %s", l2.State)
	}
}

func TestEvaluateOne_StopPersistsCostThatTriggeredIt(t *testing.T) {
	store := newMemStore()
	// One $6 session pushes the loop over its $5 cap on the next tick.
	usage := &fakeUsage{perSessionCost: 6, ok: true}
	svc := NewService(Deps{Store: store, Messenger: &fakeMessenger{}, Launcher: &fakeLauncher{}, Usage: usage})

	loop := sampleLoopState("l1", "")
	loop.StopConditions = `{"max_iterations":48,"max_cost_usd":5}`
	_ = store.InsertLoop(loop)
	store.kids = []state.ChildSession{{ID: "s1", LoopID: "l1", CreatedAt: 1}}

	l, _ := store.GetLoop("l1")
	if _, err := svc.EvaluateOne(context.Background(), *l); err != nil {
		t.Fatalf("EvaluateOne: %v", err)
	}

	got, _ := store.GetLoop("l1")
	if got.State != StateCompleted {
		t.Fatalf("expected completed on cost budget, got %s", got.State)
	}
	// The cost that triggered the stop must be persisted, not left at 0 —
	// otherwise the row contradicts the "$5 budget reached" summary.
	if got.CostUSD != 6 {
		t.Fatalf("expected persisted cost 6, got %v (row must match the stop summary)", got.CostUSD)
	}
}

func TestTriggerNow_FiresImmediatelyBypassingInterval(t *testing.T) {
	store := newMemStore()
	launcher := &fakeLauncher{}
	svc := newServiceFull(store, &fakeMessenger{}, launcher)
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 3600}, // long interval
		ActionType:     ActionPromptRoot,
		ActionTemplate: "ping {{iteration}}",
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	// Mark as recently fired so a normal evaluate would NOT fire.
	l, _ := store.GetLoop(v.ID)
	l.LastFiredAt = time.Now().UnixMilli()
	_ = store.UpdateLoop(*l)

	if err := svc.TriggerNow(context.Background(), v.ID); err != nil {
		t.Fatalf("TriggerNow: %v", err)
	}
	if len(launcher.spawns) != 1 || launcher.spawns[0].Prompt != "ping 1" {
		t.Fatalf("expected forced action to spawn with rendered prompt, got %v", launcher.spawns)
	}
	l2, _ := store.GetLoop(v.ID)
	if l2.Iteration != 1 {
		t.Fatalf("expected iteration advanced to 1, got %d", l2.Iteration)
	}
}

func TestTriggerNow_RejectsNonScheduleLoops(t *testing.T) {
	store := newMemStore()
	svc := newService(store, &fakeMessenger{})
	// pr_event loop (the service has no forge, but Create only validates
	// the trigger type is known, which pr_event is).
	v, err := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerPREvent,
		TriggerConfig:  TriggerConfig{PRNumber: 1},
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 5, MaxCostUSD: 1},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.TriggerNow(context.Background(), v.ID); err == nil {
		t.Fatal("expected TriggerNow to reject a pr_event loop")
	}
}

func TestTriggerNow_EnforcesBudget(t *testing.T) {
	store := newMemStore()
	msg := &fakeMessenger{}
	svc := newService(store, msg)
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 1, MaxCostUSD: 1},
	})
	l, _ := store.GetLoop(v.ID)
	l.Iteration = 1 // already at cap
	_ = store.UpdateLoop(*l)

	if err := svc.TriggerNow(context.Background(), v.ID); err != nil {
		t.Fatalf("TriggerNow: %v", err)
	}
	// Only the final-summary injection, no action prompt.
	if len(msg.prompts) != 1 {
		t.Fatalf("expected only the final summary prompt, got %d: %v", len(msg.prompts), msg.prompts)
	}
	l2, _ := store.GetLoop(v.ID)
	if l2.State != StateCompleted {
		t.Fatalf("expected completed (budget), got %s", l2.State)
	}
}

func TestEvaluateOne_ErrorStreakIncrements(t *testing.T) {
	store := newMemStore()
	// Fresh prompt_root spawns via the launcher; make it fail so the
	// action errors and the streak increments.
	svc := newServiceFull(store, &fakeMessenger{}, &fakeLauncher{fail: true})
	v, _ := svc.Create(context.Background(), LoopSpec{
		RootSessionID:  "s1",
		TriggerType:    TriggerSchedule,
		TriggerConfig:  TriggerConfig{IntervalSeconds: 60},
		ActionType:     ActionPromptRoot,
		StopConditions: StopConditions{MaxIterations: 100, MaxCostUSD: 1},
	})
	l, _ := store.GetLoop(v.ID)
	_, _ = svc.EvaluateOne(context.Background(), *l)
	l2, _ := store.GetLoop(v.ID)
	if l2.ErrorStreak != 1 {
		t.Fatalf("expected error streak 1, got %d", l2.ErrorStreak)
	}
	its, _ := store.ListLoopIterations(v.ID)
	if len(its) != 1 || its[0].Outcome != "error" {
		t.Fatalf("expected one errored iteration, got %+v", its)
	}
}
