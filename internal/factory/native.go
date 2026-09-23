package factory

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

var customFormulaID = regexp.MustCompile(`^custom/[a-z][a-z0-9_-]*$`)

// epicIDPattern constrains a caller-supplied Epic ID. Dots are excluded
// because child issue IDs are "<epicID>.<n>", and the ID also has to stay
// safe inside the "factory/<epicID>" Git branch name.
var epicIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)

const planningProfile = "factory-plan/v1"

// maxGoalRunes caps the Epic goal, which doubles as the Epic's title in
// every list and header.
const maxGoalRunes = 80

var (
	ErrWorkEpicNotFound        = errors.New("work epic not found")
	ErrInstantiationConflict   = errors.New("factory instantiation conflict")
	ErrFactoryUnavailable      = errors.New("factory unavailable")
	ErrFormulaCorrupt          = errors.New("factory Formula is corrupt")
	ErrFormulaNotFound         = errors.New("factory Formula not found")
	ErrInvalidFormula          = errors.New("invalid formula")
	ErrInvalidRequest          = errors.New("invalid factory request")
	ErrProjectNotLocalGit      = errors.New("project is not a local Git repository")
	ErrActionNotPermitted      = errors.New("factory action is not permitted")
	ErrAcknowledgementRequired = errors.New("local execution acknowledgement is required")
	ErrEpicIDTaken             = errors.New("factory epic id already taken: pick another human-friendly id")
	ErrEpicProjectPermanent    = model.ErrEpicProjectPermanent
	ErrEpicProjectHistory      = model.ErrEpicProjectHistory
	ErrEpicProjectNotFound     = model.ErrEpicProjectNotFound
)

type Health string

const HealthHealthy Health = "healthy"

type Status struct {
	Health        Health         `json:"health"`
	Idle          bool           `json:"idle"`
	DispatchOwner bool           `json:"dispatchOwner"`
	Dispatch      []DispatchItem `json:"dispatch"`
}

type DispatchItem struct {
	ID            string                     `json:"id"`
	EpicID        string                     `json:"epicId"`
	Title         string                     `json:"title"`
	Project       string                     `json:"project"`
	State         DispatchState              `json:"state"`
	AttemptID     string                     `json:"attemptId,omitempty"`
	Session       model.PlanningSession      `json:"session,omitempty"`
	Outcome       string                     `json:"outcome,omitempty"`
	OutcomeReason string                     `json:"outcomeReason,omitempty"`
	Blockers      []model.NativeIssueBlocker `json:"blockers,omitempty"`
	RetryAt       int64                      `json:"retryAt,omitempty"`
	RetryAttempts int                        `json:"retryAttempts,omitempty"`
}

type DispatchState string

const (
	DispatchReady         DispatchState = "ready"
	DispatchRunning       DispatchState = "running"
	DispatchCompleted     DispatchState = "completed"
	DispatchDeferred      DispatchState = "deferred"
	DispatchRetryWait     DispatchState = "retry_wait"
	DispatchBlocked       DispatchState = "terminally_blocked"
	DispatchNotApplicable DispatchState = "not_applicable"
	DispatchPaused        DispatchState = "paused"
)

type FormulaOrigin string

const (
	FormulaOriginBuiltIn FormulaOrigin = "built_in"
	FormulaOriginCustom  FormulaOrigin = "custom"
)

type CreateWorkEpicRequest struct {
	InstantiationID string `json:"instantiationId"`
	// EpicID is an optional human-friendly kebab-case ID for the Epic,
	// normally supplied by the agent that creates it. Empty means the ID is
	// derived from the goal.
	EpicID                    string                     `json:"epicId,omitempty"`
	Goal                      string                     `json:"goal"`
	Brief                     string                     `json:"brief,omitempty"`
	InitialProject            string                     `json:"initialProject"`
	FormulaID                 string                     `json:"formulaId,omitempty"`
	FormulaRevision           int                        `json:"formulaRevision,omitempty"`
	AcknowledgeLocalExecution bool                       `json:"acknowledgeLocalExecution"`
	Projects                  []ProjectAdmission         `json:"projects,omitempty"`
	PermissionRules           []platforms.PermissionRule `json:"permissionRules,omitempty"`
}

type ProjectAdmission struct {
	Path                      string `json:"path"`
	RemoteID                  string `json:"remoteId,omitempty"`
	AcknowledgeLocalExecution bool   `json:"acknowledgeLocalExecution"`
}

