package workflows

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/loops"
	"github.com/NoUseFreak/ocman/internal/state"
)

const (
	StateActive     = "active"
	StatePaused     = "paused"
	StateSuccessful = "successful"
	StateCanceled   = "canceled"
	StateFailed     = "failed"

	NodePending    = "pending"
	NodeReady      = "ready"
	NodeRunning    = "running"
	NodeSuccessful = "successful"
	NodeFailed     = "failed"
	NodeCanceled   = "canceled"
	NodeSkipped    = "skipped"

	AttemptWaiting    = "waiting"
	AttemptStarting   = "starting"
	AttemptRunning    = "running"
	AttemptSuccessful = "successful"
	AttemptFailed     = "failed"
	AttemptCanceled   = "canceled"
	AttemptErrored    = "errored"
	AttemptDenied     = "denied"
	AttemptUnknown    = "unknown"

	TriggerManual          = "manual"
	TriggerInterval        = "interval"
	TriggerCron            = "cron"
	TriggerPR              = "pr"
	TriggerChildCompletion = "child_completion"
	TriggerTurnCompletion  = "turn_completion"

	OverlapSkip     = "skip"
	OverlapQueue    = "queue"
	OverlapParallel = "parallel"

	DecisionStarted = "started"
	DecisionSkipped = "skipped"
	DecisionQueued  = "queued"
)

type Definition struct {
	ID            string           `json:"id" yaml:"id"`
	Name          string           `json:"name" yaml:"name"`
	Version       string           `json:"version" yaml:"version"`
	Concurrency   int              `json:"concurrency" yaml:"concurrency"`
	RetentionDays int              `json:"retentionDays,omitempty" yaml:"retentionDays,omitempty"`
	Directory     string           `json:"directory,omitempty" yaml:"directory,omitempty"`
	Secrets       []SecretRef      `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Pools         []Pool           `json:"pools,omitempty" yaml:"pools,omitempty"`
	Workspace     *WorkspaceConfig `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	Limits        *Limits          `json:"limits,omitempty" yaml:"limits,omitempty"`
	// LoopCompat marks a one-node workflow copied from the legacy loop
	// system. The legacy engine remains its execution owner until #331.
	LoopCompat   json.RawMessage `json:"loopCompat,omitempty" yaml:"loopCompat,omitempty"`
	Triggers     []Trigger       `json:"triggers" yaml:"triggers"`
	Nodes        []Node          `json:"nodes" yaml:"nodes"`
	Dependencies []Dependency    `json:"dependencies" yaml:"dependencies"`
	FailFast     bool            `json:"failFast,omitempty" yaml:"failFast,omitempty"`

	// SubworkflowRefs records the workflow ids this version references
	// through subworkflow and map nodes at publish time. Inlining removes
	// subworkflow nodes, so this preserves the reference graph needed for
	// indirect recursive-cycle detection across later publishes.
	SubworkflowRefs []string `json:"subworkflowRefs,omitempty" yaml:"subworkflowRefs,omitempty"`
}

// Pool is a named resource capacity a workflow declares. Nodes acquire
// units from a pool before going active; the scheduler never lets held
// units exceed the pool's capacity.
type Pool struct {
	Name     string `json:"name" yaml:"name"`
	Capacity int    `json:"capacity" yaml:"capacity"`
}

// ResourceRequest is a node's demand for units from a named pool.
type ResourceRequest struct {
	Pool  string `json:"pool" yaml:"pool"`
	Units int    `json:"units" yaml:"units"`
}

// Limits optionally bound a run's aggregate descendant work. A zero /
// omitted field means unlimited.
type Limits struct {
	MaxCostUSD      float64 `json:"maxCostUsd,omitempty" yaml:"maxCostUsd,omitempty"`
	MaxTokens       int64   `json:"maxTokens,omitempty" yaml:"maxTokens,omitempty"`
	MaxDurationSecs int64   `json:"maxDurationSeconds,omitempty" yaml:"maxDurationSeconds,omitempty"`
}

type Trigger struct {
	ID              string `json:"id" yaml:"id"`
	Type            string `json:"type" yaml:"type"`
	Overlap         string `json:"overlap,omitempty" yaml:"overlap,omitempty"`
	IntervalSeconds int    `json:"intervalSeconds,omitempty" yaml:"intervalSeconds,omitempty"`
	Cron            string `json:"cron,omitempty" yaml:"cron,omitempty"`
	PRNumber        int    `json:"prNumber,omitempty" yaml:"prNumber,omitempty"`
	PollSeconds     int    `json:"pollSeconds,omitempty" yaml:"pollSeconds,omitempty"`
	Directory       string `json:"directory,omitempty" yaml:"directory,omitempty"`
	Platform        string `json:"platform,omitempty" yaml:"platform,omitempty"`
	SessionID       string `json:"sessionId,omitempty" yaml:"sessionId,omitempty"`
}

type TriggerSnapshot struct {
	Trigger
	VersionID string `json:"versionId"`
	FiredAt   int64  `json:"firedAt"`
	Detail    string `json:"detail"`
}

type TriggerStatus struct {
	Trigger
	VersionID     string `json:"versionId"`
	LastCheckedAt int64  `json:"lastCheckedAt,omitempty"`
	NextCheckAt   int64  `json:"nextCheckAt,omitempty"`
	LastFiredAt   int64  `json:"lastFiredAt,omitempty"`
	LastDecision  string `json:"lastDecision,omitempty"`
	LastRunID     string `json:"lastRunId,omitempty"`
	Queued        int    `json:"queued"`
}

