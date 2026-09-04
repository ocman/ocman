package model

import (
	"errors"
	"regexp"
	"time"
)

var nativeFormulaKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func ValidNativeFormulaKey(key string) bool { return nativeFormulaKey.MatchString(key) }

var (
	ErrNativeEpicNotFound          = errors.New("factory epic not found")
	ErrNativeInstantiationConflict = errors.New("factory instantiation conflict")
	ErrInvalidGraphMutation        = errors.New("invalid factory graph mutation")
)

type NativeEpic struct {
	ID              string
	Status          string
	Goal            string
	Brief           string
	InitialProject  string
	InstantiationID string
	FormulaID       string
	FormulaVersion  int
	FormulaHash     string
}

type NativeIssue struct {
	ID             string
	EpicID         string
	ParentID       string
	Requirement    string
	FormulaID      string
	FormulaVersion int
	FormulaHash    string
	Bindings       map[string]string
	Kind           string
	Title          string
	Status         string
	Outcome        string
	OutcomeReason  string
	GateResolution string
	DispatchState  string
	Blockers       []NativeIssueBlocker
	RetryAt        int64
	RetryAttempts  int
	Description    string
	PlanRevision   int
	ManifestKey    string
	CreatedAt      int64
	RemovedAt      int64
}

type NativeIssueBlocker struct {
	ID      string `json:"id"`
	EpicID  string `json:"epicId"`
	Reason  string `json:"reason"`
	Outcome string `json:"outcome"`
}

type GraphMutation struct {
	Action         string `json:"action"`
	EpicID         string `json:"epicId"`
	IssueID        string `json:"issueId"`
	ParentID       string `json:"parentId"`
	DependsOnID    string `json:"dependsOnId"`
	DependencyType string `json:"dependencyType"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Requirement    string `json:"requirement"`
	Actor          string `json:"actor"`
}

type NativeProposalRevision struct {
	EpicID            string
	MolID             string
	Project           string
	Revision          int
	ManifestJSON      string
	RationaleMarkdown string
	ContentHash       string
	CreatedAt         int64
}

type NativePlanGate struct {
	EpicID           string
	IssueID          string
	ProposalRevision int
	ProposalHash     string
	Outcome          string
	Resolution       string
	Feedback         string
	ReviewIssueIDs   []string
}

type NativeMaterialization struct {
	ID               string
	EpicID           string
	IssueID          string
	ProposalRevision int
	ProposalHash     string
	ManifestKey      string
	ImplementationID string
}

type FactoryCapacityPolicy struct {
	GlobalCapacity   int            `json:"globalCapacity"`
	ProjectCapacity  int            `json:"projectCapacity"`
	ProjectOverrides map[string]int `json:"projectOverrides"`
}

type NativeFormula struct {
	ID          string
	Version     int
	Source      string
	Hash        string
	Inputs      []string
	Nodes       []NativeFormulaNode
	Edges       []NativeFormulaEdge
	Composition []NativeFormulaComposition
}

type NativeFormulaNode struct{ Key, Kind string }
type NativeFormulaEdge struct{ From, To, Type string }
type NativeFormulaComposition struct {
	Key         string
	Requirement string
	Bindings    map[string]string
	Formula     NativeFormula
}

// NativeFormulaRevision is the durable, TOML-only custom Formula revision.
type NativeFormulaRevision struct {
	FormulaID    string
	Name         string
	Revision     int
	SourceTOML   string
	CompiledJSON string
	ContentHash  string
	CreatedAt    int64
}

type PlanningSession struct {
	Platform string `json:"platform"`
	ID       string `json:"id"`
}

type FactoryAttemptPhase string

const (
	FactoryAttemptPrepared FactoryAttemptPhase = "prepared"
	FactoryAttemptActive   FactoryAttemptPhase = "active"
	FactoryAttemptStopping FactoryAttemptPhase = "stopping"
	FactoryAttemptTerminal FactoryAttemptPhase = "terminal"
)

type FactoryAttemptOutcome string

const (
	FactoryAttemptSucceeded           FactoryAttemptOutcome = "succeeded"
	FactoryAttemptSkipped             FactoryAttemptOutcome = "skipped"
	FactoryAttemptCancelled           FactoryAttemptOutcome = "cancelled"
	FactoryAttemptAcknowledgedPartial FactoryAttemptOutcome = "acknowledged_partial"
	FactoryAttemptFailed              FactoryAttemptOutcome = "failed"
	FactoryAttemptInterrupted         FactoryAttemptOutcome = "interrupted"
	FactoryAttemptAmbiguous           FactoryAttemptOutcome = "ambiguous"
)

type FactoryAttemptPolicy struct {
	PlanRevision       int    `json:"planRevision"`
	PlanHash           string `json:"planHash"`
	TargetID           string `json:"targetId"`
	Repository         string `json:"repository"`
	Profile            string `json:"profile"`
	DeliveryRemoteType string `json:"deliveryRemoteType,omitempty"`
	DeliveryRemoteHost string `json:"deliveryRemoteHost,omitempty"`
	DeliveryRemoteRepo string `json:"deliveryRemoteRepo,omitempty"`
}

type FactoryAttemptResult struct {
	SchemaVersion int    `json:"schemaVersion"`
	Summary       string `json:"summary"`
	PRURL         string `json:"prUrl,omitempty"`
}

type FactoryAttemptFailure struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type FactoryAttempt struct {
	ID               string                `json:"id"`
	EpicID           string                `json:"epicId"`
	WorkID           string                `json:"workId"`
	Sequence         int                   `json:"sequence"`
	Phase            FactoryAttemptPhase   `json:"phase"`
	Outcome          FactoryAttemptOutcome `json:"outcome,omitempty"`
	Session          PlanningSession       `json:"session"`
	RetryOfAttemptID string                `json:"retryOfAttemptId,omitempty"`
	RetryAt          int64                 `json:"retryAt,omitempty"`
	FrozenPolicy     FactoryAttemptPolicy  `json:"frozenPolicy"`
	Result           *FactoryAttemptResult `json:"result,omitempty"`
	Failure          FactoryAttemptFailure `json:"failure,omitempty"`
	CreatedAt        int64                 `json:"createdAt"`
	UpdatedAt        int64                 `json:"updatedAt"`
	StartedAt        int64                 `json:"startedAt,omitempty"`
	FinishedAt       int64                 `json:"finishedAt,omitempty"`
	AgentToken       string                `json:"-"`
}

type RecoveryGate struct {
	IssueID    string   `json:"issueId"`
	EpicID     string   `json:"epicId"`
	AttemptID  string   `json:"attemptId"`
	WorkID     string   `json:"workId"`
	Question   string   `json:"question"`
	Reason     string   `json:"reason"`
	Choices    []string `json:"choices"`
	Response   string   `json:"response,omitempty"`
	Resolution string   `json:"resolution"`
}

// AuthorityEscalationGate is a one-time decision for a permission outside an
// implementation Attempt's frozen profile.
type AuthorityEscalationGate struct {
	IssueID    string `json:"issueId"`
	EpicID     string `json:"epicId"`
	AttemptID  string `json:"attemptId"`
	WorkID     string `json:"workId"`
	RequestID  string `json:"requestId"`
	Permission string `json:"permission"`
	Target     string `json:"target"`
	Resolution string `json:"resolution"`
}

type NativeIssueComment struct {
	ID        int64  `json:"id"`
	IssueID   string `json:"issueId"`
	Actor     string `json:"actor"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
}

type AuditRecord struct {
	EpicID    string
	WorkID    string
	AttemptID string
	Actor     string
	Action    string
	Details   any
	At        time.Time
}
