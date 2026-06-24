// Package loops implements the agent-loops domain: self-driving
// orchestrations that fire an action on a trigger until a stop condition
// is met. See spec/agent-loops/architecture.md.
//
// The package is decoupled from internal/server (AD-9): everything it
// needs from the outside world is expressed as a small interface
// (Store, Messenger, Launcher, ForgePoller, UsageSource) which the
// server/mcp layers inject.
package loops

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// Trigger types.
const (
	TriggerChildComplete = "child_complete"
	TriggerSchedule      = "schedule"
	TriggerPREvent       = "pr_event"
	TriggerTurnComplete  = "turn_complete"
)

// Action types.
const (
	ActionPromptRoot    = "prompt_root"
	ActionPromptChild   = "prompt_child"
	ActionSpawnChild    = "spawn_child"
	ActionSpawnWorktree = "spawn_worktree"
)

// Loop states.
const (
	StateActive    = "active"
	StatePaused    = "paused"
	StateCompleted = "completed"
	StateDeleted   = "deleted"
	StateErrored   = "errored"
)

// Session modes (per-iteration session strategy for prompt_root, OQ5).
const (
	// SessionModeFresh spawns a new dedicated session each iteration.
	// Default: avoids context blowup on long-running loops.
	SessionModeFresh = "fresh"
	// SessionModeReuse re-prompts the loop's dedicated session every
	// iteration (continuity across iterations).
	SessionModeReuse = "reuse"
)

// Default stop-condition values (AD-6). A loop with no budget cannot be
// created, but the non-budget defaults are filled in when omitted.
const (
	DefaultMaxIterations = 25
	DefaultMaxDuration   = 8 * time.Hour
	DefaultErrorStreak   = 3
	MinScheduleInterval  = 60 * time.Second
	MinPRPollInterval    = 30 * time.Second
)

// StopConditions is the decoded form of loops.stop_conditions (AD-6).
type StopConditions struct {
	MaxIterations int     `json:"max_iterations"`
	MaxCostUSD    float64 `json:"max_cost_usd,omitempty"`
	MaxTokens     int64   `json:"max_tokens,omitempty"`
	MaxDuration   string  `json:"max_duration,omitempty"` // e.g. "8h"
	ErrorStreak   int     `json:"error_streak,omitempty"`
	GoalPredicate string  `json:"goal_predicate,omitempty"`
}

// maxDuration parses MaxDuration, falling back to DefaultMaxDuration.
func (sc StopConditions) maxDuration() time.Duration {
	if sc.MaxDuration == "" {
		return DefaultMaxDuration
	}
	d, err := time.ParseDuration(sc.MaxDuration)
	if err != nil || d <= 0 {
		return DefaultMaxDuration
	}
	return d
}

// errorStreak returns the configured error-streak cutoff or the default.
func (sc StopConditions) errorStreak() int {
	if sc.ErrorStreak <= 0 {
		return DefaultErrorStreak
	}
	return sc.ErrorStreak
}

// hasBudget reports whether at least one spend cap is set (AD-6: a loop
// with no budget cannot be created).
func (sc StopConditions) hasBudget() bool {
	return sc.MaxCostUSD > 0 || sc.MaxTokens > 0
}

// TriggerConfig is the decoded form of loops.trigger_config. Fields are
// trigger-type specific; unused ones stay zero.
type TriggerConfig struct {
	IntervalSeconds int    `json:"interval_seconds,omitempty"`  // schedule
	PRNumber        int    `json:"pr_number,omitempty"`         // pr_event
	PollSeconds     int    `json:"poll_seconds,omitempty"`      // pr_event throttle
	TargetSessionID string `json:"target_session_id,omitempty"` // prompt_child target
	// Detection state for pr_event (AD-3): persisted so polling is stateless.
	LastHeadSHA   string `json:"last_head_sha,omitempty"`
	SeenCommentID int64  `json:"seen_comment_id,omitempty"`
	Merged        bool   `json:"merged,omitempty"`
}

// LoopSpec is the create-time input shared by REST and MCP.
type LoopSpec struct {
	Platform       string         `json:"platform"`
	RootSessionID  string         `json:"root_session_id"`
	ParentLoopID   string         `json:"parent_loop_id,omitempty"`
	Directory      string         `json:"directory,omitempty"`
	ProjectName    string         `json:"project_name,omitempty"`
	Title          string         `json:"title,omitempty"`
	Description    string         `json:"description,omitempty"`
	Pattern        string         `json:"pattern,omitempty"`
	TriggerType    string         `json:"trigger_type"`
	TriggerConfig  TriggerConfig  `json:"trigger_config"`
	ActionType     string         `json:"action_type"`
	ActionTemplate string         `json:"action_template"`
	Model          string         `json:"model,omitempty"`
	StopConditions StopConditions `json:"stop_conditions"`
	// SessionMode controls per-iteration session strategy for prompt_root:
	// "fresh" (default) or "reuse". Ignored by other action types.
	SessionMode string `json:"session_mode,omitempty"`
}

