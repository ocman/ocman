package loops

import (
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// stopDecision is the result of evaluating stop conditions before an
// action. When Stop is true the loop must transition to TerminalState
// with Reason recorded as the summary; the action is NOT performed (AD-6).
type stopDecision struct {
	Stop          bool
	TerminalState string // completed | stopped | errored
	Reason        string
}

// recurringTrigger reports whether a trigger type fires repeatedly over
// the loop's lifetime (vs. running once to completion). These are exempt
// from the lifetime duration cap — a daily cron is meant to live for
// weeks (see evaluateStop).
func recurringTrigger(triggerType string) bool {
	switch triggerType {
	case TriggerSchedule, TriggerCron, TriggerPREvent:
		return true
	default:
		return false
	}
}

// evaluateStop checks every stop condition for a loop BEFORE its next
// action (AD-6). Pre-action evaluation guarantees an over-budget loop
// never sends "one more" prompt.
//
// now is injected for deterministic tests.
func evaluateStop(l state.Loop, sc StopConditions, now time.Time) stopDecision {
	// Iteration cap.
	if l.Iteration >= sc.MaxIterations {
		return stopDecision{true, StateCompleted,
			fmt.Sprintf("reached max iterations (%d)", sc.MaxIterations)}
	}

	// Duration cap — a lifetime wall-clock cutoff measured from loop
	// creation, only for one-shot loops. Recurring loops (schedule/cron/
	// pr_event) are exempt: they're meant to live indefinitely (a daily
	// cron should run for weeks), and the engine fires-and-forgets each
	// run, so it has no per-run end time to bound on. Per-run wall-clock
	// is the spawned agent session's concern, not the loop's.
	if !recurringTrigger(l.TriggerType) {
		maxDur := sc.maxDuration()
		elapsed := now.Sub(time.UnixMilli(l.CreatedAt))
		if elapsed >= maxDur {
			return stopDecision{true, StateCompleted,
				fmt.Sprintf("reached max duration (%s)", maxDur)}
		}
	}

	// Error-streak cutoff — directly addresses "wrong path for longer".
	if l.ErrorStreak >= sc.errorStreak() {
		return stopDecision{true, StateErrored,
			fmt.Sprintf("error streak reached (%d consecutive failures)", l.ErrorStreak)}
	}

	// Cost cap.
	if sc.MaxCostUSD > 0 && l.CostUSD >= sc.MaxCostUSD {
		return stopDecision{true, StateCompleted,
			fmt.Sprintf("reached cost budget ($%.2f)", sc.MaxCostUSD)}
	}

	// Token cap.
	if sc.MaxTokens > 0 && l.TokensUsed >= sc.MaxTokens {
		return stopDecision{true, StateCompleted,
			fmt.Sprintf("reached token budget (%d)", sc.MaxTokens)}
	}

	return stopDecision{Stop: false}
}
