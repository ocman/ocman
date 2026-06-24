package loops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// Service is the loop domain API used by REST, MCP, and the engine
// (AD-8). Single service, two transports — no behavior drift.
type Service struct {
	store     Store
	messenger Messenger
	launcher  Launcher
	forge     ForgePoller
	status    SessionStatusInferer
	usage     UsageSource
	notify    func(loopID string) // optional SSE broadcast hook (AD-10)
	now       func() time.Time    // injectable clock for tests
}

// Deps bundles the injected dependencies for NewService. All but Store
// may be nil in reduced configurations (e.g. tests, or a build without
// a forge); the relevant trigger/action then errors at evaluation time.
type Deps struct {
	Store     Store
	Messenger Messenger
	Launcher  Launcher
	Forge     ForgePoller
	Status    SessionStatusInferer
	Usage     UsageSource
	Notify    func(loopID string)
}

// NewService constructs a Service.
func NewService(d Deps) *Service {
	return &Service{
		store:     d.Store,
		messenger: d.Messenger,
		launcher:  d.Launcher,
		forge:     d.Forge,
		status:    d.Status,
		usage:     d.Usage,
		notify:    d.Notify,
		now:       time.Now,
	}
}

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) broadcast(loopID string) {
	if s.notify != nil {
		s.notify(loopID)
	}
}