type FactoryProgress struct {
	DeliveryStatus    string                  `json:"deliveryStatus,omitempty"`
	ProjectDeliveries []ProjectDeliveryStatus `json:"projectDeliveries,omitempty"`
	RequiredTotal     int                     `json:"requiredTotal"`
	RequiredSucceeded int                     `json:"requiredSucceeded"`
	OptionalOpen      int                     `json:"optionalOpen"`
	ClosureBlockers   []string                `json:"closureBlockers,omitempty"`
	// Stuck means closure is blocked yet nothing can move on its own: no
	// work is ready, running, or waiting to retry, and no gate is open.
	Stuck bool `json:"stuck,omitempty"`
}

type ProjectDeliveryStatus struct {
	Project string `json:"project"`
	IssueID string `json:"issueId,omitempty"`
	Status  string `json:"status"`
	Lineage int    `json:"lineage,omitempty"`
}

type WorkEpic struct {
	ID              string                 `json:"id"`
	Status          string                 `json:"status"`
	Goal            string                 `json:"goal"`
	Brief           string                 `json:"brief,omitempty"`
	InitialProject  string                 `json:"initialProject"`
	Projects        []model.EpicProject    `json:"projects"`
	FormulaID       string                 `json:"formulaId"`
	FormulaVersion  int                    `json:"formulaVersion"`
	FormulaRevision int                    `json:"formulaRevision"`
	FormulaHash     string                 `json:"formulaHash"`
	FormulaOrigin   FormulaOrigin          `json:"formulaOrigin"`
	InstantiationID string                 `json:"instantiationId"`
	Proposal        *ProposalRevision      `json:"proposal,omitempty"`
	PlanGate        *PlanGate              `json:"planGate,omitempty"`
	Models          model.EpicModels       `json:"models"`
	Attempts        []model.FactoryAttempt `json:"attempts,omitempty"`
	Progress        FactoryProgress        `json:"progress"`
}

type PlanningSession = model.PlanningSession
type FactoryAuditRecord = model.AuditRecord

type PlanningSessionRequest struct {
	Model                                                    string
	Prompt                                                   string
	EpicID, WorkID, AttemptID, AgentToken, Repository, Title string
	Projects                                                 []string
	ScopeExpansion                                           bool
	PermissionRules                                          []model.PermissionRule
}

type PlanningLauncher interface {
	LaunchPlanningSession(context.Context, PlanningSessionRequest) (PlanningSession, error)
	PromptPlanningSession(context.Context, PlanningSession, PlanningSessionRequest) error
	ProbePlanningSession(context.Context, PlanningSession) (bool, error)
	StopPlanningSession(context.Context, PlanningSession) error
}

// GraphMutation is a user-authorized structural change to open Factory work.
type GraphMutation = model.GraphMutation

type nativeStore interface {
	CreateFactoryEpic(ctx context.Context, preferredID, goal, brief, project, instantiationID string, formula model.NativeFormula) (model.NativeEpic, error)
	ListFactoryEpics(context.Context) ([]model.NativeEpic, error)
	GetFactoryEpic(context.Context, string) (model.NativeEpic, error)
	PourFactoryEpic(context.Context, string, model.NativeFormula) (model.NativeEpic, []model.NativeIssue, error)
	ListFactoryIssues(context.Context, string) ([]model.NativeIssue, error)
}

type nativeProjectSetStore interface {
	RemoveFactoryEpicProject(context.Context, string, string) error
}

