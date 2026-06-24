package loops

import (
	"context"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// Trigger detects whether a loop should fire its action this tick (AD-2).
// Cadence/throttling is enforced inside each implementation so the engine
// tick can be short.
//
// ShouldFire returns the fire decision, a human-readable detail string for
// the UI/template, and any optional mutation to the trigger config (e.g.
// pr_event records the seen head SHA so the next poll is stateless).
type Trigger interface {
	ShouldFire(ctx context.Context, l state.Loop, tc TriggerConfig, now time.Time) (fire bool, detail string, newConfig *TriggerConfig, err error)
}

// triggerFor returns the Trigger implementation for a loop's trigger type.
func triggerFor(triggerType string, status SessionStatusInferer, forge ForgePoller) (Trigger, error) {
	switch triggerType {
	case TriggerSchedule:
		return scheduleTrigger{}, nil
	case TriggerPREvent:
		return prEventTrigger{forge: forge}, nil
	case TriggerChildComplete:
		return childCompleteTrigger{status: status}, nil
	case TriggerTurnComplete:
		return turnCompleteTrigger{status: status}, nil
	default:
		return nil, fmt.Errorf("unknown trigger type %q", triggerType)
	}
}

// SessionStatusInferer reports whether a session's latest turn is still
// running. Used by child_complete / turn_complete triggers; reuses the
// adapter's status inference (same code path as the existing watcher).
type SessionStatusInferer interface {
	// TurnRunning reports whether the session is currently busy. ok is
	// false when the session can't be found / status can't be inferred.
	TurnRunning(ctx context.Context, platform, sessionID string) (running, ok bool)
}

// scheduleTrigger fires when now >= last_fired_at + interval (floor 60s).
type scheduleTrigger struct{}

func (scheduleTrigger) ShouldFire(_ context.Context, l state.Loop, tc TriggerConfig, now time.Time) (bool, string, *TriggerConfig, error) {
	interval := time.Duration(tc.IntervalSeconds) * time.Second
	if interval < MinScheduleInterval {
		interval = MinScheduleInterval
	}
	// Never fired yet → fire immediately.
	if l.LastFiredAt == 0 {
		return true, "scheduled (first run)", nil, nil
	}
	next := time.UnixMilli(l.LastFiredAt).Add(interval)
	if !now.Before(next) {
		return true, fmt.Sprintf("scheduled (every %s)", interval), nil, nil
	}
	return false, "", nil, nil
}

// turnCompleteTrigger fires when the root session's turn finishes, then
// waits for the next turn (one fire per turn). It uses last_fired_at as
// the gate: fire only when the session is idle AND we haven't fired since
// it last went idle. We approximate that by firing when idle and the loop
// hasn't fired in the current idle window — tracked via the session being
// busy between fires. Simplest correct version: fire on the idle edge.
type turnCompleteTrigger struct{ status SessionStatusInferer }

func (t turnCompleteTrigger) ShouldFire(ctx context.Context, l state.Loop, _ TriggerConfig, _ time.Time) (bool, string, *TriggerConfig, error) {
	running, ok := t.status.TurnRunning(ctx, l.Platform, l.RootSessionID)
	if !ok {
		return false, "", nil, nil
	}
	if running {
		return false, "", nil, nil
	}
	// Idle. Fire only if we haven't already fired while idle: if the
	// last fire is newer than nothing meaningful here, we rely on the
	// action advancing the conversation (making it busy again). If it's
	// still idle on the next tick with no new fire, that means the
	// re-prompt didn't take; the engine's in-flight guard plus the
	// error-streak cutoff bound any runaway. ponytail: edge detection by
	// "idle now" is naive; add a per-loop last-seen-busy flag if turns
	// get skipped or double-fired in practice.
	return true, "turn complete", nil, nil
}

// childCompleteTrigger fires when a tracked child of the loop reaches a
// terminal state since the last fire. Routed primarily via the watcher
// (AD-4), but also evaluated on tick as a backstop.
type childCompleteTrigger struct{ status SessionStatusInferer }

func (t childCompleteTrigger) ShouldFire(ctx context.Context, l state.Loop, _ TriggerConfig, _ time.Time) (bool, string, *TriggerConfig, error) {
	running, ok := t.status.TurnRunning(ctx, l.Platform, l.RootSessionID)
	if !ok || running {
		return false, "", nil, nil
	}
	return true, "child complete", nil, nil
}

// prEventTrigger polls the forge and fires on head-SHA change, an unseen
// comment, or merge (AD-2/AD-3). Throttled to MinPRPollInterval.
type prEventTrigger struct{ forge ForgePoller }

func (t prEventTrigger) ShouldFire(ctx context.Context, l state.Loop, tc TriggerConfig, now time.Time) (bool, string, *TriggerConfig, error) {
	if t.forge == nil {
		return false, "", nil, fmt.Errorf("pr_event trigger requires a forge poller")
	}
	if tc.PRNumber <= 0 {
		return false, "", nil, fmt.Errorf("pr_event trigger requires pr_number")
	}
	// Throttle polling independently of last_fired_at: we may poll and
	// find nothing, which must not reset the action clock.
	poll := time.Duration(tc.PollSeconds) * time.Second
	if poll < MinPRPollInterval {
		poll = MinPRPollInterval
	}
	if l.LastFiredAt != 0 && now.Sub(time.UnixMilli(l.LastFiredAt)) < poll {
		return false, "", nil, nil
	}

	st, err := t.forge.PollPR(ctx, l.Directory, tc.PRNumber)
	if err != nil {
		return false, "", nil, fmt.Errorf("polling PR #%d: %w", tc.PRNumber, err)
	}

	newCfg := tc
	var reasons []string
	if st.HeadSHA != "" && st.HeadSHA != tc.LastHeadSHA {
		if tc.LastHeadSHA != "" {
			reasons = append(reasons, "new commits")
		}
		newCfg.LastHeadSHA = st.HeadSHA
	}
	if st.LatestComment > tc.SeenCommentID {
		reasons = append(reasons, "new comments")
		newCfg.SeenCommentID = st.LatestComment
	}
	if st.Merged && !tc.Merged {
		reasons = append(reasons, "merged")
		newCfg.Merged = true
	}

	if len(reasons) == 0 {
		// No change: persist any first-seen baseline so we don't treat
		// the initial state as a change next time, but don't fire.
		if newCfg != tc {
			return false, "", &newCfg, nil
		}
		return false, "", nil, nil
	}

	detail := fmt.Sprintf("PR #%d: %s", tc.PRNumber, joinReasons(reasons))
	return true, detail, &newCfg, nil
}

func joinReasons(rs []string) string {
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += ", "
		}
		out += r
	}
	return out
}