// Create validates and persists a new loop (AD-6: budget is mandatory).
func (s *Service) Create(ctx context.Context, spec LoopSpec) (LoopView, error) {
	if spec.RootSessionID == "" {
		return LoopView{}, fmt.Errorf("root_session_id is required")
	}
	if spec.TriggerType == "" || spec.ActionType == "" {
		return LoopView{}, fmt.Errorf("trigger_type and action_type are required")
	}
	if _, err := triggerFor(spec.TriggerType, s.status, s.forge); err != nil {
		return LoopView{}, err
	}
	switch spec.ActionType {
	case ActionPromptRoot, ActionPromptChild, ActionSpawnChild, ActionSpawnWorktree:
	default:
		return LoopView{}, fmt.Errorf("unknown action type %q", spec.ActionType)
	}

	sc := spec.StopConditions
	if sc.MaxIterations <= 0 {
		sc.MaxIterations = DefaultMaxIterations
	}
	if !sc.hasBudget() {
		return LoopView{}, fmt.Errorf("a budget is required: set max_cost_usd or max_tokens")
	}
	if spec.ActionType == ActionPromptChild && spec.TriggerConfig.TargetSessionID == "" {
		return LoopView{}, fmt.Errorf("prompt_child action requires trigger_config.target_session_id")
	}

	platform := spec.Platform
	if platform == "" {
		platform = "opencode"
	}

	tcJSON, err := encodeJSON(spec.TriggerConfig)
	if err != nil {
		return LoopView{}, fmt.Errorf("encoding trigger config: %w", err)
	}
	scJSON, err := encodeJSON(sc)
	if err != nil {
		return LoopView{}, fmt.Errorf("encoding stop conditions: %w", err)
	}

	sessionMode := spec.SessionMode
	if sessionMode != SessionModeReuse {
		sessionMode = SessionModeFresh // default; also normalizes unknown values
	}

	now := s.clock().UnixMilli()
	l := state.Loop{
		ID:             newLoopID(),
		Platform:       platform,
		RootSessionID:  spec.RootSessionID,
		ParentLoopID:   spec.ParentLoopID,
		Directory:      spec.Directory,
		ProjectName:    spec.ProjectName,
		Title:          spec.Title,
		Description:    spec.Description,
		Pattern:        spec.Pattern,
		TriggerType:    spec.TriggerType,
		TriggerConfig:  tcJSON,
		ActionType:     spec.ActionType,
		ActionTemplate: spec.ActionTemplate,
		StopConditions: scJSON,
		SessionMode:    sessionMode,
		State:          StateActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.InsertLoop(l); err != nil {
		return LoopView{}, err
	}
	s.broadcast(l.ID)
	return toView(l), nil
}

// Update applies an in-place edit of a loop's safe-to-change settings
// (title, action template, session mode, trigger config, stop conditions).
// Trigger TYPE and action TYPE are immutable here — changing those is
// effectively a new loop. Allowed only while active or paused.
//
// Budget invariant is preserved: if stop_conditions are edited they must
// still carry a budget (AD-6).
func (s *Service) Update(ctx context.Context, id string, upd LoopUpdate) (LoopView, error) {
	l, err := s.store.GetLoop(id)
	if err != nil {
		return LoopView{}, err
	}
	if l.State != StateActive && l.State != StatePaused {
		return LoopView{}, fmt.Errorf("loop %s is not editable (state=%s)", id, l.State)
	}

	if upd.Title != nil {
		l.Title = *upd.Title
	}
	if upd.ActionTemplate != nil {
		l.ActionTemplate = *upd.ActionTemplate
	}
	if upd.SessionMode != nil {
		mode := *upd.SessionMode
		if mode != SessionModeReuse {
			mode = SessionModeFresh
		}
		l.SessionMode = mode
	}
	if upd.TriggerConfig != nil {
		// Merge detection state forward so an edit of interval/pr_number
		// doesn't wipe the pr_event baseline (last_head_sha etc.).
		cur := decodeTriggerConfig(l.TriggerConfig)
		next := *upd.TriggerConfig
		next.LastHeadSHA = cur.LastHeadSHA
		next.SeenCommentID = cur.SeenCommentID
		next.Merged = cur.Merged
		enc, encErr := encodeJSON(next)
		if encErr != nil {
			return LoopView{}, fmt.Errorf("encoding trigger config: %w", encErr)
		}
		l.TriggerConfig = enc
	}
	if upd.StopConditions != nil {
		sc := *upd.StopConditions
		if sc.MaxIterations <= 0 {
			sc.MaxIterations = DefaultMaxIterations
		}
		if !sc.hasBudget() {
			return LoopView{}, fmt.Errorf("a budget is required: set max_cost_usd or max_tokens")
		}
		enc, encErr := encodeJSON(sc)
		if encErr != nil {
			return LoopView{}, fmt.Errorf("encoding stop conditions: %w", encErr)
		}
		l.StopConditions = enc
	}

	if err := s.store.UpdateLoop(*l); err != nil {
		return LoopView{}, err
	}
	s.broadcast(l.ID)
	return toView(*l), nil
}

// List returns loops matching the filter.
func (s *Service) List(ctx context.Context, f LoopFilter) ([]LoopView, error) {
	rows, err := s.store.ListLoops(f.RootSessionID, f.Directory)
	if err != nil {
		return nil, err
	}
	out := make([]LoopView, 0, len(rows))
	for _, l := range rows {
		out = append(out, toView(l))
	}
	return out, nil
}

// Get returns a loop with its iteration timeline and child tree.
func (s *Service) Get(ctx context.Context, id string) (LoopDetail, error) {
	l, err := s.store.GetLoop(id)
	if err != nil {
		return LoopDetail{}, err
	}
	its, err := s.store.ListLoopIterations(id)
	if err != nil {
		return LoopDetail{}, err
	}
	children, err := s.store.ListChildSessionsByLoop(id)
	if err != nil {
		return LoopDetail{}, err
	}
	subs, err := s.store.ListLoopsByParent(id)
	if err != nil {
		return LoopDetail{}, err
	}
	subViews := make([]LoopView, 0, len(subs))
	for _, sub := range subs {
		subViews = append(subViews, toView(sub))
	}
	return LoopDetail{LoopView: toView(*l), Iterations: its, Children: children, SubLoops: subViews}, nil
}

// Delete soft-deletes a loop: it transitions to the terminal "deleted"
// state, so the engine stops evaluating it and it's filtered out of the
// listing, but the row + audit trail remain in the DB. Spawned child
// sessions are left running (the server layer can cancel them separately
// if desired).
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.store.SetLoopState(id, StateDeleted, "deleted by user"); err != nil {
		return err
	}
	s.broadcast(id)
	return nil
}

// Pause sets a loop to paused (skipped by the engine until resumed).
func (s *Service) Pause(ctx context.Context, id string) error {
	if err := s.store.SetLoopState(id, StatePaused, ""); err != nil {
		return err
	}
	s.broadcast(id)
	return nil
}