// LoopUpdate is the editable subset of a loop's settings (PATCH). Pointer
// fields distinguish "not provided" from "set to zero". Trigger/action
// TYPE are intentionally absent (immutable post-create).
type LoopUpdate struct {
	Title          *string         `json:"title,omitempty"`
	ActionTemplate *string         `json:"action_template,omitempty"`
	Model          *string         `json:"model,omitempty"`
	SessionMode    *string         `json:"session_mode,omitempty"`
	TriggerConfig  *TriggerConfig  `json:"trigger_config,omitempty"`
	StopConditions *StopConditions `json:"stop_conditions,omitempty"`
}

// LoopView is the read model returned to callers (REST/MCP), combining
// the stored row with decoded config for display.
type LoopView struct {
	state.Loop
	TriggerConfigDecoded  TriggerConfig  `json:"triggerConfig"`
	StopConditionsDecoded StopConditions `json:"stopConditions"`
}

// LoopDetail is a single loop plus its iteration timeline, child sessions,
// and direct sub-loops (for nested rendering).
type LoopDetail struct {
	LoopView
	Iterations []state.LoopIteration `json:"iterations"`
	Children   []state.ChildSession  `json:"children"`
	SubLoops   []LoopView            `json:"subLoops"`
}

// LoopFilter narrows List results.
type LoopFilter struct {
	RootSessionID string
	Directory     string
}

// decodeStopConditions parses a loop's stored JSON, applying defaults.
func decodeStopConditions(raw string) StopConditions {
	var sc StopConditions
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &sc)
	}
	if sc.MaxIterations <= 0 {
		sc.MaxIterations = DefaultMaxIterations
	}
	return sc
}

// decodeTriggerConfig parses a loop's stored JSON trigger config.
func decodeTriggerConfig(raw string) TriggerConfig {
	var tc TriggerConfig
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &tc)
	}
	return tc
}

func encodeJSON(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Store is the subset of state.DB the loops domain needs. Defined here
// so the package depends on an interface, not the concrete DB (AD-9).
type Store interface {
	InsertLoop(l state.Loop) error
	UpdateLoop(l state.Loop) error
	SetLoopState(id, newState, summary string) error
	GetLoop(id string) (*state.Loop, error)
	ListLoops(rootSessionID, directory string) ([]state.Loop, error)
	ListActiveLoops() ([]state.Loop, error)
	InsertLoopIteration(it state.LoopIteration) (int64, error)
	UpdateLoopIteration(it state.LoopIteration) error
	ListLoopIterations(loopID string) ([]state.LoopIteration, error)
	ListChildSessionsByLoop(loopID string) ([]state.ChildSession, error)
	ListLoopsByParent(parentLoopID string) ([]state.Loop, error)
}

// Messenger sends a prompt to an existing session (Platform.SendMessage,
// AD-5). Narrowed so loops needn't import the full Platform interface.
type Messenger interface {
	SendPrompt(ctx context.Context, sessionID, prompt, model string) error
}

// SpawnRequest describes a child/worktree spawn for a loop action.
type SpawnRequest struct {
	LoopID        string
	Platform      string
	ParentSession string
	Directory     string
	Intent        string
	Prompt        string
	Model         string
	Worktree      bool
}

// Launcher spawns child sessions for spawn_* actions (AD-5). Returns the
// new child session ID.
type Launcher interface {
	Spawn(ctx context.Context, req SpawnRequest) (childSessionID string, err error)
}

// PRState is the forge-poll result for a pr_event trigger (AD-3).
type PRState struct {
	HeadSHA       string
	LatestComment int64
	Merged        bool
}

// ForgePoller polls a PR's current state. Reuses the PR-sidebar clients
// in the server layer.
type ForgePoller interface {
	PollPR(ctx context.Context, directory string, prNumber int) (PRState, error)
}

// UsageSource sums token/cost across a set of sessions (AD-7). The Service
// collects the full session-id set for a loop tree (root + children +
// descendant sub-loops, recursively) and passes it here; the source need
// only know how to read per-session usage. ok=false means data was
// unavailable (the caller then keeps the last known cached value).
type UsageSource interface {
	SessionUsage(ctx context.Context, sessionIDs []string) (tokens int64, costUSD float64, ok bool)
}