type Node struct {
	ID          string            `json:"id" yaml:"id"`
	Name        string            `json:"name" yaml:"name"`
	Type        string            `json:"type" yaml:"type"`
	Command     []string          `json:"command,omitempty" yaml:"command,omitempty"`
	Environment map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
	Permission  []PermissionRule  `json:"permission,omitempty" yaml:"permission,omitempty"`
	Outputs     []Collector       `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Agent       *AgentConfig      `json:"agent,omitempty" yaml:"agent,omitempty"`
	Resources   []ResourceRequest `json:"resources,omitempty" yaml:"resources,omitempty"`
	Lease       *LeaseConfig      `json:"lease,omitempty" yaml:"lease,omitempty"`
	Subworkflow *SubworkflowRef   `json:"subworkflow,omitempty" yaml:"subworkflow,omitempty"`
	Map         *MapConfig        `json:"map,omitempty" yaml:"map,omitempty"`
	Join        *JoinConfig       `json:"join,omitempty" yaml:"join,omitempty"`
	Repeat      *RepeatConfig     `json:"repeat,omitempty" yaml:"repeat,omitempty"`
}

// RepeatConfig retries a successful node until Until evaluates true. The
// attempt limit is mandatory: workflow graphs stay acyclic and bounded.
type RepeatConfig struct {
	Until       string `json:"until" yaml:"until"`
	MaxAttempts int    `json:"maxAttempts" yaml:"maxAttempts"`
}

// SubworkflowRef references a reusable workflow to inline. At publish
// time the referenced workflow's active version is resolved and its
// (already-inlined) nodes are pinned into the parent definition, so a
// later edit to the subworkflow cannot alter an existing parent version.
type SubworkflowRef struct {
	WorkflowID string `json:"workflowId" yaml:"workflowId"`
}

// MapConfig fans a declared JSON array artifact out across per-item
// pinned subworkflow runs. Source names the upstream artifact (a JSON
// array); Key is a field on each item object used as the stable,
// duplicate-free key so restart/retry never reprocesses a completed
// item. Subworkflow is the pinned per-item pipeline. Join is the id of
// the join node that aggregates this map's items.
type MapConfig struct {
	Source      string         `json:"source" yaml:"source"`
	Key         string         `json:"key" yaml:"key"`
	Subworkflow SubworkflowRef `json:"subworkflow" yaml:"subworkflow"`
	Join        string         `json:"join" yaml:"join"`
	FailFast    bool           `json:"failFast,omitempty" yaml:"failFast,omitempty"`
	// VersionID pins the per-item subworkflow to a concrete active
	// version at parent publish time so downstream edits cannot alter an
	// existing parent version's mapped execution.
	VersionID string `json:"versionId,omitempty" yaml:"versionId,omitempty"`
}

// JoinConfig aggregates a map node's per-item outcomes into an
// input-ordered result with an explicit success policy.
//
//	all-success   – join succeeds only if every item succeeded.
//	always        – join always succeeds, carrying per-item statuses.
//	minimum-success – join succeeds if at least MinSuccess items did.
type JoinConfig struct {
	Policy     string `json:"policy" yaml:"policy"`
	MinSuccess int    `json:"minSuccess,omitempty" yaml:"minSuccess,omitempty"`
}

const (
	JoinAllSuccess     = "all-success"
	JoinAlways         = "always"
	JoinMinimumSuccess = "minimum-success"
)

type PermissionRule struct {
	Permission string `json:"permission" yaml:"permission"`
	Pattern    string `json:"pattern" yaml:"pattern"`
	Action     string `json:"action" yaml:"action"`
}

type Collector struct {
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

type AgentConfig struct {
	Platform        string      `json:"platform,omitempty" yaml:"platform,omitempty"`
	Directory       string      `json:"directory" yaml:"directory"`
	Prompt          string      `json:"prompt" yaml:"prompt"`
	Model           string      `json:"model,omitempty" yaml:"model,omitempty"`
	Agent           string      `json:"agent,omitempty" yaml:"agent,omitempty"`
	Reasoning       string      `json:"reasoning,omitempty" yaml:"reasoning,omitempty"`
	SessionAffinity string      `json:"sessionAffinity,omitempty" yaml:"sessionAffinity,omitempty"`
	Collectors      []Collector `json:"collectors,omitempty" yaml:"collectors,omitempty"`
}

type AgentRequest struct {
	Platform  string
	Directory string
	Prompt    string
	Model     string
	Agent     string
	Reasoning string
	SessionID string
}

type AgentSession struct {
	ID        string
	Platform  string
	State     string
	Directory string
	Error     string
}

type AgentResult struct {
	State   string
	Outputs map[string]json.RawMessage
	Error   string
}

type AgentExecutor interface {
	Start(context.Context, AgentRequest) (AgentSession, error)
	Inspect(context.Context, AgentSession, []Collector) (AgentResult, error)
	Cancel(context.Context, AgentSession) error
}

type Dependency struct {
	From      string `json:"from" yaml:"from"`
	To        string `json:"to" yaml:"to"`
	Condition string `json:"condition,omitempty" yaml:"condition,omitempty"`
}

type Validation struct {
	Definition    Definition      `json:"definition"`
	CanonicalJSON json.RawMessage `json:"canonicalJson"`
	YAML          string          `json:"yaml"`
}

type Version struct {
	ID            string          `json:"id"`
	WorkflowID    string          `json:"workflowId"`
	Name          string          `json:"name"`
	Revision      int             `json:"revision"`
	CreatedAt     int64           `json:"createdAt"`
	Definition    Definition      `json:"definition"`
	TriggerStates []TriggerStatus `json:"triggerStates"`
	Active        bool            `json:"active"`
}

type Run struct {
	ID           string           `json:"id"`
	WorkflowID   string           `json:"workflowId"`
	VersionID    string           `json:"versionId"`
	State        string           `json:"state"`
	CreatedAt    int64            `json:"createdAt"`
	UpdatedAt    int64            `json:"updatedAt"`
	CompletedAt  int64            `json:"completedAt,omitempty"`
	Trigger      *TriggerSnapshot `json:"trigger,omitempty"`
	ParentRunID  string           `json:"parentRunId,omitempty"`
	ParentNodeID string           `json:"parentNodeId,omitempty"`
	ItemKey      string           `json:"itemKey,omitempty"`
	ItemIndex    int              `json:"itemIndex,omitempty"`
}

type RunDetail struct {
	Run
	Version   Version          `json:"version"`
	Nodes     []NodeRun        `json:"nodes"`
	Resources []ResourcePool   `json:"resources,omitempty"`
	Workspace []WorkspaceLease `json:"workspace,omitempty"`
	// Children summarizes mapped-item child runs for the run UI.
	Children []MapItemRun `json:"children,omitempty"`
}

// WorkspaceLease is the run-UI view of a held shard lease: which node/
// attempt owns which shard, in what mode, over which paths, and on which
// (optional) host.
type WorkspaceLease struct {
	NodeID     string   `json:"nodeId"`
	AttemptID  int64    `json:"attemptId"`
	Shard      int      `json:"shard"`
	Mode       string   `json:"mode"`
	Paths      []string `json:"paths,omitempty"`
	Commit     bool     `json:"commit,omitempty"`
	Host       string   `json:"host,omitempty"`
	ShardPath  string   `json:"shardPath,omitempty"`
	AcquiredAt int64    `json:"acquiredAt"`
}

// MapItemRun is one mapped item's summary for the run UI: its owning map
// node, stable key, input order, executing child run, and terminal state.
type MapItemRun struct {
	MapNode    string `json:"mapNode"`
	Key        string `json:"key"`
	Index      int    `json:"index"`
	ChildRunID string `json:"childRunId,omitempty"`
	State      string `json:"state"`
}

// ResourcePool is the live view of one pool for a run: its capacity, how
// many units are currently held, and which ready nodes are waiting on it.
type ResourcePool struct {
	Pool     string   `json:"pool"`
	Capacity int      `json:"capacity"`
	Held     int      `json:"held"`
	Waiting  []string `json:"waiting,omitempty"`
}

type NodeRun struct {
	NodeID          string    `json:"nodeId"`
	Name            string    `json:"name"`
	Type            string    `json:"type"`
	State           string    `json:"state"`
	ReadyAt         int64     `json:"readyAt,omitempty"`
	CompletedAt     int64     `json:"completedAt,omitempty"`
	Attempts        []Attempt `json:"attempts"`
	PinnedVersionID string    `json:"pinnedVersionId,omitempty"`
}

type Attempt struct {
	ID              int64                      `json:"id"`
	Seq             int                        `json:"seq"`
	State           string                     `json:"state"`
	StartedAt       int64                      `json:"startedAt"`
	CompletedAt     int64                      `json:"completedAt,omitempty"`
	ExitCode        *int                       `json:"exitCode,omitempty"`
	Stdout          string                     `json:"stdout,omitempty"`
	Stderr          string                     `json:"stderr,omitempty"`
	Error           string                     `json:"error,omitempty"`
	Outputs         map[string]json.RawMessage `json:"outputs,omitempty"`
	StdoutTruncated bool                       `json:"stdoutTruncated,omitempty"`
	StderrTruncated bool                       `json:"stderrTruncated,omitempty"`
	Platform        string                     `json:"platform,omitempty"`
	SessionID       string                     `json:"sessionId,omitempty"`
	SessionState    string                     `json:"sessionState,omitempty"`
	Affinity        string                     `json:"-"`
	Directory       string                     `json:"-"`
}

type Store interface {
	InsertWorkflowVersion(state.WorkflowVersion) (state.WorkflowVersion, error)
	GetWorkflowVersion(string) (*state.WorkflowVersion, error)
	GetActiveWorkflowVersion(string) (*state.WorkflowVersion, error)
	ListWorkflowVersions() ([]state.WorkflowVersion, error)
	ActivateWorkflowVersion(string, int64) (*state.WorkflowVersion, error)
	InsertWorkflowRun(state.WorkflowRun) error
	GetWorkflowRun(string) (*state.WorkflowRun, error)
	ListWorkflowRuns() ([]state.WorkflowRun, error)
	ListCurrentWorkflowVersions() ([]state.WorkflowVersion, error)
	ListQueuedWorkflowVersions() ([]state.WorkflowVersion, error)
	GetWorkflowTriggerState(string, string) (state.WorkflowTriggerState, error)
	UpsertWorkflowTriggerState(state.WorkflowTriggerState) error
	CommitWorkflowTriggerFiring(*state.WorkflowRun, state.WorkflowTriggerFiring, state.WorkflowTriggerState) error
	CountActiveWorkflowTriggerRuns(string, string) (int, error)
	ActiveWorkflowTriggerRunID(string, string) (string, error)
	CountQueuedWorkflowTriggerFirings(string, string) (int, error)
	NextQueuedWorkflowTriggerFiring(string, string) (*state.WorkflowTriggerFiring, error)
	InsertWorkflowRunFromQueued(state.WorkflowRun, int64, int64, state.WorkflowTriggerState) error
	ApproveWorkflowNode(string, string, int64) error
	StartWorkflowCommand(string, string, []state.WorkflowResourceRequest, *state.WorkflowWorkspaceRequest, int64) (bool, error)
	CompleteWorkflowCommand(string, string, state.WorkflowCommandResult, int64) error
	SetWorkflowRunState(string, string, string, int64) error
	FailWorkflowRun(string, string, int64) error
	ClaimWorkflowAgentAttempt(string, string, int64, string, string, []state.WorkflowResourceRequest, *state.WorkflowWorkspaceRequest, int64) (bool, error)
	ListWorkflowResourceLeases(string) ([]state.WorkflowResourceLease, error)
	ListWorkflowWorkspaceLeases(string) ([]state.WorkflowWorkspaceLease, error)
	SetWorkflowWorkspaceShardPath(string, string, string) error
	AttachWorkflowAgentSession(string, string, int64, string, string, string, int64) error
	SetWorkflowAgentSessionState(string, string, int64, string, string, int64) error
	CompleteWorkflowAgentNode(string, string, int64, bool, string, string, string, int64) error
	ResolveWorkflowAttempt(string, int64, string, int64) error
	InsertWorkflowArtifact(state.WorkflowArtifact) error
	ListWorkflowArtifacts(string) ([]state.WorkflowArtifact, error)
	GetWorkflowArtifact(string) (*state.WorkflowArtifact, error)
	ExpiredWorkflowArtifactHashes(int64) ([]string, error)
	MarkWorkflowArtifactPayloadDeleted(string) error
	CreateWorkflowMapItem(state.WorkflowMapItem, state.WorkflowRun) (bool, error)
	ListWorkflowMapItems(string, string) ([]state.WorkflowMapItem, error)
	SetWorkflowMapItemState(string, string, string, string) error
	ListWorkflowChildRuns(string) ([]state.WorkflowRun, error)
	StartWorkflowNode(string, string, []state.WorkflowResourceRequest, int64) (bool, error)
	SettleWorkflowNode(string, string, int64, bool, string, string, int64) error
	SkipWorkflowNode(string, string, string, int64) error
	RepeatWorkflowNode(string, string, int64, string, int64) error
	ExhaustWorkflowRepeat(string, string, string, int64) error
}

// WorkspaceProvider creates (or reuses) the on-disk worktree shard for a
// run through the existing host/worktree services. Shard is the index in
// the run's bounded pool. Repo is the workflow's declared repository root.
// Returns the shard's working directory. When nil, mutating nodes run in
// the workflow directory itself (single-worktree fallback).
type WorkspaceProvider interface {
	EnsureShard(ctx context.Context, runID, repo string, shard int) (string, error)
}

type Deps struct {
	Store           Store
	Workspace       WorkspaceProvider
	Now             func() time.Time
	Notify          func(runID string)
	NotifyTrigger   func()
	Forge           loops.ForgePoller
	Status          loops.SessionStatusInferer
	CommandExecutor CommandExecutor
	Agent           AgentExecutor
	Usage           loops.UsageSource
	// Blobs is the content-addressed payload store for artifacts. When
	// nil, artifact payloads are not persisted (metadata-only fallback).
	Blobs *BlobStore
	// ResolveSecret maps a host env var name to its value at execution
	// time. Defaults to os.Getenv. Returns "" for unset vars.
	ResolveSecret func(env string) string
}

type Service struct {
	store         Store
	now           func() time.Time
	notify        func(string)
	notifyTrigger func()
	forge         loops.ForgePoller
	status        loops.SessionStatusInferer
	executor      CommandExecutor
	agent         AgentExecutor
	usage         loops.UsageSource
	workspace     WorkspaceProvider
	blobs         *BlobStore
	resolveSecret func(string) string
	dispatchMu    sync.Mutex
	triggerMu     sync.Mutex
	mu            sync.Mutex
	running       map[string]map[string]*activeCommand
	stopping      map[string]bool
}

type activeCommand struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func NewService(deps Deps) *Service {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	executor := deps.CommandExecutor
	if executor == nil {
		executor = localCommandExecutor{}
	}
	resolveSecret := deps.ResolveSecret
	if resolveSecret == nil {
		resolveSecret = os.Getenv
	}
	return &Service{
		store:         deps.Store,
		now:           now,
		notify:        deps.Notify,
		notifyTrigger: deps.NotifyTrigger,
		forge:         deps.Forge,
		status:        deps.Status,
		executor:      executor,
		agent:         deps.Agent,
		usage:         deps.Usage,
		workspace:     deps.Workspace,
		blobs:         deps.Blobs,
		resolveSecret: resolveSecret,
		running:       make(map[string]map[string]*activeCommand),
		stopping:      make(map[string]bool),
	}
}

func (s *Service) ValidateJSON(_ context.Context, source []byte) (Definition, error) {
	definition, _, err := s.prepareDefinition(source)
	return definition, err
}

// prepareDefinition decodes, validates the authored definition, then
// resolves + pins + inlines its subworkflow references and validates the
// resulting graph. The returned definition is the canonical, fully
// inlined form persisted for a new version.
func (s *Service) prepareDefinition(source []byte) (Definition, []byte, error) {
	authored, _, _, err := decodeDefinition(source)
	if err != nil {
		return Definition{}, nil, err
	}
	if err := validateAuthoredDefinition(authored); err != nil {
		return Definition{}, nil, err
	}
	inlined, err := s.inlineSubworkflows(authored)
	if err != nil {
		return Definition{}, nil, err
	}
	if err := validateDefinition(inlined); err != nil {
		return Definition{}, nil, err
	}
	canonical, err := json.Marshal(inlined)
	if err != nil {
		return Definition{}, nil, fmt.Errorf("encoding workflow definition: %w", err)
	}
	return inlined, canonical, nil
}

func (s *Service) PublishJSON(_ context.Context, source []byte) (Version, error) {
	definition, canonical, err := s.prepareDefinition(source)
	if err != nil {
		return Version{}, err
	}
	now := s.now().UnixMilli()
	row := state.WorkflowVersion{
		ID:              newID("wfv"),
		WorkflowID:      definition.ID,
		Name:            definition.Name,
		MetadataVersion: definition.Version,
		DefinitionJSON:  string(canonical),
		Concurrency:     definition.Concurrency,
		RetentionDays:   definition.RetentionDays,
		CreatedAt:       now,
	}
	row.Nodes, row.Dependencies = versionNodeRows(definition)
	row, err = s.store.InsertWorkflowVersion(row)
	if err != nil {
		return Version{}, err
	}
	return versionFromState(row, definition), nil
}

func (s *Service) Publish(ctx context.Context, source []byte) (Version, error) {
	return s.PublishJSON(ctx, source)
}

func (s *Service) Validate(_ context.Context, source []byte) (Validation, error) {
	definition, canonical, err := s.prepareDefinition(source)
	if err != nil {
		return Validation{}, err
	}
	stableYAML, err := encodeDefinitionYAML(definition)
	if err != nil {
		return Validation{}, err
	}
	return Validation{Definition: definition, CanonicalJSON: canonical, YAML: stableYAML}, nil
}

func (s *Service) Activate(_ context.Context, id string) (Version, error) {
	row, err := s.store.ActivateWorkflowVersion(id, s.now().UnixMilli())
	if err != nil {
		return Version{}, err
	}
	return versionFromRow(*row)
}

func (s *Service) StartActive(ctx context.Context, workflowID string) (RunDetail, error) {
	version, err := s.store.GetActiveWorkflowVersion(workflowID)
	if err != nil {
		return RunDetail{}, err
	}
	return s.Start(ctx, version.ID)
}

func (s *Service) GetVersion(_ context.Context, id string) (Version, error) {
	row, err := s.store.GetWorkflowVersion(id)
	if err != nil {
		return Version{}, err
	}
	version, err := versionFromRow(*row)
	if err != nil {
		return Version{}, err
	}
	version.TriggerStates, err = s.triggerStatuses(id, version.Definition.Triggers)
	return version, err
}

func (s *Service) ExportYAML(ctx context.Context, id string) (string, error) {
	version, err := s.GetVersion(ctx, id)
	if err != nil {
		return "", err
	}
	canonical, err := json.Marshal(version.Definition)
	if err != nil {
		return "", fmt.Errorf("encoding workflow definition: %w", err)
	}
	validated, err := s.Validate(ctx, canonical)
	if err != nil {
		return "", err
	}
	return validated.YAML, nil
}

func (s *Service) ListVersions(_ context.Context) ([]Version, error) {
	rows, err := s.store.ListWorkflowVersions()
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(rows))
	for _, row := range rows {
		version, err := versionFromRow(row)
		if err != nil {
			return nil, err
		}
		version.TriggerStates, err = s.triggerStatuses(row.ID, version.Definition.Triggers)
		if err != nil {
			return nil, err
		}
		out = append(out, version)
	}
	return out, nil
}

func (s *Service) triggerStatuses(versionID string, triggers []Trigger) ([]TriggerStatus, error) {
	out := make([]TriggerStatus, 0, len(triggers))
	for _, trigger := range triggers {
		row, err := s.triggerState(versionID, trigger.ID)
		if err != nil {
			return nil, err
		}
		queued, err := s.store.CountQueuedWorkflowTriggerFirings(versionID, trigger.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, TriggerStatus{Trigger: normalizedTrigger(trigger), VersionID: versionID, LastCheckedAt: row.LastCheckedAt, NextCheckAt: row.NextCheckAt, LastFiredAt: row.LastFiredAt, LastDecision: row.LastDecision, LastRunID: row.LastRunID, Queued: queued})
	}
	return out, nil
}

func (s *Service) Start(ctx context.Context, versionID string) (RunDetail, error) {
	version, err := s.store.GetWorkflowVersion(versionID)
	if err != nil {
		version, err = s.store.GetActiveWorkflowVersion(versionID)
		if err != nil {
			return RunDetail{}, fmt.Errorf("workflow version or definition %q not found: %w", versionID, err)
		}
	}
	parsed, err := versionFromRow(*version)
	if err != nil {
		return RunDetail{}, err
	}
	for _, trigger := range parsed.Definition.Triggers {
		if trigger.Type == TriggerManual {
			run, err := s.fire(ctx, *version, trigger, "manual", s.now().UnixMilli())
			if err != nil || run.ID != "" {
				return run, err
			}
			status, err := s.GetTrigger(ctx, version.ID, trigger.ID)
			if err != nil || status.LastRunID == "" {
				return RunDetail{}, err
			}
			return s.GetRun(ctx, status.LastRunID)
		}
	}
	return RunDetail{}, fmt.Errorf("workflow version has no manual trigger")
}

func (s *Service) newRun(version state.WorkflowVersion, snapshot TriggerSnapshot) state.WorkflowRun {
	dependencies := make(map[string]bool, len(version.Dependencies))
	for _, dep := range version.Dependencies {
		dependencies[dep.To] = true
	}
	now := s.now().UnixMilli()
	run := state.WorkflowRun{
		ID:         newID("wfr"),
		WorkflowID: version.WorkflowID,
		VersionID:  version.ID,
		State:      StateActive,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		panic(err)
	}
	run.TriggerSnapshotJSON = string(snapshotJSON)
	for _, node := range version.Nodes {
		nodeState := NodePending
		readyAt := int64(0)
		if !dependencies[node.ID] {
			nodeState = NodeReady
			readyAt = now
		}
		run.Nodes = append(run.Nodes, state.WorkflowNodeRun{NodeID: node.ID, Type: node.Type, Position: node.Position, State: nodeState, ReadyAt: readyAt})
	}
	return run
}

func (s *Service) fire(ctx context.Context, version state.WorkflowVersion, trigger Trigger, detail string, firedAt int64) (RunDetail, error) {
	s.triggerMu.Lock()
	defer s.triggerMu.Unlock()
	return s.fireLocked(ctx, version, trigger, detail, firedAt, nil)
}

func (s *Service) fireLocked(ctx context.Context, version state.WorkflowVersion, trigger Trigger, detail string, firedAt int64, pendingState *state.WorkflowTriggerState) (RunDetail, error) {
	trigger = normalizedTrigger(trigger)
	snapshot := TriggerSnapshot{Trigger: trigger, VersionID: version.ID, FiredAt: firedAt, Detail: detail}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return RunDetail{}, err
	}
	activeRunID, err := s.store.ActiveWorkflowTriggerRunID(version.ID, trigger.ID)
	if err != nil {
		return RunDetail{}, err
	}
	decision := DecisionStarted
	if activeRunID != "" && trigger.Overlap != OverlapParallel {
		decision = DecisionSkipped
		if trigger.Overlap == OverlapQueue {
			decision = DecisionQueued
		}
	}
	var stateRow state.WorkflowTriggerState
	if pendingState != nil {
		stateRow = *pendingState
	} else if stateRow, err = s.triggerState(version.ID, trigger.ID); err != nil {
		return RunDetail{}, err
	}
	stateRow.LastFiredAt, stateRow.LastDecision = firedAt, decision
	if activeRunID != "" {
		stateRow.LastRunID = activeRunID
	}
	firing := state.WorkflowTriggerFiring{VersionID: version.ID, TriggerID: trigger.ID, FiredAt: firedAt, Detail: detail, SnapshotJSON: string(snapshotJSON), Decision: decision}
	if decision != DecisionStarted {
		if err := s.store.CommitWorkflowTriggerFiring(nil, firing, stateRow); err != nil {
			return RunDetail{}, err
		}
		s.triggerChanged()
		s.changed(stateRow.LastRunID)
		return RunDetail{}, nil
	}
	run := s.newRun(version, snapshot)
	firing.RunID, firing.StartedAt = run.ID, s.now().UnixMilli()
	stateRow.LastRunID = run.ID
	if err := s.store.CommitWorkflowTriggerFiring(&run, firing, stateRow); err != nil {
		return RunDetail{}, err
	}
	s.triggerChanged()
	s.changed(run.ID)
	if err := s.dispatch(ctx, run.ID); err != nil {
		return RunDetail{}, err
	}
	return s.GetRun(ctx, run.ID)
}

func (s *Service) ListRuns(_ context.Context) ([]Run, error) {
	rows, err := s.store.ListWorkflowRuns()
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(rows))
	for _, row := range rows {
		// Mapped-item child runs are nested under their parent in the run
		// detail view; keep the top-level list to real workflow runs.
		if row.ParentRunID != "" {
			continue
		}
		out = append(out, runFromState(row))
	}
	return out, nil
}

func (s *Service) GetTrigger(_ context.Context, versionID, triggerID string) (TriggerStatus, error) {
	version, err := s.store.GetWorkflowVersion(versionID)
	if err != nil {
		return TriggerStatus{}, err
	}
	parsed, err := versionFromRow(*version)
	if err != nil {
		return TriggerStatus{}, err
	}
	var trigger Trigger
	found := false
	for _, candidate := range parsed.Definition.Triggers {
		if candidate.ID == triggerID {
			trigger, found = normalizedTrigger(candidate), true
			break
		}
	}
	if !found {
		return TriggerStatus{}, fmt.Errorf("workflow trigger %q not found", triggerID)
	}
	row, err := s.triggerState(versionID, triggerID)
	if err != nil {
		return TriggerStatus{}, err
	}
	queued, err := s.store.CountQueuedWorkflowTriggerFirings(versionID, triggerID)
	if err != nil {
		return TriggerStatus{}, err
	}
	return TriggerStatus{Trigger: trigger, VersionID: versionID, LastCheckedAt: row.LastCheckedAt, NextCheckAt: row.NextCheckAt, LastFiredAt: row.LastFiredAt, LastDecision: row.LastDecision, LastRunID: row.LastRunID, Queued: queued}, nil
}

func (s *Service) EvaluateTriggers(ctx context.Context) error {
	s.triggerMu.Lock()
	defer s.triggerMu.Unlock()
	var errs []error
	queuedVersions, err := s.store.ListQueuedWorkflowVersions()
	if err != nil {
		return err
	}
	for _, listed := range queuedVersions {
		version, err := s.store.GetWorkflowVersion(listed.ID)
		if err != nil {
			return err
		}
		parsed, err := versionFromRow(*version)
		if err != nil {
			return err
		}
		// The migration preserves loop data as workflow history, but legacy
		// loops still execute through their existing engine for one release.
		if len(parsed.Definition.LoopCompat) != 0 {
			continue
		}
		for _, trigger := range parsed.Definition.Triggers {
			if _, err := s.drainQueued(ctx, *version, normalizedTrigger(trigger)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	versions, err := s.store.ListCurrentWorkflowVersions()
	if err != nil {
		return err
	}
	for _, listed := range versions {
		version, err := s.store.GetWorkflowVersion(listed.ID)
		if err != nil {
			return err
		}
		parsed, err := versionFromRow(*version)
		if err != nil {
			return err
		}
		if len(parsed.Definition.LoopCompat) != 0 {
			continue
		}
		for _, trigger := range parsed.Definition.Triggers {
			if trigger.Type == TriggerManual {
				continue
			}
			if err := s.evaluateTrigger(ctx, *version, normalizedTrigger(trigger)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Service) evaluateTrigger(ctx context.Context, version state.WorkflowVersion, trigger Trigger) error {
	row, err := s.triggerState(version.ID, trigger.ID)
	if err != nil {
		return err
	}
	if drained, err := s.drainQueued(ctx, version, trigger); err != nil || drained {
		return err
	}
	now := s.now()
	if row.NextCheckAt > now.UnixMilli() {
		return nil
	}
	fire, detail, detection, running, err := s.shouldFire(ctx, trigger, row, now)
	if err != nil {
		return err
	}
	row.LastCheckedAt = now.UnixMilli()
	lastFired := row.LastFiredAt
	if fire {
		lastFired = now.UnixMilli()
	}
	row.NextCheckAt = nextCheck(trigger, lastFired, now)
	if detection != nil {
		encoded, _ := json.Marshal(detection)
		row.DetectionJSON = string(encoded)
	}
	if running != nil {
		row.LastRunning = running
	}
	if !fire {
		if err := s.store.UpsertWorkflowTriggerState(row); err != nil {
			return err
		}
		s.triggerChanged()
		return nil
	}
	_, err = s.fireLocked(ctx, version, trigger, detail, now.UnixMilli(), &row)
	return err
}

func (s *Service) drainQueued(ctx context.Context, version state.WorkflowVersion, trigger Trigger) (bool, error) {
	active, err := s.store.CountActiveWorkflowTriggerRuns(version.ID, trigger.ID)
	if err != nil || active > 0 {
		return false, err
	}
	queued, err := s.store.NextQueuedWorkflowTriggerFiring(version.ID, trigger.ID)
	if err != nil || queued == nil {
		return false, err
	}
	var snapshot TriggerSnapshot
	if err := json.Unmarshal([]byte(queued.SnapshotJSON), &snapshot); err != nil {
		return false, err
	}
	row, err := s.triggerState(version.ID, trigger.ID)
	if err != nil {
		return false, err
	}
	run := s.newRun(version, snapshot)
	row.LastDecision, row.LastRunID = DecisionStarted, run.ID
	if err := s.store.InsertWorkflowRunFromQueued(run, queued.ID, s.now().UnixMilli(), row); err != nil {
		return false, err
	}
	s.triggerChanged()
	s.changed(run.ID)
	if err := s.dispatch(ctx, run.ID); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Service) shouldFire(ctx context.Context, trigger Trigger, row state.WorkflowTriggerState, now time.Time) (bool, string, *loops.TriggerConfig, *bool, error) {
	switch trigger.Type {
	case TriggerChildCompletion, TriggerTurnCompletion:
		if s.status == nil {
			return false, "", nil, nil, fmt.Errorf("%s trigger requires session status", trigger.Type)
		}
		running, ok := s.status.TurnRunning(ctx, trigger.Platform, trigger.SessionID)
		if !ok {
			return false, "", nil, nil, nil
		}
		fire := row.LastRunning != nil && *row.LastRunning && !running
		detail := "turn complete"
		if trigger.Type == TriggerChildCompletion {
			detail = "child complete"
		}
		return fire, detail, nil, &running, nil
	}
	config := loops.TriggerConfig{IntervalSeconds: trigger.IntervalSeconds, CronExpr: trigger.Cron, PRNumber: trigger.PRNumber, PollSeconds: trigger.PollSeconds}
	if row.DetectionJSON != "" {
		_ = json.Unmarshal([]byte(row.DetectionJSON), &config)
	}
	loopType := loops.TriggerSchedule
	last := row.LastFiredAt
	switch trigger.Type {
	case TriggerCron:
		loopType = loops.TriggerCron
		if last == 0 {
			last = row.LastCheckedAt
		}
	case TriggerPR:
		loopType, last = loops.TriggerPREvent, 0
	}
	fire, detail, updated, err := loops.EvaluateTrigger(ctx, loopType, state.Loop{LastFiredAt: last, Directory: trigger.Directory, Platform: trigger.Platform, RootSessionID: trigger.SessionID}, config, now, s.status, s.forge)
	return fire, detail, updated, nil, err
}

func nextCheck(trigger Trigger, lastFired int64, now time.Time) int64 {
	switch trigger.Type {
	case TriggerInterval:
		interval := time.Duration(trigger.IntervalSeconds) * time.Second
		if interval < loops.MinScheduleInterval {
			interval = loops.MinScheduleInterval
		}
		if lastFired == 0 {
			return now.Add(interval).UnixMilli()
		}
		return time.UnixMilli(lastFired).Add(interval).UnixMilli()
	case TriggerCron:
		next, ok, _ := loops.NextCron(trigger.Cron, now)
		if ok {
			return next.UnixMilli()
		}
	case TriggerPR:
		poll := time.Duration(trigger.PollSeconds) * time.Second
		if poll < loops.MinPRPollInterval {
			poll = loops.MinPRPollInterval
		}
		return now.Add(poll).UnixMilli()
	case TriggerChildCompletion, TriggerTurnCompletion:
		return now.Add(5 * time.Second).UnixMilli()
	}
	return 0
}

func (s *Service) triggerState(versionID, triggerID string) (state.WorkflowTriggerState, error) {
	row, err := s.store.GetWorkflowTriggerState(versionID, triggerID)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return row, err
	}
	return state.WorkflowTriggerState{VersionID: versionID, TriggerID: triggerID, DetectionJSON: "{}"}, nil
}

func (s *Service) GetRun(ctx context.Context, id string) (RunDetail, error) {
	run, err := s.store.GetWorkflowRun(id)
	if err != nil {
		return RunDetail{}, err
	}
	version, err := s.GetVersion(ctx, run.VersionID)
	if err != nil {
		return RunDetail{}, err
	}
	detail := RunDetail{Run: runFromState(*run), Version: version, Nodes: make([]NodeRun, 0, len(run.Nodes))}
	for _, row := range run.Nodes {
		node := NodeRun{NodeID: row.NodeID, Name: row.Name, Type: row.Type, State: row.State, ReadyAt: row.ReadyAt, CompletedAt: row.CompletedAt, Attempts: make([]Attempt, 0, len(row.Attempts)), PinnedVersionID: row.PinnedVersionID}
		for _, attempt := range row.Attempts {
			out := Attempt{ID: attempt.ID, Seq: attempt.Seq, State: attempt.State, StartedAt: attempt.StartedAt, CompletedAt: attempt.CompletedAt, ExitCode: attempt.ExitCode, Stdout: attempt.Stdout, Stderr: attempt.Stderr, Error: attempt.Error, StdoutTruncated: attempt.StdoutTruncated, StderrTruncated: attempt.StderrTruncated, Platform: attempt.Platform, SessionID: attempt.SessionID, SessionState: attempt.SessionState, Affinity: attempt.Affinity, Directory: attempt.Directory}
			if attempt.OutputsJSON != "" && attempt.OutputsJSON != "{}" {
				if err := json.Unmarshal([]byte(attempt.OutputsJSON), &out.Outputs); err != nil {
					return RunDetail{}, fmt.Errorf("decoding workflow outputs: %w", err)
				}
			}
			node.Attempts = append(node.Attempts, out)
		}
		detail.Nodes = append(detail.Nodes, node)
	}
	leases, err := s.store.ListWorkflowResourceLeases(id)
	if err != nil {
		return RunDetail{}, err
	}
	detail.Resources = resourceView(detail, leases)
	workspace, err := s.store.ListWorkflowWorkspaceLeases(id)
	if err != nil {
		return RunDetail{}, err
	}
	for _, lease := range workspace {
		detail.Workspace = append(detail.Workspace, WorkspaceLease{
			NodeID: lease.NodeID, AttemptID: lease.AttemptID, Shard: lease.Shard, Mode: lease.Mode,
			Paths: lease.Paths, Commit: lease.CommitLease, Host: lease.Host, ShardPath: lease.ShardPath, AcquiredAt: lease.AcquiredAt,
		})
	}
	children, err := s.mapItemRuns(detail)
	if err != nil {
		return RunDetail{}, err
	}
	detail.Children = children
	return detail, nil
}

// mapItemRuns collects every mapped item across the run's map nodes, in
// input order, so the run UI can nest and link child runs under their
// parent map node.
func (s *Service) mapItemRuns(detail RunDetail) ([]MapItemRun, error) {
	var out []MapItemRun
	for _, node := range detail.Nodes {
		if node.Type != "map" {
			continue
		}
		items, err := s.store.ListWorkflowMapItems(detail.ID, node.NodeID)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			out = append(out, MapItemRun{MapNode: item.MapNode, Key: item.ItemKey, Index: item.ItemIndex, ChildRunID: item.ChildRunID, State: item.State})
		}
	}
	return out, nil
}

// resourceView derives the live per-pool state (capacity, held units, and
// ready nodes still waiting on capacity) for the run UI and restart
// reconciliation. The implicit run-concurrency pool is reported as "run".
func resourceView(detail RunDetail, leases []state.WorkflowResourceLease) []ResourcePool {
	def := detail.Version.Definition
	order := []string{""}
	capacity := map[string]int{"": def.Concurrency}
	for _, pool := range def.Pools {
		order = append(order, pool.Name)
		capacity[pool.Name] = pool.Capacity
	}
	held := map[string]int{}
	holder := map[string]bool{} // "pool\x00node" holds capacity
	for _, lease := range leases {
		held[lease.Pool] += lease.Units
		holder[lease.Pool+"\x00"+lease.NodeID] = true
	}
	waiting := map[string][]string{}
	for _, node := range detail.Nodes {
		if node.State != NodeReady && node.State != NodeRunning {
			continue
		}
		active := len(node.Attempts) > 0 && node.Attempts[len(node.Attempts)-1].State == AttemptWaiting
		if !active {
			continue
		}
		for _, req := range resourceRequests(def, node.NodeID) {
			if holder[req.Pool+"\x00"+node.NodeID] {
				continue
			}
			waiting[req.Pool] = append(waiting[req.Pool], node.NodeID)
		}
	}
	out := make([]ResourcePool, 0, len(order))
	for _, pool := range order {
		out = append(out, ResourcePool{Pool: pool, Capacity: capacity[pool], Held: held[pool], Waiting: waiting[pool]})
	}
	return out
}

func (s *Service) Approve(ctx context.Context, runID, nodeID string) (RunDetail, error) {
	if err := s.store.ApproveWorkflowNode(runID, nodeID, s.now().UnixMilli()); err != nil {
		return RunDetail{}, err
	}
	s.changed(runID)
	if run, err := s.GetRun(ctx, runID); err == nil {
		if _, err := s.applyPolicies(ctx, run); err != nil {
			return RunDetail{}, err
		}
	}
	if err := s.dispatch(ctx, runID); err != nil {
		return RunDetail{}, err
	}
	return s.GetRun(ctx, runID)
}

func (s *Service) Pause(ctx context.Context, runID string) (RunDetail, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	if err := s.store.SetWorkflowRunState(runID, StateActive, StatePaused, s.now().UnixMilli()); err != nil {
		return RunDetail{}, err
	}
	s.changed(runID)
	return s.GetRun(ctx, runID)
}

func (s *Service) Resume(ctx context.Context, runID string) (RunDetail, error) {
	if err := s.store.SetWorkflowRunState(runID, StatePaused, StateActive, s.now().UnixMilli()); err != nil {
		return RunDetail{}, err
	}
	s.changed(runID)
	return s.GetRun(ctx, runID)
}

func (s *Service) ResolveUnknown(ctx context.Context, runID string, attemptID int64, resolution string) (RunDetail, error) {
	if resolution != AttemptSuccessful && resolution != AttemptFailed {
		return RunDetail{}, fmt.Errorf("resolution must be %q or %q", AttemptSuccessful, AttemptFailed)
	}
	if err := s.store.ResolveWorkflowAttempt(runID, attemptID, resolution, s.now().UnixMilli()); err != nil {
		return RunDetail{}, fmt.Errorf("resolving unknown attempt: %w", err)
	}
	s.changed(runID)
	return s.GetRun(ctx, runID)
}

func (s *Service) Cancel(ctx context.Context, runID string) (RunDetail, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	run, err := s.store.GetWorkflowRun(runID)
	if err != nil {
		return RunDetail{}, err
	}
	if run.State != StateActive && run.State != StatePaused {
		return RunDetail{}, fmt.Errorf("workflow run cannot be canceled from %s", run.State)
	}
	s.mu.Lock()
	s.stopping[runID] = true
	active := make([]*activeCommand, 0, len(s.running[runID]))
	for _, command := range s.running[runID] {
		active = append(active, command)
		command.cancel()
	}
	s.mu.Unlock()
	for _, command := range active {
		<-command.done
	}
	if s.agent != nil {
		for _, node := range run.Nodes {
			for _, attempt := range node.Attempts {
				if attempt.State != AttemptRunning || attempt.SessionID == "" {
					continue
				}
				err := s.agent.Cancel(ctx, AgentSession{ID: attempt.SessionID, Platform: attempt.Platform, State: attempt.SessionState, Directory: attempt.Directory})
				message := ""
				if err != nil {
					message = err.Error()
				}
				if stateErr := s.store.SetWorkflowAgentSessionState(runID, node.NodeID, attempt.ID, StateCanceled, message, s.now().UnixMilli()); stateErr != nil {
					return RunDetail{}, stateErr
				}
			}
		}
	}
	if err := s.store.SetWorkflowRunState(runID, run.State, StateCanceled, s.now().UnixMilli()); err != nil {
		return RunDetail{}, err
	}
	s.mu.Lock()
	delete(s.stopping, runID)
	s.mu.Unlock()
	// Cancel any mapped-item child runs so orphaned per-item work stops too.
	s.cancelChildRuns(ctx, runID)
	s.changed(runID)
	return s.GetRun(ctx, runID)
}

func (s *Service) dispatchReady(run RunDetail) {
	if run.State != StateActive {
		return
	}
	definitions := make(map[string]Node, len(run.Version.Definition.Nodes))
	for _, node := range run.Version.Definition.Nodes {
		definitions[node.ID] = node
	}
	for _, nodeRun := range run.Nodes {
		if nodeRun.Type != "command" || nodeRun.State != NodeReady || len(nodeRun.Attempts) == 0 || nodeRun.Attempts[len(nodeRun.Attempts)-1].State != AttemptWaiting {
			continue
		}
		s.mu.Lock()
		if s.stopping[run.ID] {
			s.mu.Unlock()
			return
		}
		if s.running[run.ID] == nil {
			s.running[run.ID] = make(map[string]*activeCommand)
		}
		if s.running[run.ID][nodeRun.NodeID] != nil {
			s.mu.Unlock()
			continue
		}
		// The store atomically holds run-concurrency and named-pool
		// capacity before flipping the attempt to running; a full pool
		// simply skips this node so a differently-provisioned ready
		// sibling still gets a fair chance.
		started, err := s.store.StartWorkflowCommand(run.ID, nodeRun.NodeID, resourceRequests(run.Version.Definition, nodeRun.NodeID), workspaceRequest(run.Version.Definition, nodeRun.NodeID), s.now().UnixMilli())
		if err != nil || !started {
			s.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		active := &activeCommand{cancel: cancel, done: make(chan struct{})}
		s.running[run.ID][nodeRun.NodeID] = active
		s.mu.Unlock()
		definition := definitions[nodeRun.NodeID]
		attemptID := nodeRun.Attempts[len(nodeRun.Attempts)-1].ID
		go s.executeCommand(ctx, active, run.Version, run.ID, run.Version.Definition.Directory, definition, attemptID)
	}
}

func (s *Service) executeCommand(ctx context.Context, active *activeCommand, version Version, runID, directory string, node Node, attemptID int64) {
	redactor := s.runRedactor(version)
	env := s.secretEnv(version, node.Environment)
	if shardDir, err := s.shardDirectory(ctx, version, runID, node.ID); err != nil {
		s.finishCommandError(active, runID, node.ID, redactor, "provisioning workspace shard: "+err.Error())
		return
	} else if shardDir != "" {
		directory = shardDir
	}
	result := s.executor.Execute(ctx, CommandRequest{Directory: directory, Command: node.Command, Environment: env, Permission: node.Permission, Outputs: node.Outputs, RestrictGit: restrictGitFor(version.Definition, node.ID)})
	// Redact known secret values from logs and collected outputs before
	// anything is persisted (audit) or published (artifacts).
	result.Stdout = redactor.redact(result.Stdout)
	result.Stderr = redactor.redact(result.Stderr)
	result.Error = redactor.redact(result.Error)
	result.Outputs = redactor.redactOutputs(result.Outputs)
	stopOwner := false
	if version.Definition.FailFast && result.State != AttemptSuccessful && result.State != AttemptCanceled {
		stopOwner = s.stopSiblingCommands(runID, node.ID)
		if !stopOwner {
			result.State = AttemptCanceled
			result.Error = "canceled after sibling failure"
		}
	}
	outputs, err := json.Marshal(result.Outputs)
	if err != nil {
		result.State, result.Error = AttemptErrored, err.Error()
		outputs = []byte("{}")
	}
	if result.State == AttemptSuccessful {
		s.publishCommandArtifacts(version, runID, node, attemptID, result.Outputs)
	}
	_ = s.store.CompleteWorkflowCommand(runID, node.ID, state.WorkflowCommandResult{
		State: result.State, ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr, Error: result.Error,
		OutputsJSON: string(outputs), StdoutTruncated: result.StdoutTruncated, StderrTruncated: result.StderrTruncated,
	}, s.now().UnixMilli())
	if completed, err := s.GetRun(context.Background(), runID); err == nil {
		_, _ = s.applyPolicies(context.Background(), completed)
	}
	s.mu.Lock()
	delete(s.running[runID], node.ID)
	if len(s.running[runID]) == 0 {
		delete(s.running, runID)
	}
	if stopOwner {
		delete(s.stopping, runID)
	}
	close(active.done)
	s.mu.Unlock()
	_ = s.dispatch(context.Background(), runID)
	s.changed(runID)
}

// shardDirectory resolves the on-disk worktree directory for a node's
// workspace lease, creating the shard through the WorkspaceProvider on
// first use and recording its path. Returns "" when the node holds no
// lease or no provider is configured (the command then runs in the
// workflow directory as before).
func (s *Service) shardDirectory(ctx context.Context, version Version, runID, nodeID string) (string, error) {
	if s.workspace == nil || version.Definition.Workspace == nil {
		return "", nil
	}
	leases, err := s.store.ListWorkflowWorkspaceLeases(runID)
	if err != nil {
		return "", err
	}
	for _, lease := range leases {
		if lease.NodeID != nodeID {
			continue
		}
		if lease.ShardPath != "" {
			return lease.ShardPath, nil
		}
		repo := version.Definition.Workspace.Repo
		if repo == "" {
			repo = version.Definition.Directory
		}
		path, err := s.workspace.EnsureShard(ctx, runID, repo, lease.Shard)
		if err != nil {
			return "", err
		}
		if err := s.store.SetWorkflowWorkspaceShardPath(runID, nodeID, path); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", nil
}

// finishCommandError settles a command attempt as errored (e.g. shard
// provisioning failed) and releases its held capacity, mirroring the
// bookkeeping executeCommand does on a normal completion.
func (s *Service) finishCommandError(active *activeCommand, runID, nodeID string, red *redactor, message string) {
	_ = s.store.CompleteWorkflowCommand(runID, nodeID, state.WorkflowCommandResult{
		State: AttemptErrored, ExitCode: -1, Error: red.redact(message), OutputsJSON: "{}",
	}, s.now().UnixMilli())
	s.mu.Lock()
	delete(s.running[runID], nodeID)
	if len(s.running[runID]) == 0 {
		delete(s.running, runID)
	}
	close(active.done)
	s.mu.Unlock()
	_ = s.dispatch(context.Background(), runID)
	s.changed(runID)
}

func (s *Service) stopSiblingCommands(runID, nodeID string) bool {
	s.mu.Lock()
	if s.stopping[runID] {
		s.mu.Unlock()
		return false
	}
	s.stopping[runID] = true
	var siblings []*activeCommand
	for id, command := range s.running[runID] {
		if id != nodeID {
			siblings = append(siblings, command)
			command.cancel()
		}
	}
	s.mu.Unlock()
	for _, command := range siblings {
		<-command.done
	}
	return true
}

func (s *Service) Tick(ctx context.Context) error {
	runs, err := s.store.ListWorkflowRuns()
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.State == StateActive {
			if err := s.recoverInterrupted(ctx, run.ID); err != nil {
				return err
			}
			if err := s.dispatch(ctx, run.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) recoverInterrupted(ctx context.Context, runID string) error {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	for _, node := range run.Nodes {
		if len(node.Attempts) == 0 {
			continue
		}
		attempt := node.Attempts[len(node.Attempts)-1]
		switch {
		case node.Type == "command" && attempt.State == AttemptRunning && !s.commandActive(runID, node.NodeID):
			return s.store.CompleteWorkflowCommand(runID, node.NodeID, state.WorkflowCommandResult{State: AttemptErrored, Error: "command interrupted by server restart", OutputsJSON: "{}"}, s.now().UnixMilli())
		case node.Type == "agent" && attempt.State == AttemptStarting && attempt.SessionID == "":
			return s.store.CompleteWorkflowAgentNode(runID, node.NodeID, attempt.ID, false, "error", "{}", "agent launch interrupted by server restart", s.now().UnixMilli())
		}
	}
	return nil
}

func (s *Service) commandActive(runID, nodeID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running[runID] != nil && s.running[runID][nodeID] != nil
}

func (s *Service) dispatch(ctx context.Context, runID string) error {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	return s.dispatchLocked(ctx, runID)
}

func (s *Service) dispatchLocked(ctx context.Context, runID string) error {
	run, err := s.GetRun(ctx, runID)
	if err != nil || run.State != StateActive {
		return err
	}
	// A compatibility workflow is a historical loop copy. Its legacy loop
	// remains the execution owner until #331 retires that engine. Cancel any
	// active run created before the trigger guard was deployed rather than
	// dispatching an incomplete compatibility agent configuration.
	if len(run.Version.Definition.LoopCompat) != 0 {
		if err := s.store.SetWorkflowRunState(runID, StateActive, StateCanceled, s.now().UnixMilli()); err != nil {
			return err
		}
		s.changed(runID)
		return nil
	}
	if moved, err := s.applyPolicies(ctx, run); err != nil {
		return err
	} else if moved {
		run, err = s.GetRun(ctx, runID)
		if err != nil || run.State != StateActive {
			return err
		}
	}
	if exceeded, reason := s.budgetExceeded(ctx, run); exceeded {
		if err := s.store.FailWorkflowRun(runID, reason, s.now().UnixMilli()); err != nil {
			return err
		}
		s.changed(runID)
		return nil
	}
	s.dispatchReady(run)
	for {
		mapMoved, err := s.driveMapNodes(ctx, runID)
		if err != nil {
			return err
		}
		if s.agent == nil {
			if mapMoved {
				continue
			}
			return nil
		}
		run, err := s.GetRun(ctx, runID)
		if err != nil || run.State != StateActive {
			return err
		}
		progressed := mapMoved
		for _, node := range run.Nodes {
			if node.Type != "agent" || (node.State != NodeReady && node.State != NodeRunning) || len(node.Attempts) == 0 {
				continue
			}
			config := agentConfig(run.Version.Definition.Nodes, node.NodeID)
			attempt := node.Attempts[len(node.Attempts)-1]
			if attempt.SessionID == "" {
				existing := affinitySession(run, node.NodeID, config)
				// Claim atomically holds run-concurrency + named-pool
				// capacity. A failed claim (lost race or full pool) skips
				// this node; other ready agents still get their turn and
				// capacity is retried on the next dispatch/tick.
				claimed, err := s.store.ClaimWorkflowAgentAttempt(runID, node.NodeID, attempt.ID, config.SessionAffinity, config.Directory, resourceRequests(run.Version.Definition, node.NodeID), workspaceRequest(run.Version.Definition, node.NodeID), s.now().UnixMilli())
				if err != nil {
					return err
				}
				if !claimed {
					continue
				}
				agentDir := config.Directory
				if shardDir, shardErr := s.shardDirectory(ctx, run.Version, runID, node.NodeID); shardErr != nil {
					if err := s.store.CompleteWorkflowAgentNode(runID, node.NodeID, attempt.ID, false, "error", "{}", "provisioning workspace shard: "+shardErr.Error(), s.now().UnixMilli()); err != nil {
						return err
					}
					s.changed(runID)
					progressed = true
					continue
				} else if shardDir != "" {
					agentDir = shardDir
				}
				prompt := s.itemPrompt(ctx, run, config.Prompt)
				session, startErr := s.agent.Start(ctx, AgentRequest{Platform: config.Platform, Directory: agentDir, Prompt: prompt, Model: config.Model, Agent: config.Agent, Reasoning: config.Reasoning, SessionID: existing})
				if startErr != nil {
					if err := s.store.CompleteWorkflowAgentNode(runID, node.NodeID, attempt.ID, false, "error", "{}", startErr.Error(), s.now().UnixMilli()); err != nil {
						return err
					}
					s.changed(runID)
					progressed = true
					continue
				}
				if err := s.store.AttachWorkflowAgentSession(runID, node.NodeID, attempt.ID, session.Platform, session.ID, session.State, s.now().UnixMilli()); err != nil {
					return err
				}
				if session.Error != "" {
					if err := s.store.CompleteWorkflowAgentNode(runID, node.NodeID, attempt.ID, false, session.State, "{}", session.Error, s.now().UnixMilli()); err != nil {
						return err
					}
				}
				s.changed(runID)
				progressed = true
				continue
			}
			result, inspectErr := s.agent.Inspect(ctx, AgentSession{ID: attempt.SessionID, Platform: attempt.Platform, State: attempt.SessionState, Directory: attempt.Directory}, config.Collectors)
			if inspectErr != nil {
				return inspectErr
			}
			if result.State == "busy" || result.State == "running" {
				if err := s.store.SetWorkflowAgentSessionState(runID, node.NodeID, attempt.ID, result.State, "", s.now().UnixMilli()); err != nil {
					return err
				}
				continue
			}
			successful := result.State != "error" && result.State != "failed" && result.State != "canceled"
			if successful {
				for _, collector := range config.Collectors {
					if _, ok := result.Outputs[collector.Name]; !ok {
						successful = false
						result.Error = fmt.Sprintf("collector %q produced no output", collector.Name)
						break
					}
				}
			}
			// Redact known secret values from agent outputs and error
			// before persistence and artifact publication.
			redactor := s.runRedactor(run.Version)
			result.Outputs = redactor.redactRawOutputs(result.Outputs)
			result.Error = redactor.redact(result.Error)
			if successful {
				s.publishAgentArtifacts(run.Version, runID, node.NodeID, attempt.ID, config, result.Outputs)
			}
			outputs, err := json.Marshal(result.Outputs)
			if err != nil {
				return err
			}
			if err := s.store.CompleteWorkflowAgentNode(runID, node.NodeID, attempt.ID, successful, result.State, string(outputs), result.Error, s.now().UnixMilli()); err != nil {
				return err
			}
			if completed, err := s.GetRun(ctx, runID); err == nil {
				if _, err := s.applyPolicies(ctx, completed); err != nil {
					return err
				}
			}
			s.changed(runID)
			progressed = true
		}
		if !progressed {
			return nil
		}
	}
}

// applyPolicies evaluates conditions before a ready node is dispatched and
// repeat predicates after a node body has completed. Evaluation errors are a
// deterministic skipped branch, never an implicit allow.
func (s *Service) applyPolicies(ctx context.Context, run RunDetail) (bool, error) {
	if run.Version.Definition.FailFast {
		for _, node := range run.Nodes {
			if node.State != NodeFailed {
				continue
			}
			s.stopSiblingCommands(run.ID, node.NodeID)
			s.cancelChildRuns(ctx, run.ID)
			if err := s.store.FailWorkflowRun(run.ID, "workflow stopped by fail-fast after "+node.NodeID+" failed", s.now().UnixMilli()); err != nil {
				return false, err
			}
			s.changed(run.ID)
			return true, nil
		}
	}
	outcomes := make(map[string]any, len(run.Nodes))
	for _, node := range run.Nodes {
		outcomes[node.NodeID] = map[string]any{"state": node.State}
	}
	artifacts, err := s.celArtifacts(ctx, run.ID)
	if err != nil {
		return false, err
	}
	moved := false
	for _, node := range run.Nodes {
		if node.State == NodeReady {
			for _, edge := range run.Version.Definition.Dependencies {
				if edge.To != node.NodeID || edge.Condition == "" {
					continue
				}
				ok, evalErr := evaluateCEL(edge.Condition, outcomes, artifacts)
				if evalErr != nil || !ok {
					reason := "condition evaluated false"
					if evalErr != nil {
						reason = "condition error: " + evalErr.Error()
					}
					if err := s.store.SkipWorkflowNode(run.ID, node.NodeID, reason, s.now().UnixMilli()); err != nil {
						return moved, err
					}
					moved = true
					break
				}
			}
		}
		if node.State != NodeSuccessful {
			continue
		}
		config := repeatConfig(run.Version.Definition.Nodes, node.NodeID)
		if config == nil {
			continue
		}
		ok, evalErr := evaluateCEL(config.Until, outcomes, artifacts)
		if evalErr != nil {
			if err := s.store.ExhaustWorkflowRepeat(run.ID, node.NodeID, "repeat condition error: "+evalErr.Error(), s.now().UnixMilli()); err != nil {
				return moved, err
			}
			moved = true
			continue
		}
		if ok {
			continue
		}
		if len(node.Attempts) >= config.MaxAttempts {
			if err := s.store.ExhaustWorkflowRepeat(run.ID, node.NodeID, fmt.Sprintf("repeat exhausted after %d attempts", config.MaxAttempts), s.now().UnixMilli()); err != nil {
				return moved, err
			}
			moved = true
			continue
		}
		if err := s.store.RepeatWorkflowNode(run.ID, node.NodeID, node.Attempts[len(node.Attempts)-1].ID, "repeat condition evaluated false", s.now().UnixMilli()); err != nil {
			return moved, err
		}
		moved = true
	}
	if moved {
		s.changed(run.ID)
	}
	return moved, nil
}

func repeatConfig(nodes []Node, id string) *RepeatConfig {
	for _, node := range nodes {
		if node.ID == id {
			return node.Repeat
		}
	}
	return nil
}

func (s *Service) celArtifacts(ctx context.Context, runID string) (map[string]any, error) {
	rows, err := s.ListArtifacts(ctx, runID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	latest := map[string]int64{}
	for _, row := range rows {
		if row.Kind != KindJSON || !row.PayloadAvailable {
			continue
		}
		key := row.NodeID + "." + row.Name
		if row.AttemptID < latest[key] {
			continue
		}
		_, payload, err := s.DownloadArtifact(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		var value any
		if json.Unmarshal(payload, &value) == nil {
			latest[key] = row.AttemptID
			out[key] = value
		}
	}
	return out, nil
}

// budgetExceeded reports whether a run has crossed a configured optional
// limit. Omitted limits mean unlimited. Cost/token usage aggregates every
// descendant agent session; duration is wall-clock since the run started.
func (s *Service) budgetExceeded(ctx context.Context, run RunDetail) (bool, string) {
	limits := run.Version.Definition.Limits
	if limits == nil {
		return false, ""
	}
	if limits.MaxDurationSecs > 0 {
		elapsed := s.now().UnixMilli() - run.CreatedAt
		if elapsed >= limits.MaxDurationSecs*1000 {
			return true, fmt.Sprintf("workflow exceeded duration limit of %ds", limits.MaxDurationSecs)
		}
	}
	if (limits.MaxCostUSD <= 0 && limits.MaxTokens <= 0) || s.usage == nil {
		return false, ""
	}
	var sessions []string
	for _, node := range run.Nodes {
		for _, attempt := range node.Attempts {
			if attempt.SessionID != "" {
				sessions = append(sessions, attempt.SessionID)
			}
		}
	}
	if len(sessions) == 0 {
		return false, ""
	}
	tokens, cost, ok := s.usage.SessionUsage(ctx, sessions)
	if !ok {
		return false, ""
	}
	if limits.MaxCostUSD > 0 && cost >= limits.MaxCostUSD {
		return true, fmt.Sprintf("workflow exceeded cost limit of $%.2f", limits.MaxCostUSD)
	}
	if limits.MaxTokens > 0 && tokens >= limits.MaxTokens {
		return true, fmt.Sprintf("workflow exceeded token limit of %d", limits.MaxTokens)
	}
	return false, ""
}

// resourceRequests builds the atomic acquisition set for a node: the
// implicit run-concurrency pool ("") plus every declared named pool. The
// run pool always demands one unit so competing ready nodes cannot
// oversubscribe the required concurrency cap.
func resourceRequests(definition Definition, nodeID string) []state.WorkflowResourceRequest {
	capacities := make(map[string]int, len(definition.Pools))
	for _, pool := range definition.Pools {
		capacities[pool.Name] = pool.Capacity
	}
	requests := []state.WorkflowResourceRequest{{Pool: "", Units: 1, Capacity: definition.Concurrency}}
	for _, node := range definition.Nodes {
		if node.ID != nodeID {
			continue
		}
		for _, request := range node.Resources {
			requests = append(requests, state.WorkflowResourceRequest{Pool: request.Pool, Units: request.Units, Capacity: capacities[request.Pool]})
		}
		break
	}
	return requests
}

// workspaceRequest builds the shard-lease demand for a node, or nil when
// the workflow declares no shard pool or the node declares no lease. A node
// with a lease but no explicit mode defaults to exclusive. Path scopes are
// assumed already normalized by validation.
func workspaceRequest(definition Definition, nodeID string) *state.WorkflowWorkspaceRequest {
	if definition.Workspace == nil || definition.Workspace.Shards <= 0 {
		return nil
	}
	lease := nodeLease(definition.Nodes, nodeID)
	if lease == nil {
		return nil
	}
	mode := lease.Mode
	if mode == "" {
		mode = LeaseExclusive
	}
	return &state.WorkflowWorkspaceRequest{
		Shards:      definition.Workspace.Shards,
		Mode:        mode,
		Paths:       lease.Paths,
		CommitLease: lease.Commit,
		Host:        definition.Workspace.Host,
	}
}

// restrictGitFor returns the git subcommands a node may not run because it
// holds a path-scoped lease. Exclusive and commit-coordinator leases own
// repository-wide mutation, so they are unrestricted. Returns nil when no
// restriction applies.
func restrictGitFor(definition Definition, nodeID string) []string {
	if definition.Workspace == nil || definition.Workspace.Shards <= 0 {
		return nil
	}
	lease := nodeLease(definition.Nodes, nodeID)
	if lease == nil || lease.Commit {
		return nil
	}
	if lease.Mode != LeasePath {
		return nil
	}
	if len(definition.Workspace.RestrictedGit) > 0 {
		return definition.Workspace.RestrictedGit
	}
	return defaultRestrictedGitSubcommands
}

func nodeLease(nodes []Node, id string) *LeaseConfig {
	for _, node := range nodes {
		if node.ID == id {
			return node.Lease
		}
	}
	return nil
}

func agentConfig(nodes []Node, id string) *AgentConfig {
	for _, node := range nodes {
		if node.ID == id {
			return node.Agent
		}
	}
	return nil
}

func affinitySession(run RunDetail, currentNode string, config *AgentConfig) string {
	if config == nil || config.SessionAffinity == "" {
		return ""
	}
	for _, node := range run.Nodes {
		if node.NodeID == currentNode {
			continue
		}
		for _, attempt := range node.Attempts {
			if attempt.SessionID != "" && (config.Platform == "" || attempt.Platform == config.Platform) {
				if attempt.Affinity == config.SessionAffinity && attempt.Directory == config.Directory {
					return attempt.SessionID
				}
			}
		}
	}
	return ""
}

func (s *Service) changed(runID string) {
	if s.notify != nil {
		s.notify(runID)
	}
}

func (s *Service) triggerChanged() {
	if s.notifyTrigger != nil {
		s.notifyTrigger()
	}
}

func validateDefinition(definition Definition) error {
	if definition.ID == "" || definition.Name == "" || definition.Version == "" {
		return fmt.Errorf("id, name, and version are required")
	}
	if definition.Concurrency <= 0 {
		return fmt.Errorf("concurrency must be positive")
	}
	if len(definition.Nodes) == 0 {
		return fmt.Errorf("at least one node is required")
	}
	if len(definition.Triggers) == 0 {
		return fmt.Errorf("at least one trigger is required")
	}
	if err := validateSecrets(definition.Secrets); err != nil {
		return err
	}
	pools := map[string]int{}
	for _, pool := range definition.Pools {
		if pool.Name == "" {
			return fmt.Errorf("resource pool name is required")
		}
		if pool.Capacity <= 0 {
			return fmt.Errorf("resource pool %q capacity must be positive", pool.Name)
		}
		if _, ok := pools[pool.Name]; ok {
			return fmt.Errorf("duplicate resource pool %q", pool.Name)
		}
		pools[pool.Name] = pool.Capacity
	}
	if definition.Limits != nil {
		if definition.Limits.MaxCostUSD < 0 || definition.Limits.MaxTokens < 0 || definition.Limits.MaxDurationSecs < 0 {
			return fmt.Errorf("limits must not be negative")
		}
	}
	if definition.Workspace != nil && definition.Workspace.Shards <= 0 {
		return fmt.Errorf("workspace shard pool capacity must be positive")
	}
	triggerIDs := map[string]bool{}
	manualTriggers := 0
	for _, trigger := range definition.Triggers {
		if err := validateTrigger(trigger); err != nil {
			return err
		}
		if triggerIDs[trigger.ID] {
			return fmt.Errorf("duplicate trigger %q", trigger.ID)
		}
		triggerIDs[trigger.ID] = true
		if trigger.Type == TriggerManual {
			manualTriggers++
		}
	}
	if manualTriggers > 1 {
		return fmt.Errorf("only one manual trigger is supported")
	}
	nodes := make(map[string]bool, len(definition.Nodes))
	for _, node := range definition.Nodes {
		if node.ID == "" || node.Name == "" || node.Type == "" {
			return fmt.Errorf("node id, name, and type are required")
		}
		switch node.Type {
		case "approval", "command", "agent", "map", "join":
		default:
			return fmt.Errorf("unsupported node type %q", node.Type)
		}
		if node.Type == "command" {
			if err := validateCommandNode(definition.Directory, node); err != nil {
				return fmt.Errorf("node %q: %w", node.ID, err)
			}
		}
		if node.Type == "agent" {
			if node.Agent == nil || node.Agent.Directory == "" || node.Agent.Prompt == "" {
				return fmt.Errorf("agent node %q requires directory and prompt", node.ID)
			}
			collectorNames := map[string]bool{}
			for _, collector := range node.Agent.Collectors {
				if collector.Name == "" || collectorNames[collector.Name] {
					return fmt.Errorf("agent node %q has invalid collector name", node.ID)
				}
				collectorNames[collector.Name] = true
				switch collector.Type {
				case "final-message", "diff":
					if collector.Path != "" {
						return fmt.Errorf("collector %q does not accept a path", collector.Name)
					}
				case "file", "json-file":
					clean := filepath.Clean(collector.Path)
					if collector.Path == "" || filepath.IsAbs(collector.Path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
						return fmt.Errorf("collector %q requires a safe relative path", collector.Name)
					}
				default:
					return fmt.Errorf("unsupported collector type %q", collector.Type)
				}
			}
		}
		if node.Repeat != nil {
			if node.Repeat.MaxAttempts <= 0 {
				return fmt.Errorf("node %q repeat maxAttempts must be positive", node.ID)
			}
			if err := validateCEL(node.Repeat.Until); err != nil {
				return fmt.Errorf("node %q repeat: %w", node.ID, err)
			}
		}
		requested := map[string]bool{}
		for _, request := range node.Resources {
			capacity, ok := pools[request.Pool]
			if !ok {
				return fmt.Errorf("node %q requests undeclared resource pool %q", node.ID, request.Pool)
			}
			if request.Units <= 0 {
				return fmt.Errorf("node %q resource units for pool %q must be positive", node.ID, request.Pool)
			}
			if request.Units > capacity {
				return fmt.Errorf("node %q requests %d units of pool %q exceeding capacity %d", node.ID, request.Units, request.Pool, capacity)
			}
			if requested[request.Pool] {
				return fmt.Errorf("node %q requests pool %q more than once", node.ID, request.Pool)
			}
			requested[request.Pool] = true
		}
		if err := validateLease(definition.Workspace, node); err != nil {
			return fmt.Errorf("node %q: %w", node.ID, err)
		}
		if nodes[node.ID] {
			return fmt.Errorf("duplicate node %q", node.ID)
		}
		nodes[node.ID] = true
	}
	indegree := make(map[string]int, len(nodes))
	edges := make(map[string][]string, len(nodes))
	seenDependencies := make(map[Dependency]bool, len(definition.Dependencies))
	for _, dep := range definition.Dependencies {
		if dep.From == "" || dep.To == "" {
			return fmt.Errorf("dependency endpoints are required")
		}
		if dep.From == dep.To {
			return fmt.Errorf("self dependency for node %q", dep.From)
		}
		if !nodes[dep.From] {
			return fmt.Errorf("dependency references missing node %q", dep.From)
		}
		if !nodes[dep.To] {
			return fmt.Errorf("dependency references missing node %q", dep.To)
		}
		if seenDependencies[dep] {
			return fmt.Errorf("duplicate dependency %q -> %q", dep.From, dep.To)
		}
		if dep.Condition != "" {
			if err := validateCEL(dep.Condition); err != nil {
				return fmt.Errorf("dependency %q -> %q: %w", dep.From, dep.To, err)
			}
		}
		seenDependencies[dep] = true
		edges[dep.From] = append(edges[dep.From], dep.To)
		indegree[dep.To]++
	}
	queue := make([]string, 0, len(nodes))
	for id := range nodes {
		if indegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range edges[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(nodes) {
		return fmt.Errorf("workflow contains a cycle")
	}
	return nil
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateCommandNode(directory string, node Node) error {
	if directory == "" || !filepath.IsAbs(directory) {
		return fmt.Errorf("workflow directory must be absolute for command nodes")
	}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return fmt.Errorf("workflow directory must exist")
	}
	if len(node.Command) == 0 || node.Command[0] == "" {
		return fmt.Errorf("command is required")
	}
	for _, arg := range node.Command {
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("command contains NUL")
		}
	}
	for key, value := range node.Environment {
		if !environmentName.MatchString(key) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid environment variable %q", key)
		}
	}
	for _, rule := range node.Permission {
		if rule.Permission != "bash" || rule.Pattern == "" {
			return fmt.Errorf("command permission requires bash and a pattern")
		}
		switch rule.Action {
		case "allow", "deny", "ask":
		default:
			return fmt.Errorf("invalid permission action %q", rule.Action)
		}
	}
	seen := make(map[string]bool, len(node.Outputs))
	if len(node.Outputs) > 32 {
		return fmt.Errorf("at most 32 collectors are allowed")
	}
	for _, output := range node.Outputs {
		if output.Name == "" || seen[output.Name] {
			return fmt.Errorf("collector names must be present and unique")
		}
		seen[output.Name] = true
		switch output.Type {
		case "text", "git_diff":
			if output.Path != "" {
				return fmt.Errorf("collector %q does not accept a path", output.Name)
			}
		case "file", "json_file":
			clean := filepath.Clean(output.Path)
			if output.Path == "" || filepath.IsAbs(output.Path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("collector %q path must stay inside workflow directory", output.Name)
			}
		default:
			return fmt.Errorf("unsupported collector type %q", output.Type)
		}
	}
	return nil
}

func validateTrigger(trigger Trigger) error {
	if trigger.ID == "" {
		return fmt.Errorf("trigger id is required")
	}
	if trigger.Overlap != "" && trigger.Overlap != OverlapSkip && trigger.Overlap != OverlapQueue && trigger.Overlap != OverlapParallel {
		return fmt.Errorf("trigger %q has invalid overlap %q", trigger.ID, trigger.Overlap)
	}
	switch trigger.Type {
	case TriggerManual:
	case TriggerInterval:
		if trigger.IntervalSeconds <= 0 {
			return fmt.Errorf("trigger %q intervalSeconds must be positive", trigger.ID)
		}
	case TriggerCron:
		if err := loops.ValidateCron(trigger.Cron); err != nil {
			return fmt.Errorf("trigger %q has invalid cron: %w", trigger.ID, err)
		}
	case TriggerPR:
		if trigger.PRNumber <= 0 || trigger.Directory == "" {
			return fmt.Errorf("trigger %q requires prNumber and directory", trigger.ID)
		}
	case TriggerChildCompletion, TriggerTurnCompletion:
		if trigger.SessionID == "" {
			return fmt.Errorf("trigger %q requires sessionId", trigger.ID)
		}
	default:
		return fmt.Errorf("trigger %q has unsupported type %q", trigger.ID, trigger.Type)
	}
	return nil
}

// normalizeLeases canonicalizes every path-lease scope in place so the
// stored definition and all overlap comparisons use one normalized form.
// It also de-duplicates and rejects scopes that overlap within one node.
func normalizeLeases(definition *Definition) error {
	for i := range definition.Nodes {
		lease := definition.Nodes[i].Lease
		if lease == nil {
			continue
		}
		if lease.Mode == LeasePath || len(lease.Paths) > 0 {
			normalized, err := normalizedLeasePaths(lease.Paths)
			if err != nil {
				return fmt.Errorf("node %q: %w", definition.Nodes[i].ID, err)
			}
			lease.Paths = normalized
		}
	}
	return nil
}

// validateLease checks a node's workspace lease against the workflow's
// shard pool declaration.
func validateLease(workspace *WorkspaceConfig, node Node) error {
	if node.Lease == nil {
		return nil
	}
	if workspace == nil || workspace.Shards <= 0 {
		return fmt.Errorf("declares a workspace lease but no workspace shard pool is configured")
	}
	mode := node.Lease.Mode
	switch mode {
	case "", LeaseExclusive:
		if len(node.Lease.Paths) > 0 {
			return fmt.Errorf("exclusive lease does not accept path scopes")
		}
	case LeasePath:
		if node.Lease.Commit {
			return fmt.Errorf("commit coordinator must use an exclusive lease")
		}
		if len(node.Lease.Paths) == 0 {
			return fmt.Errorf("path lease requires at least one declared path scope")
		}
	default:
		return fmt.Errorf("unsupported lease mode %q", mode)
	}
	return nil
}

func validateSecrets(secrets []SecretRef) error {
	names := make(map[string]bool, len(secrets))
	for _, secret := range secrets {
		if secret.Name == "" || secret.Env == "" {
			return fmt.Errorf("secret name and env are required")
		}
		if !environmentName.MatchString(secret.Env) {
			return fmt.Errorf("secret %q references invalid env var %q", secret.Name, secret.Env)
		}
		if names[secret.Name] {
			return fmt.Errorf("duplicate secret %q", secret.Name)
		}
		names[secret.Name] = true
	}
	return nil
}

func normalizedTrigger(trigger Trigger) Trigger {
	if trigger.Overlap == "" {
		trigger.Overlap = OverlapSkip
	}
	return trigger
}

func versionFromRow(row state.WorkflowVersion) (Version, error) {
	var definition Definition
	if err := json.Unmarshal([]byte(row.DefinitionJSON), &definition); err != nil {
		return Version{}, fmt.Errorf("decoding stored workflow definition: %w", err)
	}
	return versionFromState(row, definition), nil
}

func versionFromState(row state.WorkflowVersion, definition Definition) Version {
	return Version{ID: row.ID, WorkflowID: row.WorkflowID, Name: row.Name, Revision: row.Revision, CreatedAt: row.CreatedAt, Active: row.Active, Definition: definition}
}

func runFromState(row state.WorkflowRun) Run {
	run := Run{ID: row.ID, WorkflowID: row.WorkflowID, VersionID: row.VersionID, State: row.State, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, CompletedAt: row.CompletedAt, ParentRunID: row.ParentRunID, ParentNodeID: row.ParentNodeID, ItemKey: row.ItemKey, ItemIndex: row.ItemIndex}
	if row.TriggerSnapshotJSON != "" {
		var snapshot TriggerSnapshot
		if json.Unmarshal([]byte(row.TriggerSnapshotJSON), &snapshot) == nil {
			run.Trigger = &snapshot
		}
	}
	return run
}

func newID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(raw[:])
}
