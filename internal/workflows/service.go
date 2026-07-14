package workflows

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Version       string       `json:"version"`
	Concurrency   int          `json:"concurrency"`
	RetentionDays int          `json:"retentionDays,omitempty"`
	Directory     string       `json:"directory,omitempty"`
	Secrets       []SecretRef  `json:"secrets,omitempty"`
	Triggers      []Trigger    `json:"triggers"`
	Nodes         []Node       `json:"nodes"`
	Dependencies  []Dependency `json:"dependencies"`
}

type Trigger struct {
	ID              string `json:"id"`
	Type            string `json:"type"`
	Overlap         string `json:"overlap,omitempty"`
	IntervalSeconds int    `json:"intervalSeconds,omitempty"`
	Cron            string `json:"cron,omitempty"`
	PRNumber        int    `json:"prNumber,omitempty"`
	PollSeconds     int    `json:"pollSeconds,omitempty"`
	Directory       string `json:"directory,omitempty"`
	Platform        string `json:"platform,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
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
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Command     []string          `json:"command,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Permission  []PermissionRule  `json:"permission,omitempty"`
	Outputs     []Collector       `json:"outputs,omitempty"`
	Agent       *AgentConfig      `json:"agent,omitempty"`
}

type PermissionRule struct {
	Permission string `json:"permission"`
	Pattern    string `json:"pattern"`
	Action     string `json:"action"`
}

type Collector struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
}

type AgentConfig struct {
	Platform        string      `json:"platform,omitempty"`
	Directory       string      `json:"directory"`
	Prompt          string      `json:"prompt"`
	Model           string      `json:"model,omitempty"`
	Agent           string      `json:"agent,omitempty"`
	Reasoning       string      `json:"reasoning,omitempty"`
	SessionAffinity string      `json:"sessionAffinity,omitempty"`
	Collectors      []Collector `json:"collectors,omitempty"`
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
	From string `json:"from"`
	To   string `json:"to"`
}

type Version struct {
	ID            string          `json:"id"`
	WorkflowID    string          `json:"workflowId"`
	Name          string          `json:"name"`
	Revision      int             `json:"revision"`
	CreatedAt     int64           `json:"createdAt"`
	Definition    Definition      `json:"definition"`
	TriggerStates []TriggerStatus `json:"triggerStates"`
}

type Run struct {
	ID          string           `json:"id"`
	WorkflowID  string           `json:"workflowId"`
	VersionID   string           `json:"versionId"`
	State       string           `json:"state"`
	CreatedAt   int64            `json:"createdAt"`
	UpdatedAt   int64            `json:"updatedAt"`
	CompletedAt int64            `json:"completedAt,omitempty"`
	Trigger     *TriggerSnapshot `json:"trigger,omitempty"`
}

type RunDetail struct {
	Run
	Version Version   `json:"version"`
	Nodes   []NodeRun `json:"nodes"`
}