type configuredEpicStore interface {
	CreateFactoryEpicWithProjectsAndPermissionRules(context.Context, string, string, string, string, string, model.NativeFormula, []string, []model.PermissionRule) (model.NativeEpic, error)
}
type nativeProjectRequestStore interface {
	CreateFactoryProjectRequestGate(context.Context, string, string, string, time.Time) (model.ProjectRequestGate, error)
	GetFactoryProjectRequestGate(context.Context, string) (model.ProjectRequestGate, bool, error)
	GetFactoryProjectRequestGateForPlan(context.Context, string) (model.ProjectRequestGate, bool, error)
	ResolveFactoryProjectRequestGate(context.Context, string, string, string, string, time.Time) (model.ProjectRequestGate, model.FactoryAttempt, error)
	CompleteFactoryProjectRequestRejection(context.Context, string, time.Time) (model.ProjectRequestGate, error)
	FailFactoryProjectRequestRejection(context.Context, string, time.Time) (model.ProjectRequestGate, error)
	ApplyFactoryScopePlan(context.Context, model.NativeProposalRevision, string, string, time.Time) (model.NativeProposalRevision, error)
}
type nativeFormulaStore interface {
	ListNativeFactoryFormulaRevisions(context.Context) ([]model.NativeFormulaRevision, error)
	GetNativeFactoryFormulaRevision(context.Context, string, int) (model.NativeFormulaRevision, error)
	SaveNativeFactoryFormulaRevision(context.Context, model.NativeFormulaRevision, time.Time) (model.NativeFormulaRevision, error)
}
type nativePlanningStore interface {
	ClaimFactoryPlan(context.Context, string, string, string, time.Time) (model.NativeEpic, model.FactoryAttempt, error)
	ListFactoryAttempts(context.Context, string) ([]model.FactoryAttempt, error)
	ActivateFactoryAttempt(context.Context, string, model.PlanningSession, time.Time) (bool, error)
	FailFactoryAttempt(context.Context, string, model.FactoryAttemptFailure, time.Time) (bool, error)
	SaveFactoryProposalRevision(context.Context, model.NativeProposalRevision) (model.NativeProposalRevision, error)
	ImportFactoryProposalRevision(context.Context, model.NativeProposalRevision) (model.NativeProposalRevision, bool, error)
	SaveFactoryProposalRevisionForAttempt(context.Context, model.NativeProposalRevision, string, string) (model.NativeProposalRevision, bool, error)
	GetFactoryProposalRevision(context.Context, string, int) (model.NativeProposalRevision, error)
	ListFactoryProposalRevisions(context.Context, string) ([]model.NativeProposalRevision, error)
	GetFactoryPlanGate(context.Context, string) (model.NativePlanGate, error)
	DecideFactoryPlanGate(context.Context, string, string, int, string, string, ...string) (model.NativePlanGate, error)
	MaterializeFactoryPlan(context.Context, string, string, string, time.Time) (model.NativeMaterialization, error)
	ClaimFactoryImplementation(context.Context, string, string, string, time.Time) (model.NativeEpic, model.FactoryAttempt, error)
}
type nativeAttemptCompletionStore interface {
	EnsureFactoryDeliveryIssue(context.Context, string) error
	SetFactoryAttemptWorkspace(context.Context, string, model.FactoryAttemptPolicy) error
	FactoryAttemptHasRecoveryResponse(context.Context, string, string) (bool, error)
	CompleteFactoryImplementationAttempt(context.Context, string, string, model.FactoryAttemptResult, time.Time) (bool, error)
	FactoryEpicPRURL(context.Context, string, string) (string, error)
	StopFactoryAttempt(context.Context, string, time.Time) (bool, error)
	ValidateFactoryAttemptToken(context.Context, string, string) (bool, error)
}
type nativeRecoveryStore interface {
	CreateFactoryRecoveryGate(context.Context, string, string, string, []string, time.Time) (model.RecoveryGate, error)
	ResolveFactoryRecoveryGate(context.Context, string, string, string, time.Time) (model.RecoveryGate, model.FactoryAttempt, error)
	CompleteFactoryRecoveryGate(context.Context, string, time.Time) (model.RecoveryGate, error)
	IsFactoryAttemptRecoveryPaused(context.Context, string) (bool, error)
	GetFactoryRecoveryGate(context.Context, string) (model.RecoveryGate, bool, error)
}
type nativeAuthorityStore interface {
	IsFactoryImplementationSession(context.Context, string) (bool, error)
	CreateFactoryAuthorityEscalationGate(context.Context, string, string, string, string, time.Time) (model.AuthorityEscalationGate, bool, error)
	ResolveFactoryAuthorityEscalationGate(context.Context, string, string, time.Time) (model.AuthorityEscalationGate, model.FactoryAttempt, error)
	CompleteFactoryAuthorityEscalationGate(context.Context, string, string, time.Time) (model.AuthorityEscalationGate, error)
	GetFactoryAuthorityEscalationGate(context.Context, string) (model.AuthorityEscalationGate, bool, error)
	GetFactoryAttempt(context.Context, string) (model.FactoryAttempt, bool, error)
}
type nativeDelayStore interface {
	DeferFactoryIssue(context.Context, string, string, string) error
	ResumeFactoryIssue(context.Context, string, string) error
	RetryFactoryIssueAt(context.Context, string, string, time.Time) error
	WakeFactoryRetries(context.Context, time.Time) error
}
type nativeClosureStore interface {
	CloseFactoryMol(context.Context, string, string) error
	CloseFactoryEpic(context.Context, string, bool) error
}
type nativeEpicLifecycleStore interface {
	SetFactoryEpicPaused(context.Context, string, bool) error
}
type nativeReopenStore interface {
	ReopenFactoryIssue(context.Context, string, string) error
}
type nativeMutationStore interface {
	MutateFactoryGraph(context.Context, GraphMutation) error
}
type nativeMergeGateStore interface {
	ListFactoryMergeGateDeliveries(context.Context, time.Time, int) ([]model.FactoryDeliveryObservation, error)
	RecordFactoryDeliveryObservation(context.Context, model.FactoryDeliveryObservation) error
}
type nativeCommentStore interface {
	AppendFactoryIssueComment(context.Context, string, string, string, string, time.Time) (model.NativeIssueComment, error)
	ListFactoryIssueComments(context.Context, string, string) ([]model.NativeIssueComment, error)
}
type capacityPolicyStore interface {
	GetFactoryCapacityPolicy(context.Context) (model.FactoryCapacityPolicy, error)
	SetFactoryCapacityPolicy(context.Context, model.FactoryCapacityPolicy) error
}
type localExecutionAckStore interface {
	UpsertFactoryLocalExecutionAck(context.Context, string, string, string, string, string, time.Time) error
}
type nativeProjectResolver interface {
	ResolveLocalProject(context.Context, string) (string, error)
}