// Resume reactivates a paused loop, preserving its iteration count,
// budget consumed, and history.
//
// completed/errored/deleted loops are NOT resumable: they're terminal
// (a real limit was hit, or the user deleted the loop). To run again,
// recreate the loop.
//
// As a guard, a resume that would trip a stop condition on the very next
// tick is rejected with a message pointing at the offending limit, so the
// user doesn't get a loop that silently re-terminates one tick later.
func (s *Service) Resume(ctx context.Context, id string) error {
	l, err := s.store.GetLoop(id)
	if err != nil {
		return err
	}
	if l.State != StatePaused {
		return fmt.Errorf("loop %s cannot be resumed (state=%s); recreate it instead", id, l.State)
	}
	// Refresh usage so the pre-resume stop check sees current cost/tokens.
	s.refreshUsage(ctx, l)
	if dec := evaluateStop(*l, decodeStopConditions(l.StopConditions), s.clock()); dec.Stop {
		return fmt.Errorf("cannot resume: %s — recreate the loop or raise its limits", dec.Reason)
	}
	if err := s.store.SetLoopState(id, StateActive, ""); err != nil {
		return err
	}
	s.broadcast(id)
	return nil
}

// Step runs one cycle for a loop regardless of pause state, then leaves
// it paused. Used by the manual "step" control.
func (s *Service) Step(ctx context.Context, id string) error {
	l, err := s.store.GetLoop(id)
	if err != nil {
		return err
	}
	if _, err := s.EvaluateOne(ctx, *l); err != nil {
		return err
	}
	return s.Pause(ctx, id)
}

// EvaluateOne runs one trigger→stop→action cycle for an active loop if
// the trigger is due. Returns whether an action was performed.
//
// Order is mandatory (AD-6): trigger first (cheap, decides if anything
// happens), THEN stop conditions (before any side effect), THEN action.
func (s *Service) EvaluateOne(ctx context.Context, l state.Loop) (advanced bool, err error) {
	now := s.clock()
	tc := decodeTriggerConfig(l.TriggerConfig)
	sc := decodeStopConditions(l.StopConditions)

	// Refresh cached usage before stop evaluation (AD-7).
	s.refreshUsage(ctx, &l)

	trig, err := triggerFor(l.TriggerType, s.status, s.forge)
	if err != nil {
		return false, err
	}

	fire, detail, newCfg, err := trig.ShouldFire(ctx, l, tc, now)
	if err != nil {
		return false, err
	}

	// Persist any trigger-state mutation (e.g. pr_event baseline) even
	// when not firing, so detection is stateless across ticks (AD-3).
	if newCfg != nil {
		if enc, encErr := encodeJSON(*newCfg); encErr == nil {
			l.TriggerConfig = enc
			tc = *newCfg
			_ = s.store.UpdateLoop(l)
		}
	}

	if !fire {
		return false, nil
	}

	return s.fireAction(ctx, l, tc, sc, detail, now)
}

// TriggerNow forces a schedule loop to fire its action immediately,
// bypassing the interval throttle but STILL enforcing stop conditions
// (AD-6). Restricted to `schedule` loops: the other trigger types fire
// on external events (PR change, child/turn completion) where a manual
// "fire now" has no well-defined meaning. The interval clock is reset
// (LastFiredAt := now via fireAction) so the next scheduled fire is
// interval-from-now.
func (s *Service) TriggerNow(ctx context.Context, id string) error {
	l, err := s.store.GetLoop(id)
	if err != nil {
		return err
	}
	if l.TriggerType != TriggerSchedule {
		return fmt.Errorf("trigger-now is only supported for schedule loops (this loop is %q)", l.TriggerType)
	}
	if l.State != StateActive && l.State != StatePaused {
		return fmt.Errorf("loop %s is not runnable (state=%s)", id, l.State)
	}

	now := s.clock()
	tc := decodeTriggerConfig(l.TriggerConfig)
	sc := decodeStopConditions(l.StopConditions)
	s.refreshUsage(ctx, l)

	_, err = s.fireAction(ctx, *l, tc, sc, "manual trigger", now)
	return err
}

