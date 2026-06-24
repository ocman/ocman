package loops

import (
	"context"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// dispatchResult reports what an action produced for the iteration record
// and the loop's counters.
type dispatchResult struct {
	TargetSessionID string
	ChildSessionID  string
	Summary         string
	// LoopSessionID, when non-empty, is the dedicated loop session that
	// prompt_root resolved/created this iteration. fireAction persists it
	// back onto the loop so 'reuse' mode can re-prompt it next time and
	// 'fresh' mode tracks the latest spawned session.
	LoopSessionID string
}

// dispatchAction performs one loop action with DB-idempotent bookkeeping
// (AD-5 / AD-5a):
//
//  1. Insert a loop_iteration row with outcome='pending' BEFORE the side
//     effect (the outbox), so a crash can't silently re-send.
//  2. Perform the side effect (SendPrompt or Spawn).
//  3. Update the same row to ok/error.
//
// Returns the dispatch result and whether it succeeded. The caller updates
// the loop's iteration counter and error streak.
func (s *Service) dispatchAction(ctx context.Context, l state.Loop, tc TriggerConfig, prompt, triggerDetail string, now time.Time) (dispatchResult, error) {
	seq := l.Iteration + 1
	itID, err := s.store.InsertLoopIteration(state.LoopIteration{
		LoopID:         l.ID,
		Seq:            seq,
		FiredAt:        now.UnixMilli(),
		StartedAt:      now.UnixMilli(),
		TriggerDetail:  triggerDetail,
		RenderedPrompt: prompt,
		Outcome:        "pending",
	})
	if err != nil {
		return dispatchResult{}, fmt.Errorf("recording pending iteration: %w", err)
	}

	res, dispatchErr := s.performAction(ctx, l, tc, prompt)

	it := state.LoopIteration{
		ID:              itID,
		CompletedAt:     time.Now().UnixMilli(),
		TargetSessionID: res.TargetSessionID,
		ChildSessionID:  res.ChildSessionID,
	}
	if dispatchErr != nil {
		it.Outcome = "error"
		it.Summary = dispatchErr.Error()
	} else {
		it.Outcome = "ok"
		it.Summary = res.Summary
	}
	if updErr := s.store.UpdateLoopIteration(it); updErr != nil {
		// Bookkeeping failure: log via returned error chain but keep the
		// dispatch error as primary.
		if dispatchErr != nil {
			return res, dispatchErr
		}
		return res, fmt.Errorf("closing iteration record: %w", updErr)
	}
	return res, dispatchErr
}

// performAction routes to the concrete action (AD-5).
func (s *Service) performAction(ctx context.Context, l state.Loop, tc TriggerConfig, prompt string) (dispatchResult, error) {
	switch l.ActionType {
	case ActionPromptRoot:
		return s.promptLoopSession(ctx, l, prompt)

	case ActionPromptChild:
		target := tc.TargetSessionID
		if target == "" {
			return dispatchResult{}, fmt.Errorf("prompt_child requires target_session_id")
		}
		if s.messenger == nil {
			return dispatchResult{}, fmt.Errorf("no messenger configured")
		}
		if err := s.messenger.SendPrompt(ctx, target, prompt, l.Model); err != nil {
			return dispatchResult{}, err
		}
		return dispatchResult{TargetSessionID: target, Summary: "prompted child session"}, nil

	case ActionSpawnChild, ActionSpawnWorktree:
		if s.launcher == nil {
			return dispatchResult{}, fmt.Errorf("no launcher configured")
		}
		childID, err := s.launcher.Spawn(ctx, SpawnRequest{
			LoopID:        l.ID,
			Platform:      l.Platform,
			ParentSession: l.RootSessionID,
			Directory:     l.Directory,
			Intent:        l.CurrentTask,
			Prompt:        prompt,
			Model:         l.Model,
			Worktree:      l.ActionType == ActionSpawnWorktree,
		})
		if err != nil {
			return dispatchResult{}, err
		}
		return dispatchResult{ChildSessionID: childID, Summary: "spawned " + childID}, nil

	default:
		return dispatchResult{}, fmt.Errorf("unknown action type %q", l.ActionType)
	}
}

// promptLoopSession runs the prompt_root action against the loop's
// DEDICATED session (not the creator/owner session). The creator session
// in RootSessionID is reserved for anchoring + final-summary reporting and
// is never prompted with action output (OQ5).
//
// Session strategy:
//   - reuse + existing loop session  -> re-prompt that session (continuity).
//   - fresh, or first run, or missing -> spawn a new dedicated session in
//     the loop's directory and send the prompt as its first message.
//
// The resolved/created session id is returned in dispatchResult.LoopSessionID
// so fireAction can persist it onto the loop.
func (s *Service) promptLoopSession(ctx context.Context, l state.Loop, prompt string) (dispatchResult, error) {
	reuse := l.SessionMode == SessionModeReuse && l.LoopSessionID != ""
	if reuse {
		if s.messenger == nil {
			return dispatchResult{}, fmt.Errorf("no messenger configured")
		}
		if err := s.messenger.SendPrompt(ctx, l.LoopSessionID, prompt, l.Model); err != nil {
			return dispatchResult{}, err
		}
		return dispatchResult{
			TargetSessionID: l.LoopSessionID,
			LoopSessionID:   l.LoopSessionID,
			Summary:         "prompted loop session (reuse)",
		}, nil
	}

	// Spawn a fresh dedicated session for this iteration.
	if s.launcher == nil {
		return dispatchResult{}, fmt.Errorf("no launcher configured")
	}
	sessionID, err := s.launcher.Spawn(ctx, SpawnRequest{
		LoopID:        l.ID,
		Platform:      l.Platform,
		ParentSession: l.RootSessionID,
		Directory:     l.Directory,
		Intent:        loopLabel(l),
		Prompt:        prompt,
		Model:         l.Model,
		Worktree:      false,
	})
	if err != nil {
		return dispatchResult{}, err
	}
	summary := "spawned fresh loop session"
	if l.SessionMode == SessionModeReuse {
		summary = "started loop session (reuse)"
	}
	return dispatchResult{
		TargetSessionID: sessionID,
		ChildSessionID:  sessionID,
		LoopSessionID:   sessionID,
		Summary:         summary,
	}, nil
}