type NodeRun struct {
	NodeID      string    `json:"nodeId"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	State       string    `json:"state"`
	ReadyAt     int64     `json:"readyAt,omitempty"`
	CompletedAt int64     `json:"completedAt,omitempty"`
	Attempts    []Attempt `json:"attempts"`
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
	StartWorkflowCommand(string, string, int64) (bool, error)
	CompleteWorkflowCommand(string, string, state.WorkflowCommandResult, int64) error
	SetWorkflowRunState(string, string, string, int64) error
	ClaimWorkflowAgentAttempt(string, string, int64, string, string, int64) (bool, error)
	AttachWorkflowAgentSession(string, string, int64, string, string, string, int64) error
	SetWorkflowAgentSessionState(string, string, int64, string, string, int64) error
	CompleteWorkflowAgentNode(string, string, int64, bool, string, string, string, int64) error
	ResolveWorkflowAttempt(string, int64, string, int64) error
	InsertWorkflowArtifact(state.WorkflowArtifact) error
	ListWorkflowArtifacts(string) ([]state.WorkflowArtifact, error)
	GetWorkflowArtifact(string) (*state.WorkflowArtifact, error)
	ExpiredWorkflowArtifactHashes(int64) ([]string, error)
	MarkWorkflowArtifactPayloadDeleted(string) error
}

type Deps struct {
	Store           Store
	Now             func() time.Time
	Notify          func(runID string)
	NotifyTrigger   func()
	Forge           loops.ForgePoller
	Status          loops.SessionStatusInferer
	CommandExecutor CommandExecutor
	Agent           AgentExecutor
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
		blobs:         deps.Blobs,
		resolveSecret: resolveSecret,
		running:       make(map[string]map[string]*activeCommand),
		stopping:      make(map[string]bool),
	}
}

func (s *Service) ValidateJSON(_ context.Context, source []byte) (Definition, error) {
	definition, _, err := decodeDefinition(source)
	if err != nil {
		return Definition{}, err
	}
	if err := validateDefinition(definition); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

func (s *Service) PublishJSON(_ context.Context, source []byte) (Version, error) {
	definition, canonical, err := decodeDefinition(source)
	if err != nil {
		return Version{}, err
	}
	if err := validateDefinition(definition); err != nil {
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
	for position, node := range definition.Nodes {
		row.Nodes = append(row.Nodes, state.WorkflowNode{ID: node.ID, Name: node.Name, Type: node.Type, Position: position})
	}
	for _, dep := range definition.Dependencies {
		row.Dependencies = append(row.Dependencies, state.WorkflowDependency{From: dep.From, To: dep.To})
	}
	row, err = s.store.InsertWorkflowVersion(row)
	if err != nil {
		return Version{}, err
	}
	return versionFromState(row, definition), nil
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
		node := NodeRun{NodeID: row.NodeID, Name: row.Name, Type: row.Type, State: row.State, ReadyAt: row.ReadyAt, CompletedAt: row.CompletedAt, Attempts: make([]Attempt, 0, len(row.Attempts))}
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
	return detail, nil
}

func (s *Service) Approve(ctx context.Context, runID, nodeID string) (RunDetail, error) {
	if err := s.store.ApproveWorkflowNode(runID, nodeID, s.now().UnixMilli()); err != nil {
		return RunDetail{}, err
	}
	s.changed(runID)
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
		if nodeRun.Type != "command" || nodeRun.State != NodeReady || len(nodeRun.Attempts) == 0 || nodeRun.Attempts[0].State != AttemptWaiting {
			continue
		}
		s.mu.Lock()
		if s.stopping[run.ID] || len(s.running[run.ID]) >= run.Version.Definition.Concurrency {
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
		started, err := s.store.StartWorkflowCommand(run.ID, nodeRun.NodeID, s.now().UnixMilli())
		if err != nil || !started {
			s.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		active := &activeCommand{cancel: cancel, done: make(chan struct{})}
		s.running[run.ID][nodeRun.NodeID] = active
		s.mu.Unlock()
		definition := definitions[nodeRun.NodeID]
		attemptID := nodeRun.Attempts[0].ID
		go s.executeCommand(ctx, active, run.Version, run.ID, run.Version.Definition.Directory, definition, attemptID)
	}
}

func (s *Service) executeCommand(ctx context.Context, active *activeCommand, version Version, runID, directory string, node Node, attemptID int64) {
	redactor := s.runRedactor(version)
	env := s.secretEnv(version, node.Environment)
	result := s.executor.Execute(ctx, CommandRequest{Directory: directory, Command: node.Command, Environment: env, Permission: node.Permission, Outputs: node.Outputs})
	// Redact known secret values from logs and collected outputs before
	// anything is persisted (audit) or published (artifacts).
	result.Stdout = redactor.redact(result.Stdout)
	result.Stderr = redactor.redact(result.Stderr)
	result.Error = redactor.redact(result.Error)
	result.Outputs = redactor.redactOutputs(result.Outputs)
	stopOwner := false
	if result.State != AttemptSuccessful && result.State != AttemptCanceled {
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
	s.dispatchReady(run)
	if s.agent == nil {
		return nil
	}
	for {
		run, err := s.GetRun(ctx, runID)
		if err != nil || run.State != StateActive {
			return err
		}
		progressed := false
		for _, node := range run.Nodes {
			if node.Type != "agent" || (node.State != NodeReady && node.State != NodeRunning) || len(node.Attempts) == 0 {
				continue
			}
			config := agentConfig(run.Version.Definition.Nodes, node.NodeID)
			attempt := node.Attempts[len(node.Attempts)-1]
			if attempt.SessionID == "" {
				existing := affinitySession(run, node.NodeID, config)
				claimed, err := s.store.ClaimWorkflowAgentAttempt(runID, node.NodeID, attempt.ID, config.SessionAffinity, config.Directory, s.now().UnixMilli())
				if err != nil {
					return err
				}
				if !claimed {
					return nil
				}
				session, startErr := s.agent.Start(ctx, AgentRequest{Platform: config.Platform, Directory: config.Directory, Prompt: config.Prompt, Model: config.Model, Agent: config.Agent, Reasoning: config.Reasoning, SessionID: existing})
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
			s.changed(runID)
			progressed = true
		}
		if !progressed {
			return nil
		}
	}
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

func decodeDefinition(source []byte) (Definition, []byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	var definition Definition
	if err := decoder.Decode(&definition); err != nil {
		return Definition{}, nil, fmt.Errorf("invalid workflow JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Definition{}, nil, fmt.Errorf("invalid workflow JSON: trailing content")
	}
	canonical, err := json.Marshal(definition)
	if err != nil {
		return Definition{}, nil, fmt.Errorf("encoding workflow definition: %w", err)
	}
	return definition, canonical, nil
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
		if node.Type != "approval" && node.Type != "command" && node.Type != "agent" {
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
	return Version{ID: row.ID, WorkflowID: row.WorkflowID, Name: row.Name, Revision: row.Revision, CreatedAt: row.CreatedAt, Definition: definition}
}

func runFromState(row state.WorkflowRun) Run {
	run := Run{ID: row.ID, WorkflowID: row.WorkflowID, VersionID: row.VersionID, State: row.State, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, CompletedAt: row.CompletedAt}
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
