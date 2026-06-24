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

	// Duration cap.
	maxDur := sc.maxDuration()
	elapsed := now.Sub(time.UnixMilli(l.CreatedAt))
	if elapsed >= maxDur {
		return stopDecision{true, StateCompleted,
			fmt.Sprintf("reached max duration (%s)", maxDur)}
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