// fireAction performs the stop-check → dispatch → counter-advance portion
// of a loop cycle (AD-6 order). Shared by EvaluateOne (after a trigger
// fires) and TriggerNow (which skips the trigger). Returns whether an
// action was performed.
func (s *Service) fireAction(
	ctx context.Context,
	l state.Loop,
	tc TriggerConfig,
	sc StopConditions,
	detail string,
	now time.Time,
) (bool, error) {
	// Stop conditions BEFORE the action (AD-6).
	if dec := evaluateStop(l, sc, now); dec.Stop {
		_ = s.store.SetLoopState(l.ID, dec.TerminalState, dec.Reason)
		s.injectFinalSummary(ctx, l, dec.Reason)
		s.broadcast(l.ID)
		return false, nil
	}

	prompt := renderTemplate(l.ActionTemplate, l, tc, detail)
	res, dispatchErr := s.dispatchAction(ctx, l, tc, prompt, detail, now)

	// Advance counters.
	l.Iteration++
	l.LastFiredAt = now.UnixMilli()
	// Persist the dedicated session prompt_root resolved/created so reuse
	// can re-prompt it and fresh tracks the latest (OQ5).
	if res.LoopSessionID != "" {
		l.LoopSessionID = res.LoopSessionID
	}
	if dispatchErr != nil {
		l.ErrorStreak++
		l.LastSummary = "error: " + dispatchErr.Error()
	} else {
		l.ErrorStreak = 0
		l.LastSummary = res.Summary
	}
	if err := s.store.UpdateLoop(l); err != nil {
		return true, err
	}
	s.broadcast(l.ID)
	return true, dispatchErr
}

// refreshUsage updates the loop's cached token/cost from the usage
// source (AD-7), aggregated over the loop's whole session tree: its own
// spawned sessions plus every descendant sub-loop's sessions. On
// unavailable data the cached values are left as-is (conservative: stop
// checks then use the last known value).
func (s *Service) refreshUsage(ctx context.Context, l *state.Loop) {
	if s.usage == nil {
		return
	}
	ids := s.collectTreeSessionIDs(l.ID, map[string]bool{})
	if len(ids) == 0 {
		return
	}
	tokens, cost, ok := s.usage.SessionUsage(ctx, ids)
	if !ok {
		return
	}
	l.TokensUsed = tokens
	l.CostUSD = cost
}

// collectTreeSessionIDs gathers the session IDs of a loop's spawned
// children and, recursively, those of its sub-loops. `seen` guards
// against a cyclic parent_loop_id graph (bad data) so we can't loop
// forever. Loops with prompt_root in reuse mode reuse one session; fresh
// mode spawns one per iteration — both land in child_sessions.
func (s *Service) collectTreeSessionIDs(loopID string, seen map[string]bool) []string {
	if seen[loopID] {
		return nil
	}
	seen[loopID] = true

	var ids []string
	if children, err := s.store.ListChildSessionsByLoop(loopID); err == nil {
		for _, c := range children {
			ids = append(ids, c.ID)
		}
	}
	if subs, err := s.store.ListLoopsByParent(loopID); err == nil {
		for _, sub := range subs {
			ids = append(ids, s.collectTreeSessionIDs(sub.ID, seen)...)
		}
	}
	return ids
}

// injectFinalSummary notifies the root session that the loop ended.
func (s *Service) injectFinalSummary(ctx context.Context, l state.Loop, reason string) {
	if s.messenger == nil {
		return
	}
	msg := fmt.Sprintf("Loop %q ended: %s (after %d iterations, $%.2f).",
		loopLabel(l), reason, l.Iteration, l.CostUSD)
	_ = s.messenger.SendPrompt(ctx, l.RootSessionID, msg)
}

func loopLabel(l state.Loop) string {
	if l.Title != "" {
		return l.Title
	}
	return l.ID
}

func toView(l state.Loop) LoopView {
	return LoopView{
		Loop:                  l,
		TriggerConfigDecoded:  decodeTriggerConfig(l.TriggerConfig),
		StopConditionsDecoded: decodeStopConditions(l.StopConditions),
	}
}

// newLoopID returns a short random loop identifier.
func newLoopID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "loop_" + hex.EncodeToString(b[:])
}