// NativeService persists Factory's issue graph in ocman's state database.
type NativeService struct {
	store             nativeStore
	projects          nativeProjectResolver
	planning          PlanningLauncher
	implementation    ImplementationLauncher
	implementationMu  sync.Mutex
	planningMu        sync.Mutex
	materializationMu sync.Mutex
	recoveryMu        sync.Mutex
	authorityMu       sync.Mutex
	projectRequestMu  sync.Mutex
	mergeGateMu       sync.Mutex
	startOnce         sync.Once
	closeOnce         sync.Once
	dispatchWG        sync.WaitGroup
	dispatchWake      chan struct{}
	stop              chan struct{}
}

type ImplementationSessionRequest struct {
	Verification                                                                                    bool
	Prompt                                                                                          string
	Model                                                                                           string
	EpicID, WorkID, AttemptID, AgentToken, Repository, Title, Description, Branch, BaseRef, Profile string
	Projects                                                                                        []string
	TargetBranch                                                                                    string
	Delivery                                                                                        bool
	PermissionRules                                                                                 []model.PermissionRule
}

// ImplementationLauncher is the host/platform seam for a configured worktree
// session. Implementations must apply Profile before publishing the session.
type ImplementationLauncher interface {
	ResolveImplementationWorkspace(context.Context, string, string, PlanningSession) (string, string, error)
	PrepareImplementationWorkspace(context.Context, string, string, string, string) (string, string, error)
	ValidateImplementationCheckpoint(context.Context, string, string, string) (string, error)
	LaunchImplementationSession(context.Context, ImplementationSessionRequest) (PlanningSession, error)
	PromptImplementationSession(context.Context, PlanningSession, ImplementationSessionRequest) error
	ResumeImplementationSession(context.Context, PlanningSession, string, string) error
	ProbeImplementationSession(context.Context, PlanningSession) (bool, error)
	StopImplementationSession(context.Context, PlanningSession) error
	ImplementationPermissionPending(context.Context, PlanningSession, string) (bool, error)
	RespondImplementationPermission(context.Context, PlanningSession, string, string) error
	ResolveImplementationBranch(context.Context, string, string, string, model.FactoryAttemptPolicy) (string, string, error)
	ValidateImplementationHandoff(context.Context, string, string, string, string, model.FactoryAttemptPolicy) error
	ObserveImplementationDelivery(context.Context, string, model.FactoryAttemptPolicy) (string, string, error)
}

const maxCapacity = 1000

var factoryMergeGatePollInterval = 30 * time.Second

type CapacityPolicy = model.FactoryCapacityPolicy

func NewNative(store nativeStore, resolvers ...nativeProjectResolver) *NativeService {
	var projects nativeProjectResolver
	if len(resolvers) > 0 {
		projects = resolvers[0]
	}
	return &NativeService{store: store, projects: projects, dispatchWake: make(chan struct{}, 1), stop: make(chan struct{})}
}

func NewNativeWithPlanning(store nativeStore, projects nativeProjectResolver, planning PlanningLauncher) *NativeService {
	return &NativeService{store: store, projects: projects, planning: planning, dispatchWake: make(chan struct{}, 1), stop: make(chan struct{})}
}

func NewNativeWithExecution(store nativeStore, projects nativeProjectResolver, planning PlanningLauncher, implementation ImplementationLauncher) *NativeService {
	return &NativeService{store: store, projects: projects, planning: planning, implementation: implementation, dispatchWake: make(chan struct{}, 1), stop: make(chan struct{})}
}
