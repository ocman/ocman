package factory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/sirupsen/logrus"
)

var customFormulaID = regexp.MustCompile(`^custom/[a-z][a-z0-9_-]*$`)

const planningProfile = "factory-plan/v1"

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
	Repository    string                     `json:"repository"`
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
)

type FormulaOrigin string

const (
	FormulaOriginBuiltIn FormulaOrigin = "built_in"
	FormulaOriginCustom  FormulaOrigin = "custom"
)

type CreateWorkEpicRequest struct {
	InstantiationID           string `json:"instantiationId"`
	Goal                      string `json:"goal"`
	Brief                     string `json:"brief,omitempty"`
	InitialProject            string `json:"initialProject"`
	FormulaID                 string `json:"formulaId,omitempty"`
	FormulaRevision           int    `json:"formulaRevision,omitempty"`
	AcknowledgeLocalExecution bool   `json:"acknowledgeLocalExecution"`
}

type FactoryProgress struct {
	RequiredTotal     int      `json:"requiredTotal"`
	RequiredSucceeded int      `json:"requiredSucceeded"`
	OptionalOpen      int      `json:"optionalOpen"`
	ClosureBlockers   []string `json:"closureBlockers,omitempty"`
	// Stuck means closure is blocked yet nothing can move on its own: no
	// work is ready, running, or waiting to retry, and no gate is open.
	Stuck bool `json:"stuck,omitempty"`
}

type WorkEpic struct {
	ID              string                 `json:"id"`
	Status          string                 `json:"status"`
	Goal            string                 `json:"goal"`
	Brief           string                 `json:"brief,omitempty"`
	InitialProject  string                 `json:"initialProject"`
	FormulaID       string                 `json:"formulaId"`
	FormulaVersion  int                    `json:"formulaVersion"`
	FormulaRevision int                    `json:"formulaRevision"`
	FormulaHash     string                 `json:"formulaHash"`
	FormulaOrigin   FormulaOrigin          `json:"formulaOrigin"`
	InstantiationID string                 `json:"instantiationId"`
	Proposal        *ProposalRevision      `json:"proposal,omitempty"`
	PlanGate        *PlanGate              `json:"planGate,omitempty"`
	Attempts        []model.FactoryAttempt `json:"attempts,omitempty"`
	Progress        FactoryProgress        `json:"progress"`
}

type PlanningSession = model.PlanningSession
type FactoryAuditRecord = model.AuditRecord

type PlanningSessionRequest struct {
	EpicID, WorkID, AttemptID, AgentToken, Repository, Title string
}

type PlanningLauncher interface {
	LaunchPlanningSession(context.Context, PlanningSessionRequest) (PlanningSession, error)
	PromptPlanningSession(context.Context, PlanningSession, PlanningSessionRequest) error
	ProbePlanningSession(context.Context, PlanningSession) (bool, error)
	StopPlanningSession(context.Context, PlanningSession) error
}

const tracerFormulaSource = `version = 1
name = "Tracer"

[[input]]
key = "goal"

[[input]]
key = "initial_project"

[[issue]]
key = "plan"
kind = "plan"

[[issue]]
key = "approval"
kind = "gate"

[[issue]]
key = "materialization"
kind = "materialization"

[[dependency]]
from = "approval"
to = "plan"
type = "blocks"

[[dependency]]
from = "materialization"
to = "approval"
type = "blocks"
`

type TracerFormula struct {
	ID      string
	Version int
	Source  string
	Hash    string
}

// NativeFormulaView is the immutable, inspectable representation of a Formula revision.
type NativeFormulaView struct {
	ID          string               `json:"id"`
	Version     int                  `json:"version"`
	Name        string               `json:"name"`
	Source      string               `json:"source"`
	Hash        string               `json:"hash"`
	SourceHash  string               `json:"sourceHash"`
	Compiled    json.RawMessage      `json:"compiled"`
	Inputs      []string             `json:"inputs"`
	Nodes       []FormulaGraphNode   `json:"nodes"`
	Edges       []FormulaGraphEdge   `json:"edges"`
	Composition []FormulaComposition `json:"composition"`
	Valid       bool                 `json:"valid"`
	Errors      []string             `json:"errors"`
}

type FormulaSaveRequest struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

type FormulaGraphNode struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
}

type FormulaGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type FormulaComposition struct {
	Key         string            `json:"key"`
	Requirement string            `json:"requirement"`
	Formula     string            `json:"formula"`
	Revision    int               `json:"revision"`
	Bindings    map[string]string `json:"bindings"`
}

func BuiltInTracerFormula() TracerFormula {
	// Keep the built-in identity compatible with the original tracer release.
	compiled, err := compileNativeFormula(tracerFormulaSource)
	if err != nil {
		panic("invalid built-in tracer Formula: " + err.Error())
	}
	return TracerFormula{ID: "ocman/tracer", Version: 1, Source: tracerFormulaSource, Hash: compiled.Hash}
}

func sourceHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

type compiledNativeFormula struct {
	Version     int                  `json:"version"`
	Name        string               `json:"name"`
	Inputs      []string             `json:"inputs"`
	Nodes       []FormulaGraphNode   `json:"nodes"`
	Edges       []FormulaGraphEdge   `json:"edges"`
	Composition []FormulaComposition `json:"composition"`
}

type nativeDefinition struct {
	compiledNativeFormula
	JSON string
	Hash string
}

// compileNativeFormula accepts only the deliberately small TOML schema used by native Factory.
func compileNativeFormula(source string) (nativeDefinition, error) {
	var result compiledNativeFormula
	seenRoot, seenInput, seenNode, seenEdge, seenStable := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	section := ""
	fields := map[string]string{}
	flush := func(line int) error {
		if section == "" {
			return nil
		}
		need := func(keys ...string) error {
			for _, key := range keys {
				if fields[key] == "" {
					return fmt.Errorf("line %d: %s requires %s", line, section, key)
				}
			}
			return nil
		}
		switch section {
		case "input":
			if err := need("key"); err != nil {
				return err
			}
			if !model.ValidNativeFormulaKey(fields["key"]) || seenInput[fields["key"]] {
				return fmt.Errorf("line %d: input keys must be unique and stable", line)
			}
			seenInput[fields["key"]] = true
			result.Inputs = append(result.Inputs, fields["key"])
		case "issue":
			if err := need("key", "kind"); err != nil {
				return err
			}
			if !model.ValidNativeFormulaKey(fields["key"]) || seenStable[fields["key"]] {
				return fmt.Errorf("line %d: stable keys must be unique", line)
			}
			if fields["kind"] != "plan" && fields["kind"] != "gate" && fields["kind"] != "materialization" {
				return fmt.Errorf("line %d: invalid issue kind", line)
			}
			seenNode[fields["key"]] = true
			seenStable[fields["key"]] = true
			result.Nodes = append(result.Nodes, FormulaGraphNode{Key: fields["key"], Kind: fields["kind"]})
		case "dependency":
			if err := need("from", "to", "type"); err != nil {
				return err
			}
			if fields["type"] != "blocks" && fields["type"] != "on_failure" {
				return fmt.Errorf("line %d: invalid dependency type", line)
			}
			key := fields["from"] + "\x00" + fields["to"]
			if seenEdge[key] {
				return fmt.Errorf("line %d: duplicate dependency", line)
			}
			seenEdge[key] = true
			result.Edges = append(result.Edges, FormulaGraphEdge{From: fields["from"], To: fields["to"], Type: fields["type"]})
		case "composition":
			if err := need("key", "formula", "revision"); err != nil {
				return err
			}
			revision, err := strconv.Atoi(fields["revision"])
			if err != nil || revision < 1 {
				return fmt.Errorf("line %d: composition revision must be a positive integer", line)
			}
			if !model.ValidNativeFormulaKey(fields["key"]) || seenStable[fields["key"]] {
				return fmt.Errorf("line %d: stable keys must be unique", line)
			}
			requirement := fields["requirement"]
			if requirement == "" {
				requirement = "required"
			}
			if requirement != "required" && requirement != "optional" && requirement != "reference" {
				return fmt.Errorf("line %d: composition requirement must be required, optional, or reference", line)
			}
			bindings := map[string]string{}
			for key, value := range fields {
				if strings.HasPrefix(key, "bind_") {
					bindings[strings.TrimPrefix(key, "bind_")] = value
				}
			}
			seenStable[fields["key"]] = true
			result.Composition = append(result.Composition, FormulaComposition{Key: fields["key"], Requirement: requirement, Formula: fields["formula"], Revision: revision, Bindings: bindings})
		}
		return nil
	}
	for lineNo, raw := range strings.Split(source, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			if err := flush(lineNo + 1); err != nil {
				return nativeDefinition{}, err
			}
			section = strings.TrimSuffix(strings.TrimPrefix(line, "[["), "]]")
			if section != "input" && section != "issue" && section != "dependency" && section != "composition" {
				return nativeDefinition{}, fmt.Errorf("line %d: unsupported TOML table", lineNo+1)
			}
			fields = map[string]string{}
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nativeDefinition{}, fmt.Errorf("line %d: expected TOML key = value", lineNo+1)
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if section == "" {
			if key != "version" && key != "name" {
				return nativeDefinition{}, fmt.Errorf("line %d: unsupported top-level key", lineNo+1)
			}
			if seenRoot[key] {
				return nativeDefinition{}, fmt.Errorf("line %d: duplicate key %s", lineNo+1, key)
			}
			seenRoot[key] = true
			if key == "version" {
				n, err := strconv.Atoi(value)
				if err != nil || n != 1 {
					return nativeDefinition{}, fmt.Errorf("line %d: version must be 1", lineNo+1)
				}
				result.Version = n
			} else {
				name, err := unquoteTOML(value)
				if err != nil || name == "" {
					return nativeDefinition{}, fmt.Errorf("line %d: name must be a non-empty TOML string", lineNo+1)
				}
				result.Name = name
			}
			continue
		}
		allowed := map[string]bool{"input": key == "key", "issue": key == "key" || key == "kind", "dependency": key == "from" || key == "to" || key == "type", "composition": key == "key" || key == "formula" || key == "revision" || key == "requirement" || strings.HasPrefix(key, "bind_")}[section]
		if !allowed {
			return nativeDefinition{}, fmt.Errorf("line %d: unsupported %s key", lineNo+1, section)
		}
		if _, exists := fields[key]; exists {
			return nativeDefinition{}, fmt.Errorf("line %d: duplicate key %s", lineNo+1, key)
		}
		parsed := value
		if section != "composition" || key != "revision" {
			var err error
			parsed, err = unquoteTOML(value)
			if err != nil {
				return nativeDefinition{}, fmt.Errorf("line %d: %s must be a TOML string", lineNo+1, key)
			}
		}
		fields[key] = parsed
	}
	if err := flush(len(strings.Split(source, "\n")) + 1); err != nil {
		return nativeDefinition{}, err
	}
	if result.Version != 1 || result.Name == "" {
		return nativeDefinition{}, errors.New("formula requires version = 1 and name")
	}
	if !reflect.DeepEqual(result.Inputs, []string{"goal", "initial_project"}) {
		return nativeDefinition{}, errors.New("formula inputs must be goal and initial_project")
	}
	if len(result.Nodes) == 0 {
		return nativeDefinition{}, errors.New("formula requires at least one issue")
	}
	for _, edge := range result.Edges {
		if !seenNode[edge.From] || !seenNode[edge.To] {
			return nativeDefinition{}, errors.New("dependency references unknown issue")
		}
	}
	if hasFormulaCycle(result.Edges) {
		return nativeDefinition{}, errors.New("formula dependencies must be acyclic")
	}
	sort.Strings(result.Inputs)
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].Key < result.Nodes[j].Key })
	sort.Slice(result.Edges, func(i, j int) bool {
		if result.Edges[i].From == result.Edges[j].From {
			return result.Edges[i].To < result.Edges[j].To
		}
		return result.Edges[i].From < result.Edges[j].From
	})
	sort.Slice(result.Composition, func(i, j int) bool { return result.Composition[i].Key < result.Composition[j].Key })
	compiled, err := json.Marshal(result)
	if err != nil {
		return nativeDefinition{}, err
	}
	sum := sha256.Sum256(compiled)
	return nativeDefinition{compiledNativeFormula: result, JSON: string(compiled), Hash: hex.EncodeToString(sum[:])}, nil
}

func unquoteTOML(value string) (string, error) {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", errors.New("not a TOML string")
	}
	for i := 1; i < len(value)-1; i++ {
		if value[i] != '\\' {
			continue
		}
		i++
		if i == len(value)-1 || !strings.ContainsRune(`btnfr"\\`, rune(value[i])) {
			if value[i] != 'u' && value[i] != 'U' {
				return "", errors.New("invalid TOML escape")
			}
		}
	}
	return strconv.Unquote(value)
}

func hasFormulaCycle(edges []FormulaGraphEdge) bool {
	next := map[string][]string{}
	for _, edge := range edges {
		next[edge.From] = append(next[edge.From], edge.To)
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(node string) bool {
		if visiting[node] {
			return true
		}
		if visited[node] {
			return false
		}
		visiting[node] = true
		for _, child := range next[node] {
			if visit(child) {
				return true
			}
		}
		delete(visiting, node)
		visited[node] = true
		return false
	}
	for node := range next {
		if visit(node) {
			return true
		}
	}
	return false
}

type Issue struct {
	ID             string                     `json:"id"`
	EpicID         string                     `json:"epicId"`
	ParentID       string                     `json:"parentId,omitempty"`
	Requirement    string                     `json:"requirement,omitempty"`
	FormulaID      string                     `json:"formulaId,omitempty"`
	FormulaVersion int                        `json:"formulaVersion,omitempty"`
	FormulaHash    string                     `json:"formulaHash,omitempty"`
	Bindings       map[string]string          `json:"bindings,omitempty"`
	Kind           string                     `json:"kind"`
	Title          string                     `json:"title"`
	Status         string                     `json:"status"`
	Outcome        string                     `json:"outcome,omitempty"`
	OutcomeReason  string                     `json:"outcomeReason,omitempty"`
	Conclusion     string                     `json:"conclusion,omitempty"`
	PRURL          string                     `json:"prUrl,omitempty"`
	DispatchState  string                     `json:"dispatchState,omitempty"`
	Blockers       []model.NativeIssueBlocker `json:"blockers,omitempty"`
	RetryAt        int64                      `json:"retryAt,omitempty"`
	RetryAttempts  int                        `json:"retryAttempts,omitempty"`
	Description    string                     `json:"description,omitempty"`
	PlanRevision   int                        `json:"planRevision,omitempty"`
	ManifestKey    string                     `json:"manifestKey,omitempty"`
	RemovedAt      int64                      `json:"removedAt,omitempty"`
	AttemptID      string                     `json:"attemptId,omitempty"`
	Session        PlanningSession            `json:"session,omitempty"`
	Recovery       *RecoveryGate              `json:"recovery,omitempty"`
	Authority      *AuthorityEscalationGate   `json:"authority,omitempty"`
}

type RecoveryGate = model.RecoveryGate
type AuthorityEscalationGate = model.AuthorityEscalationGate
type IssueComment = model.NativeIssueComment

type ManifestNode struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Requirement string `json:"requirement"`
	Pinned      bool   `json:"pinned,omitempty"`
}

// ProposalManifest is deliberately limited to the tracer's one implementation node.
type ProposalManifest struct {
	EpicID  string         `json:"epicId"`
	MolID   string         `json:"molId"`
	Project string         `json:"project"`
	Nodes   []ManifestNode `json:"nodes"`
}

type SubmitProposalRequest struct {
	EpicID            string           `json:"epicId"`
	Manifest          ProposalManifest `json:"manifest"`
	RationaleMarkdown string           `json:"rationaleMarkdown,omitempty"`
	// AttemptID/AttemptToken prove a Planning Session owns EpicID. Agents
	// must send both; a user submitting through the UI sends neither.
	AttemptID    string `json:"attemptId,omitempty"`
	AttemptToken string `json:"attemptToken,omitempty"`
}

type ProposalRevision struct {
	EpicID            string           `json:"epicId"`
	MolID             string           `json:"molId"`
	Project           string           `json:"project"`
	Revision          int              `json:"revision"`
	Manifest          ProposalManifest `json:"manifest"`
	RationaleMarkdown string           `json:"rationaleMarkdown,omitempty"`
	ContentHash       string           `json:"contentHash"`
	CreatedAt         int64            `json:"createdAt"`
}

type PlanGate struct {
	IssueID          string   `json:"issueId"`
	ProposalRevision int      `json:"proposalRevision"`
	ProposalHash     string   `json:"proposalHash"`
	Outcome          string   `json:"outcome,omitempty"`
	Resolution       string   `json:"resolution"`
	Feedback         string   `json:"feedback,omitempty"`
	ReviewIssueIDs   []string `json:"reviewIssueIds,omitempty"`
}

type PlanGateDecisionRequest struct {
	ExpectedRevision int    `json:"expectedRevision"`
	ExpectedHash     string `json:"expectedHash"`
	Actor            string `json:"actor,omitempty"`
	Feedback         string `json:"feedback,omitempty"`
}

type ClaimedPlan struct {
	Attempt model.FactoryAttempt  `json:"attempt"`
	Session model.PlanningSession `json:"session"`
}

type Materialization struct {
	ID               string `json:"id"`
	IssueID          string `json:"issueId"`
	ProposalRevision int    `json:"proposalRevision"`
	ProposalHash     string `json:"proposalHash"`
	ManifestKey      string `json:"manifestKey"`
	ImplementationID string `json:"implementationId"`
}

// GraphMutation is a user-authorized structural change to open Factory work.
type GraphMutation = model.GraphMutation

type nativeStore interface {
	CreateFactoryEpic(context.Context, string, string, string, string, model.NativeFormula) (model.NativeEpic, error)
	ListFactoryEpics(context.Context) ([]model.NativeEpic, error)
	GetFactoryEpic(context.Context, string) (model.NativeEpic, error)
	PourFactoryEpic(context.Context, string, model.NativeFormula) (model.NativeEpic, []model.NativeIssue, error)
	ListFactoryIssues(context.Context, string) ([]model.NativeIssue, error)
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
	SaveFactoryProposalRevisionForAttempt(context.Context, model.NativeProposalRevision, string, string) (model.NativeProposalRevision, bool, error)
	GetFactoryProposalRevision(context.Context, string, int) (model.NativeProposalRevision, error)
	ListFactoryProposalRevisions(context.Context, string) ([]model.NativeProposalRevision, error)
	GetFactoryPlanGate(context.Context, string) (model.NativePlanGate, error)
	DecideFactoryPlanGate(context.Context, string, string, int, string, string) (model.NativePlanGate, error)
	MaterializeFactoryPlan(context.Context, string, string, string, time.Time) (model.NativeMaterialization, error)
	ClaimFactoryImplementation(context.Context, string, string, string, time.Time) (model.NativeEpic, model.FactoryAttempt, error)
}
type nativeAttemptCompletionStore interface {
	CompleteFactoryImplementationAttempt(context.Context, string, string, model.FactoryAttemptResult, time.Time) (bool, error)
	FactoryEpicPRURL(context.Context, string) (string, error)
	StopFactoryAttempt(context.Context, string, time.Time) (bool, error)
	ValidateFactoryAttemptToken(context.Context, string, string) (bool, error)
}
type nativeRecoveryStore interface {
	CreateFactoryRecoveryGate(context.Context, string, string, string, []string, time.Time) (model.RecoveryGate, error)
	ResolveFactoryRecoveryGate(context.Context, string, string, string, time.Time) (model.RecoveryGate, model.FactoryAttempt, error)
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
	CloseFactoryEpic(context.Context, string) error
}
type nativeReopenStore interface {
	ReopenFactoryIssue(context.Context, string, string) error
}
type nativeMutationStore interface {
	MutateFactoryGraph(context.Context, GraphMutation) error
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
	authorityMu       sync.Mutex
	startOnce         sync.Once
	closeOnce         sync.Once
	dispatchWG        sync.WaitGroup
	dispatchWake      chan struct{}
	stop              chan struct{}
}

type ImplementationSessionRequest struct {
	EpicID, WorkID, AttemptID, AgentToken, Repository, Title, Description, Branch, Profile string
}

// ImplementationLauncher is the host/platform seam for a configured worktree
// session. Implementations must apply Profile before publishing the session.
type ImplementationLauncher interface {
	LaunchImplementationSession(context.Context, ImplementationSessionRequest) (PlanningSession, error)
	PromptImplementationSession(context.Context, PlanningSession, ImplementationSessionRequest) error
	ProbeImplementationSession(context.Context, PlanningSession) (bool, error)
	StopImplementationSession(context.Context, PlanningSession) error
	ImplementationPermissionPending(context.Context, PlanningSession, string) (bool, error)
	RespondImplementationPermission(context.Context, PlanningSession, string, string) error
	ValidateImplementationHandoff(context.Context, string, string, string, model.FactoryAttemptPolicy) error
}

const maxCapacity = 1000

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

func (s *NativeService) Start(ctx context.Context) error {
	s.planningMu.Lock()
	defer s.planningMu.Unlock()
	store, ok := s.store.(nativePlanningStore)
	if !ok || s.planning == nil {
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	attempts, err := store.ListFactoryAttempts(ctx, "")
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Phase == model.FactoryAttemptTerminal {
			continue
		}
		if attempt.FrozenPolicy.Profile == "factory-implement/v1" {
			if recovery, ok := s.store.(nativeRecoveryStore); ok {
				paused, err := recovery.IsFactoryAttemptRecoveryPaused(ctx, attempt.ID)
				if err != nil {
					return err
				}
				if paused {
					continue
				}
			}
			if s.implementation == nil {
				continue
			}
			if attempt.Phase == model.FactoryAttemptActive && attempt.FrozenPolicy.DeliveryRemoteRepo == "" {
				if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
					continue
				}
				if _, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "delivery_migration", Message: "Implementation attempt predates shared delivery"}, time.Now()); err != nil {
					return fmt.Errorf("migrate Implementation attempt: %w", err)
				}
				continue
			}
			if attempt.Phase == model.FactoryAttemptStopping {
				if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
					continue
				}
			}
			if attempt.Phase == model.FactoryAttemptActive && attempt.Session.ID != "" {
				alive, err := s.implementation.ProbeImplementationSession(ctx, attempt.Session)
				if err != nil || alive {
					continue
				}
			}
			_, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "interrupted_startup", Message: "Implementation Session was not durably available after restart"}, time.Now())
			if err != nil {
				return fmt.Errorf("recover Implementation attempt: %w", err)
			}
			continue
		}
		if attempt.FrozenPolicy.Profile != planningProfile {
			continue
		}
		issues, err := s.store.ListFactoryIssues(ctx, attempt.EpicID)
		if err != nil {
			return err
		}
		isPlan := false
		for _, issue := range issues {
			if issue.ID == attempt.WorkID && issue.Kind == "plan" {
				isPlan = true
				break
			}
		}
		if !isPlan {
			continue
		}
		if attempt.Phase == model.FactoryAttemptActive && attempt.Session.ID != "" && attempt.Session.Platform != "" {
			alive, err := s.planning.ProbePlanningSession(ctx, attempt.Session)
			if err != nil || alive {
				continue
			}
		}
		if _, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "interrupted_startup", Message: "Planning Session was not durably available after restart"}, time.Now()); err != nil {
			return fmt.Errorf("recover Planning attempt: %w", err)
		}
	}
	if s.implementation != nil {
		s.startOnce.Do(func() {
			s.dispatchWG.Add(1)
			go func() {
				defer s.dispatchWG.Done()
				s.runDispatch()
			}()
		})
	}
	return nil
}
func (s *NativeService) runDispatch() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-s.stop
		cancel()
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.Dispatch(ctx); err != nil && ctx.Err() == nil {
				logrus.WithError(err).Error("Factory dispatch failed")
			}
		case <-s.dispatchWake:
			if err := s.Dispatch(ctx); err != nil && ctx.Err() == nil {
				logrus.WithError(err).Error("Factory dispatch failed")
			}
		case <-s.stop:
			return
		}
	}
}
func (s *NativeService) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
		s.dispatchWG.Wait()
	})
}
func (*NativeService) Status(context.Context) Status {
	return Status{Health: HealthHealthy, Idle: true, DispatchOwner: true, Dispatch: []DispatchItem{}}
}

func (s *NativeService) GetCapacityPolicy(ctx context.Context) (CapacityPolicy, error) {
	store, ok := s.store.(capacityPolicyStore)
	if !ok {
		return CapacityPolicy{}, ErrFactoryUnavailable
	}
	return store.GetFactoryCapacityPolicy(ctx)
}

func (s *NativeService) SetCapacityPolicy(ctx context.Context, policy CapacityPolicy) (CapacityPolicy, error) {
	if err := validateCapacityPolicy(policy); err != nil {
		return CapacityPolicy{}, err
	}
	if s.projects == nil && len(policy.ProjectOverrides) != 0 {
		return CapacityPolicy{}, ErrProjectNotLocalGit
	}
	canonical := make(map[string]int, len(policy.ProjectOverrides))
	for project, capacity := range policy.ProjectOverrides {
		root, err := s.canonicalProject(ctx, project)
		if err != nil {
			return CapacityPolicy{}, err
		}
		if existing, exists := canonical[root]; exists && existing != capacity {
			return CapacityPolicy{}, fmt.Errorf("%w: factory project override aliases disagree", ErrInvalidRequest)
		}
		canonical[root] = capacity
	}
	policy.ProjectOverrides = canonical
	store, ok := s.store.(capacityPolicyStore)
	if !ok {
		return CapacityPolicy{}, ErrFactoryUnavailable
	}
	if err := store.SetFactoryCapacityPolicy(ctx, policy); err != nil {
		return CapacityPolicy{}, err
	}
	return policy, nil
}

func validateCapacityPolicy(policy CapacityPolicy) error {
	for _, capacity := range append([]int{policy.GlobalCapacity, policy.ProjectCapacity}, mapValues(policy.ProjectOverrides)...) {
		if capacity < 1 || capacity > maxCapacity {
			return fmt.Errorf("%w: factory capacity must be between 1 and %d", ErrInvalidRequest, maxCapacity)
		}
	}
	return nil
}

func mapValues(values map[string]int) []int {
	result := make([]int, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func (s *NativeService) canonicalProject(ctx context.Context, path string) (string, error) {
	if !filepath.IsAbs(path) || s.projects == nil {
		return "", ErrProjectNotLocalGit
	}
	project, err := s.projects.ResolveLocalProject(ctx, path)
	if err != nil || !filepath.IsAbs(project) {
		return "", fmt.Errorf("%w: %w", ErrProjectNotLocalGit, err)
	}
	return filepath.Clean(project), nil
}

func (s *NativeService) CreateWorkEpic(ctx context.Context, req CreateWorkEpicRequest) (WorkEpic, error) {
	if strings.TrimSpace(req.Goal) == "" || strings.TrimSpace(req.InitialProject) == "" {
		return WorkEpic{}, fmt.Errorf("%w: goal and initialProject are required", ErrInvalidRequest)
	}
	project, err := s.canonicalProject(ctx, req.InitialProject)
	if err != nil {
		return WorkEpic{}, err
	}
	req.InitialProject = project
	if !req.AcknowledgeLocalExecution {
		return WorkEpic{}, ErrAcknowledgementRequired
	}
	acks, ok := s.store.(localExecutionAckStore)
	if !ok {
		return WorkEpic{}, ErrFactoryUnavailable
	}
	if err := acks.UpsertFactoryLocalExecutionAck(ctx, "local", project, "factory-implement", "v1", "operator", time.Now()); err != nil {
		return WorkEpic{}, fmt.Errorf("%w: record local execution acknowledgement: %w", ErrFactoryUnavailable, err)
	}
	formulaID, revision := req.FormulaID, req.FormulaRevision
	if formulaID == "" {
		formulaID, revision = "ocman/tracer", 1
	}
	formula, err := s.nativeFormula(ctx, formulaID, revision)
	if err != nil {
		return WorkEpic{}, err
	}
	epic, err := s.store.CreateFactoryEpic(ctx, req.Goal, req.Brief, req.InitialProject, req.InstantiationID, formula)
	if errors.Is(err, model.ErrNativeInstantiationConflict) {
		err = ErrInstantiationConflict
	}
	return nativeEpic(epic), err
}

func (s *NativeService) ListWorkEpics(ctx context.Context) ([]WorkEpic, error) {
	epics, err := s.store.ListFactoryEpics(ctx)
	result := nativeEpics(epics)
	if store, ok := s.store.(nativePlanningStore); ok {
		for i := range result {
			if gate, gateErr := store.GetFactoryPlanGate(ctx, result[i].ID); gateErr == nil {
				decoded := nativePlanGate(gate)
				result[i].PlanGate = &decoded
			}
		}
	}
	for i := range result {
		issues, issuesErr := s.store.ListFactoryIssues(ctx, result[i].ID)
		if issuesErr != nil {
			return nil, issuesErr
		}
		result[i].Progress = factoryProgress(issues)
		result[i].Progress.Stuck = result[i].Progress.Stuck && result[i].Status == "open"
	}
	return result, err
}

func (s *NativeService) CloseMol(ctx context.Context, epicID, molID string) error {
	store, ok := s.store.(nativeClosureStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	return store.CloseFactoryMol(ctx, epicID, molID)
}

func (s *NativeService) CloseEpic(ctx context.Context, epicID string) error {
	store, ok := s.store.(nativeClosureStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	return store.CloseFactoryEpic(ctx, epicID)
}

// ReopenIssue returns failed or cancelled work to the queue and dispatches.
func (s *NativeService) ReopenIssue(ctx context.Context, epicID, issueID string) error {
	store, ok := s.store.(nativeReopenStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	if err := store.ReopenFactoryIssue(ctx, epicID, issueID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if err := s.Dispatch(ctx); err != nil {
		select {
		case s.dispatchWake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (s *NativeService) MutateGraph(ctx context.Context, mutation GraphMutation) error {
	store, ok := s.store.(nativeMutationStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	err := store.MutateFactoryGraph(ctx, mutation)
	if errors.Is(err, model.ErrInvalidGraphMutation) {
		return fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	return err
}

func factoryProgress(issues []model.NativeIssue) FactoryProgress {
	byID := make(map[string]model.NativeIssue, len(issues))
	for _, issue := range issues {
		byID[issue.ID] = issue
	}
	var progress FactoryProgress
	movable := false
	for _, issue := range issues {
		if issue.Kind == "mol" {
			continue
		}
		switch {
		case issue.Status == "in_progress" || issue.Status == "retry_wait",
			issue.Kind == "gate" && issue.Status == "open",
			issue.DispatchState == "ready" && issue.Kind != "gate":
			movable = true
		}
		requirement := issue.Requirement
		for parent := issue.ParentID; parent != ""; parent = byID[parent].ParentID {
			if byID[parent].Requirement == "reference" {
				requirement = "reference"
				break
			}
			if byID[parent].Requirement == "optional" {
				requirement = "optional"
			}
		}
		switch requirement {
		case "reference":
			continue
		case "optional":
			if issue.Status != "closed" {
				progress.OptionalOpen++
			}
		default:
			progress.RequiredTotal++
			if issue.Status == "closed" && issue.Outcome == "succeeded" && (issue.Kind != "gate" || issue.GateResolution == "approved") {
				progress.RequiredSucceeded++
			} else {
				progress.ClosureBlockers = append(progress.ClosureBlockers, issue.Title)
			}
		}
	}
	progress.Stuck = len(progress.ClosureBlockers) > 0 && !movable
	return progress
}

// GetFormula returns the exact immutable native Formula revision.
func (s *NativeService) GetFormula(ctx context.Context, id string, version int) (NativeFormulaView, error) {
	formula := BuiltInTracerFormula()
	if id != formula.ID || version != formula.Version {
		store, ok := s.store.(nativeFormulaStore)
		if !ok {
			return NativeFormulaView{}, ErrFactoryUnavailable
		}
		saved, err := store.GetNativeFactoryFormulaRevision(ctx, id, version)
		if errors.Is(err, sql.ErrNoRows) {
			return NativeFormulaView{}, ErrFormulaNotFound
		}
		if err != nil {
			return NativeFormulaView{}, fmt.Errorf("%w: reading Formula revision: %w", ErrFactoryUnavailable, err)
		}
		return nativeFormulaView(saved.FormulaID, saved.Revision, saved.Name, saved.SourceTOML, saved.ContentHash, saved.CompiledJSON)
	}
	compiled, err := compileNativeFormula(formula.Source)
	if err != nil {
		return NativeFormulaView{}, err
	}
	return NativeFormulaView{ID: formula.ID, Version: formula.Version, Name: compiled.Name, Source: formula.Source, Hash: compiled.Hash, SourceHash: sourceHash(formula.Source), Compiled: json.RawMessage(compiled.JSON), Inputs: compiled.Inputs, Nodes: compiled.Nodes, Edges: compiled.Edges, Composition: compiled.Composition, Valid: true, Errors: []string{}}, nil
}

func (s *NativeService) ListFormulas(ctx context.Context) ([]NativeFormulaView, error) {
	builtIn, err := s.GetFormula(ctx, "ocman/tracer", 1)
	if err != nil {
		return nil, err
	}
	result := []NativeFormulaView{builtIn}
	store, ok := s.store.(nativeFormulaStore)
	if !ok {
		return result, nil
	}
	saved, err := store.ListNativeFactoryFormulaRevisions(ctx)
	if err != nil {
		return nil, err
	}
	for _, revision := range saved {
		view, err := nativeFormulaView(revision.FormulaID, revision.Revision, revision.Name, revision.SourceTOML, revision.ContentHash, revision.CompiledJSON)
		if err != nil {
			return nil, err
		}
		result = append(result, view)
	}
	return result, nil
}

func (s *NativeService) ValidateFormula(ctx context.Context, source, id string) (NativeFormulaView, error) {
	return s.previewNativeFormula(ctx, id, s.nextFormulaRevision(ctx, id, ""), "", source)
}
func (s *NativeService) PreviewFormula(ctx context.Context, source, id string) (NativeFormulaView, error) {
	return s.previewNativeFormula(ctx, id, s.nextFormulaRevision(ctx, id, ""), "", source)
}
func (s *NativeService) SaveFormula(ctx context.Context, req FormulaSaveRequest) (NativeFormulaView, error) {
	if !customFormulaID.MatchString(req.ID) {
		return NativeFormulaView{}, errors.New("formula id must be custom/<stable-name>")
	}
	definition, err := compileNativeFormula(req.Source)
	if err != nil {
		return NativeFormulaView{}, fmt.Errorf("%w: %w", ErrInvalidFormula, err)
	}
	version := s.nextFormulaRevision(ctx, req.ID, definition.Hash)
	view, err := s.previewNativeFormula(ctx, req.ID, version, "", req.Source)
	if err != nil {
		return NativeFormulaView{}, err
	}
	if !view.Valid {
		return NativeFormulaView{}, fmt.Errorf("%w: %s", ErrInvalidFormula, strings.Join(view.Errors, "; "))
	}
	store, ok := s.store.(nativeFormulaStore)
	if !ok {
		return NativeFormulaView{}, ErrFactoryUnavailable
	}
	saved, err := store.SaveNativeFactoryFormulaRevision(ctx, model.NativeFormulaRevision{FormulaID: req.ID, Name: view.Name, SourceTOML: req.Source, CompiledJSON: string(view.Compiled), ContentHash: view.Hash}, time.Now())
	if err != nil {
		return NativeFormulaView{}, err
	}
	return nativeFormulaView(saved.FormulaID, saved.Revision, saved.Name, saved.SourceTOML, saved.ContentHash, saved.CompiledJSON)
}

func (s *NativeService) nextFormulaRevision(ctx context.Context, id, hash string) int {
	store, ok := s.store.(nativeFormulaStore)
	if !ok {
		return 1
	}
	formulas, err := store.ListNativeFactoryFormulaRevisions(ctx)
	if err != nil {
		return 1
	}
	next := 1
	for _, formula := range formulas {
		if formula.FormulaID != id {
			continue
		}
		if formula.ContentHash == hash {
			return formula.Revision
		}
		if formula.Revision >= next {
			next = formula.Revision + 1
		}
	}
	return next
}

func (s *NativeService) previewNativeFormula(ctx context.Context, id string, version int, name, source string) (NativeFormulaView, error) {
	compiled, err := compileNativeFormula(source)
	if err != nil {
		return NativeFormulaView{ID: id, Version: version, Source: source, Valid: false, Errors: []string{err.Error()}}, nil
	}
	if name != "" {
		compiled.Name = name
		raw, marshalErr := json.Marshal(compiled.compiledNativeFormula)
		if marshalErr != nil {
			return NativeFormulaView{}, marshalErr
		}
		compiled.JSON = string(raw)
		sum := sha256.Sum256(raw)
		compiled.Hash = hex.EncodeToString(sum[:])
	}
	problems := s.compositionErrors(ctx, id, version, compiled, map[string]bool{})
	return NativeFormulaView{ID: id, Version: version, Name: compiled.Name, Source: source, Hash: compiled.Hash, SourceHash: sourceHash(source), Compiled: json.RawMessage(compiled.JSON), Inputs: compiled.Inputs, Nodes: compiled.Nodes, Edges: compiled.Edges, Composition: compiled.Composition, Valid: len(problems) == 0, Errors: problems}, nil
}

func nativeFormulaView(id string, revision int, _ string, source, hash, compiledJSON string) (NativeFormulaView, error) {
	compiled, err := compileNativeFormula(source)
	if err != nil {
		return NativeFormulaView{}, fmt.Errorf("%w: recompiling native Formula: %w", ErrFormulaCorrupt, err)
	}
	if compiledJSON != compiled.JSON || hash != compiled.Hash {
		return NativeFormulaView{}, fmt.Errorf("%w: compiled content does not match source", ErrFormulaCorrupt)
	}
	return NativeFormulaView{ID: id, Version: revision, Name: compiled.Name, Source: source, Hash: compiled.Hash, SourceHash: sourceHash(source), Compiled: json.RawMessage(compiled.JSON), Inputs: compiled.Inputs, Nodes: compiled.Nodes, Edges: compiled.Edges, Composition: compiled.Composition, Valid: true, Errors: []string{}}, nil
}

func (s *NativeService) compositionErrors(ctx context.Context, root string, revision int, definition nativeDefinition, ancestors map[string]bool) []string {
	var problems []string
	if root != "" {
		identity := root + "\x00" + strconv.Itoa(revision)
		ancestors[identity] = true
		defer delete(ancestors, identity)
	}
	for _, composition := range definition.Composition {
		identity := composition.Formula + "\x00" + strconv.Itoa(composition.Revision)
		if ancestors[identity] {
			problems = append(problems, fmt.Sprintf("composition %s creates a composition cycle", composition.Key))
			continue
		}
		source, err := s.compositionSource(ctx, composition.Formula, composition.Revision)
		if err != nil {
			problems = append(problems, fmt.Sprintf("composition %s references missing Formula revision %s@%d", composition.Key, composition.Formula, composition.Revision))
			continue
		}
		child, err := compileNativeFormula(source)
		if err != nil {
			problems = append(problems, fmt.Sprintf("composition %s references invalid Formula revision %s@%d", composition.Key, composition.Formula, composition.Revision))
			continue
		}
		for _, input := range child.Inputs {
			parent, ok := composition.Bindings[input]
			if !ok {
				problems = append(problems, fmt.Sprintf("composition %s is missing binding for %s", composition.Key, input))
			} else if !containsString(definition.Inputs, parent) {
				problems = append(problems, fmt.Sprintf("composition %s binding %s is unresolved", composition.Key, input))
			}
		}
		for input := range composition.Bindings {
			if !containsString(child.Inputs, input) {
				problems = append(problems, fmt.Sprintf("composition %s binding %s is unresolved", composition.Key, input))
			}
		}
		problems = append(problems, s.compositionErrors(ctx, composition.Formula, composition.Revision, child, ancestors)...)
	}
	sort.Strings(problems)
	return problems
}

func (s *NativeService) compositionSource(ctx context.Context, id string, revision int) (string, error) {
	if id == "ocman/tracer" && revision == 1 {
		return tracerFormulaSource, nil
	}
	store, ok := s.store.(nativeFormulaStore)
	if !ok {
		return "", ErrFormulaNotFound
	}
	formula, err := store.GetNativeFactoryFormulaRevision(ctx, id, revision)
	if err != nil {
		return "", err
	}
	return formula.SourceTOML, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (s *NativeService) GetWorkEpic(ctx context.Context, id string) (WorkEpic, error) {
	epic, err := s.store.GetFactoryEpic(ctx, id)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	result := nativeEpic(epic)
	if err != nil {
		return result, err
	}
	if store, ok := s.store.(nativePlanningStore); ok {
		if attempts, attemptsErr := store.ListFactoryAttempts(ctx, id); attemptsErr == nil {
			result.Attempts = attempts
		}
		if proposal, proposalErr := store.GetFactoryProposalRevision(ctx, id, 0); proposalErr == nil {
			if decoded, decodeErr := nativeProposal(proposal); decodeErr == nil {
				result.Proposal = &decoded
			}
		}
		if gate, gateErr := store.GetFactoryPlanGate(ctx, id); gateErr == nil {
			decoded := nativePlanGate(gate)
			result.PlanGate = &decoded
		}
	}
	issues, issuesErr := s.store.ListFactoryIssues(ctx, id)
	if issuesErr != nil {
		return result, issuesErr
	}
	result.Progress = factoryProgress(issues)
	result.Progress.Stuck = result.Progress.Stuck && result.Status == "open"
	return result, nil
}

func (s *NativeService) DecidePlanGate(ctx context.Context, epicID, action string, req PlanGateDecisionRequest) (PlanGate, error) {
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return PlanGate{}, ErrFactoryUnavailable
	}
	if req.ExpectedRevision < 1 || strings.TrimSpace(req.ExpectedHash) == "" {
		return PlanGate{}, errors.New("plan revision and hash are required")
	}
	gate, err := store.DecideFactoryPlanGate(ctx, epicID, action, req.ExpectedRevision, req.ExpectedHash, strings.TrimSpace(req.Feedback))
	if errors.Is(err, sql.ErrNoRows) {
		return PlanGate{}, errors.New("factory Plan gate is unavailable")
	}
	return nativePlanGate(gate), err
}

// Materialize creates implementation work without launching an agent session.
func (s *NativeService) Materialize(ctx context.Context, epicID, issueID string) (Materialization, error) {
	s.materializationMu.Lock()
	defer s.materializationMu.Unlock()
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return Materialization{}, ErrFactoryUnavailable
	}
	materialization, err := store.MaterializeFactoryPlan(ctx, epicID, issueID, "factory-materialize/v1", time.Now())
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	if err == nil {
		_ = s.Dispatch(ctx)
	}
	return Materialization{ID: materialization.ID, IssueID: materialization.IssueID, ProposalRevision: materialization.ProposalRevision, ProposalHash: materialization.ProposalHash, ManifestKey: materialization.ManifestKey, ImplementationID: materialization.ImplementationID}, err
}

func (s *NativeService) DeferIssue(ctx context.Context, epicID, issueID, reason string) error {
	store, ok := s.store.(nativeDelayStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	return store.DeferFactoryIssue(ctx, epicID, issueID, strings.TrimSpace(reason))
}

func (s *NativeService) ResumeIssue(ctx context.Context, epicID, issueID string) error {
	store, ok := s.store.(nativeDelayStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	return store.ResumeFactoryIssue(ctx, epicID, issueID)
}

func (s *NativeService) RetryIssueAt(ctx context.Context, epicID, issueID string, wakeAt time.Time) error {
	store, ok := s.store.(nativeDelayStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	if !wakeAt.After(time.Now()) {
		return errors.New("retry time must be in the future")
	}
	return store.RetryFactoryIssueAt(ctx, epicID, issueID, wakeAt)
}

func (s *NativeService) CreateRecoveryGate(ctx context.Context, attemptID, agentToken, question, reason string, choices []string) (RecoveryGate, error) {
	store, ok := s.store.(nativeRecoveryStore)
	if !ok {
		return RecoveryGate{}, ErrFactoryUnavailable
	}
	if strings.TrimSpace(attemptID) == "" || strings.TrimSpace(agentToken) == "" || strings.TrimSpace(question) == "" || strings.TrimSpace(reason) == "" {
		return RecoveryGate{}, errors.New("attempt, token, question, and reason are required")
	}
	tokens, ok := s.store.(nativeAttemptCompletionStore)
	if !ok {
		return RecoveryGate{}, ErrFactoryUnavailable
	}
	valid, err := tokens.ValidateFactoryAttemptToken(ctx, attemptID, agentToken)
	if err != nil || !valid {
		return RecoveryGate{}, errors.New("factory implementation attempt token is invalid")
	}
	return store.CreateFactoryRecoveryGate(ctx, attemptID, strings.TrimSpace(question), strings.TrimSpace(reason), choices, time.Now())
}

func (s *NativeService) ResolveRecoveryGate(ctx context.Context, gateID, action, response string) (RecoveryGate, error) {
	store, ok := s.store.(nativeRecoveryStore)
	if !ok {
		return RecoveryGate{}, ErrFactoryUnavailable
	}
	if action != "resume" && action != "retry" && action != "cancel" {
		return RecoveryGate{}, errors.New("invalid recovery gate action")
	}
	gate, attempt, err := store.ResolveFactoryRecoveryGate(ctx, gateID, action, strings.TrimSpace(response), time.Now())
	if err != nil {
		return RecoveryGate{}, err
	}
	if (action == "retry" || action == "cancel") && s.implementation != nil && attempt.Session.ID != "" {
		_ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session)
	}
	if action == "retry" {
		_ = s.Dispatch(ctx)
	}
	return gate, nil
}

// IsImplementationSession cheaply identifies sessions whose out-of-profile
// permissions need Factory authority handling.
func (s *NativeService) IsImplementationSession(ctx context.Context, session string) (bool, error) {
	store, ok := s.store.(nativeAuthorityStore)
	if !ok {
		return false, nil
	}
	return store.IsFactoryImplementationSession(ctx, session)
}

// EscalatePermission diverts only requests excluded by the frozen profile.
// Other prompts continue through OpenCode's direct permission flow.
func (s *NativeService) EscalatePermission(ctx context.Context, session, requestID, permission, target string) (AuthorityEscalationGate, bool, error) {
	store, ok := s.store.(nativeAuthorityStore)
	if !ok {
		return AuthorityEscalationGate{}, false, ErrFactoryUnavailable
	}
	if session == "" || requestID == "" || permission == "" {
		return AuthorityEscalationGate{}, false, errors.New("session, request, and permission are required")
	}
	return store.CreateFactoryAuthorityEscalationGate(ctx, session, requestID, permission, target, time.Now())
}

func (s *NativeService) ResolveAuthorityEscalationGate(ctx context.Context, gateID, action string) (AuthorityEscalationGate, error) {
	s.authorityMu.Lock()
	defer s.authorityMu.Unlock()
	store, ok := s.store.(nativeAuthorityStore)
	if !ok {
		return AuthorityEscalationGate{}, ErrFactoryUnavailable
	}
	if action != "approve" && action != "reject" {
		return AuthorityEscalationGate{}, errors.New("invalid authority escalation action")
	}
	if s.implementation == nil {
		return AuthorityEscalationGate{}, errors.New("implementation launcher is unavailable")
	}
	gate, found, err := store.GetFactoryAuthorityEscalationGate(ctx, gateID)
	pending := action + "_pending"
	if err != nil || !found || (gate.Resolution != "open" && gate.Resolution != pending) {
		return AuthorityEscalationGate{}, errors.New("authority escalation gate is unavailable")
	}
	attempt, found, err := store.GetFactoryAttempt(ctx, gate.AttemptID)
	if err != nil || !found || attempt.Phase != model.FactoryAttemptActive {
		return AuthorityEscalationGate{}, errors.New("authority escalation attempt is unavailable")
	}
	reply := "reject"
	if action == "approve" {
		reply = "once"
	}
	if gate.Resolution == "open" {
		gate, _, err = store.ResolveFactoryAuthorityEscalationGate(ctx, gateID, action, time.Now())
		if err != nil {
			return AuthorityEscalationGate{}, err
		}
	}
	// Persist the decision before delivery; same-action retries only redeliver it.
	promptPending, err := s.implementation.ImplementationPermissionPending(ctx, attempt.Session, gate.RequestID)
	if err != nil {
		return AuthorityEscalationGate{}, err
	}
	if promptPending {
		if err := s.implementation.RespondImplementationPermission(context.WithoutCancel(ctx), attempt.Session, gate.RequestID, reply); err != nil {
			return AuthorityEscalationGate{}, err
		}
	}
	return store.CompleteFactoryAuthorityEscalationGate(ctx, gateID, action, time.Now())
}

// Dispatch admits ready executable Issues in deterministic order until
// configured capacity is full. Plan and Materialization never enter this path.
func (s *NativeService) Dispatch(ctx context.Context) error {
	store, ok := s.store.(nativePlanningStore)
	if !ok || s.implementation == nil {
		return nil
	}
	if err := s.reconcileImplementationSessions(ctx, store); err != nil {
		return err
	}
	if delays, ok := s.store.(nativeDelayStore); ok {
		if err := delays.WakeFactoryRetries(ctx, time.Now()); err != nil {
			return err
		}
	}
	type candidate struct {
		issue model.NativeIssue
		epic  model.NativeEpic
	}
	var ready []candidate
	epics, err := s.store.ListFactoryEpics(ctx)
	if err != nil {
		return err
	}
	for _, epic := range epics {
		issues, err := s.store.ListFactoryIssues(ctx, epic.ID)
		if err != nil {
			return err
		}
		for _, issue := range issues {
			if (issue.Kind == "implementation" || issue.Kind == "task") && issue.DispatchState == "ready" {
				ready = append(ready, candidate{issue, epic})
			}
		}
	}
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].issue.CreatedAt != ready[j].issue.CreatedAt {
			return ready[i].issue.CreatedAt < ready[j].issue.CreatedAt
		}
		return ready[i].issue.ID < ready[j].issue.ID
	})
	for _, next := range ready {
		epic, attempt, err := store.ClaimFactoryImplementation(ctx, next.epic.ID, next.issue.ID, "factory-implement/v1", time.Now())
		if err != nil {
			continue
		} // A saturated project must not stall other projects.
		request := ImplementationSessionRequest{EpicID: epic.ID, WorkID: next.issue.ID, AttemptID: attempt.ID, AgentToken: attempt.AgentToken, Repository: epic.InitialProject, Title: next.issue.Title, Description: next.issue.Description, Branch: "factory/" + epic.ID, Profile: "factory-implement/v1"}
		session, launchErr := s.implementation.LaunchImplementationSession(ctx, request)
		if launchErr != nil {
			if session.ID != "" {
				_ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), session)
			}
			_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "launch_failed", Message: "Implementation Session could not be launched"}, time.Now())
			continue
		}
		if session.ID == "" || session.Platform == "" {
			_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "launch_failed", Message: "Implementation Session returned no session"}, time.Now())
			continue
		}
		if activated, err := store.ActivateFactoryAttempt(ctx, attempt.ID, session, time.Now()); err != nil || !activated {
			_ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), session)
			_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "activation_failed", Message: "Implementation Session could not be recorded"}, time.Now())
			continue
		}
		if err := s.implementation.PromptImplementationSession(ctx, session, request); err != nil {
			_ = s.implementation.StopImplementationSession(context.WithoutCancel(ctx), session)
			_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "prompt_failed", Message: "Implementation Session could not be prompted"}, time.Now())
		}
	}
	return nil
}

func (s *NativeService) reconcileImplementationSessions(ctx context.Context, store nativePlanningStore) error {
	s.implementationMu.Lock()
	defer s.implementationMu.Unlock()
	attempts, err := store.ListFactoryAttempts(ctx, "")
	if err != nil {
		return err
	}
	for _, attempt := range attempts {
		if attempt.Phase == model.FactoryAttemptStopping && attempt.FrozenPolicy.Profile == "factory-implement/v1" {
			if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
				continue
			}
			if _, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "handoff_finalize_failed", Message: "Implementation handoff did not finish recording"}, time.Now()); err != nil {
				return fmt.Errorf("recover stopping Implementation attempt: %w", err)
			}
			continue
		}
		if attempt.Phase != model.FactoryAttemptActive || attempt.FrozenPolicy.Profile != "factory-implement/v1" || attempt.Session.ID == "" {
			continue
		}
		if recovery, ok := s.store.(nativeRecoveryStore); ok {
			paused, err := recovery.IsFactoryAttemptRecoveryPaused(ctx, attempt.ID)
			if err != nil {
				return err
			}
			if paused {
				continue
			}
		}
		alive, probeErr := s.implementation.ProbeImplementationSession(ctx, attempt.Session)
		if probeErr != nil || alive {
			continue
		}
		if _, err := store.FailFactoryAttempt(ctx, attempt.ID, model.FactoryAttemptFailure{Type: "interrupted_runtime", Message: "Implementation Session is no longer available"}, time.Now()); err != nil {
			return fmt.Errorf("recover Implementation attempt: %w", err)
		}
	}
	return nil
}

func (s *NativeService) Queue(ctx context.Context) ([]DispatchItem, error) {
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return nil, ErrFactoryUnavailable
	}
	epics, err := s.store.ListFactoryEpics(ctx)
	if err != nil {
		return nil, err
	}
	attempts, err := store.ListFactoryAttempts(ctx, "")
	if err != nil {
		return nil, err
	}
	byWork := map[string]model.FactoryAttempt{}
	for _, attempt := range attempts {
		if current, ok := byWork[attempt.WorkID]; !ok || current.Sequence < attempt.Sequence {
			byWork[attempt.WorkID] = attempt
		}
	}
	var queue []DispatchItem
	for _, epic := range epics {
		issues, err := s.store.ListFactoryIssues(ctx, epic.ID)
		if err != nil {
			return nil, err
		}
		for _, issue := range issues {
			if issue.Kind != "implementation" && issue.Kind != "task" {
				continue
			}
			item := DispatchItem{ID: issue.ID, EpicID: epic.ID, Title: issue.Title, Repository: epic.InitialProject, OutcomeReason: issue.OutcomeReason, Blockers: issue.Blockers, RetryAt: issue.RetryAt, RetryAttempts: issue.RetryAttempts}
			if attempt, ok := byWork[issue.ID]; ok {
				item.AttemptID, item.Session, item.Outcome = attempt.ID, attempt.Session, string(attempt.Outcome)
				if attempt.Phase == model.FactoryAttemptTerminal {
					if issue.DispatchState == string(DispatchRetryWait) || issue.DispatchState == string(DispatchReady) {
						item.State = DispatchState(issue.DispatchState)
					} else {
						item.State = DispatchCompleted
					}
				} else {
					item.State = DispatchRunning
				}
			} else if issue.DispatchState != "" {
				item.State = DispatchState(issue.DispatchState)
			} else {
				continue
			}
			queue = append(queue, item)
		}
	}
	sort.Slice(queue, func(i, j int) bool {
		if queue[i].State != queue[j].State {
			return queue[i].State == DispatchRunning
		}
		return queue[i].ID < queue[j].ID
	})
	return queue, nil
}

func (s *NativeService) CompleteAttempt(ctx context.Context, attemptID, agentToken, summary, prURL string) error {
	s.implementationMu.Lock()
	defer s.implementationMu.Unlock()
	store, ok := s.store.(nativeAttemptCompletionStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	summary, prURL = strings.TrimSpace(summary), strings.TrimSpace(prURL)
	parsedPR, parseErr := url.ParseRequestURI(prURL)
	if attemptID == "" || agentToken == "" || summary == "" || parseErr != nil || (parsedPR.Scheme != "http" && parsedPR.Scheme != "https") || parsedPR.Host == "" {
		return errors.New("attempt ID, token, summary, and pull request URL are required")
	}
	valid, err := store.ValidateFactoryAttemptToken(ctx, attemptID, agentToken)
	if err != nil || !valid {
		return errors.New("factory implementation attempt is not active")
	}
	attemptStore, ok := s.store.(interface {
		GetFactoryAttempt(context.Context, string) (model.FactoryAttempt, bool, error)
	})
	if !ok || s.implementation == nil {
		return ErrFactoryUnavailable
	}
	attempt, found, err := attemptStore.GetFactoryAttempt(ctx, attemptID)
	if err != nil || !found {
		return errors.New("factory implementation attempt is not active")
	}
	if attempt.Phase == model.FactoryAttemptTerminal && attempt.Outcome == model.FactoryAttemptSucceeded {
		if attempt.Result != nil && attempt.Result.Summary == summary && attempt.Result.PRURL == prURL {
			return nil
		}
		return errors.New("factory implementation attempt already completed with a different result")
	}
	existingPR, err := store.FactoryEpicPRURL(ctx, attempt.EpicID)
	if err != nil {
		return err
	}
	if existingPR != "" && existingPR != prURL {
		return errors.New("factory epic already uses a different pull request")
	}
	if err := s.implementation.ValidateImplementationHandoff(ctx, attempt.FrozenPolicy.Repository, "factory/"+attempt.EpicID, prURL, attempt.FrozenPolicy); err != nil {
		return err
	}
	stopping, err := store.StopFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, time.Now())
	if err != nil || !stopping {
		return errors.New("factory implementation attempt is not active")
	}
	if err := s.implementation.StopImplementationSession(context.WithoutCancel(ctx), attempt.Session); err != nil {
		return fmt.Errorf("stop completed Factory session: %w", err)
	}
	if err := s.implementation.ValidateImplementationHandoff(context.WithoutCancel(ctx), attempt.FrozenPolicy.Repository, "factory/"+attempt.EpicID, prURL, attempt.FrozenPolicy); err != nil {
		return err
	}
	result := model.FactoryAttemptResult{SchemaVersion: 1, Summary: summary, PRURL: prURL}
	changed, err := store.CompleteFactoryImplementationAttempt(context.WithoutCancel(ctx), attemptID, agentToken, result, time.Now())
	if err != nil {
		return err
	}
	if !changed {
		completed, found, getErr := attemptStore.GetFactoryAttempt(context.WithoutCancel(ctx), attemptID)
		if getErr == nil && found && completed.Phase == model.FactoryAttemptTerminal && completed.Outcome == model.FactoryAttemptSucceeded && completed.Result != nil && *completed.Result == result {
			return nil
		}
		return errors.New("factory implementation attempt is not active")
	}
	select {
	case s.dispatchWake <- struct{}{}:
	default:
	}
	return nil
}

func nativePlanGate(gate model.NativePlanGate) PlanGate {
	return PlanGate{IssueID: gate.IssueID, ProposalRevision: gate.ProposalRevision, ProposalHash: gate.ProposalHash, Outcome: gate.Outcome, Resolution: gate.Resolution, Feedback: gate.Feedback, ReviewIssueIDs: gate.ReviewIssueIDs}
}

// ClaimPlan records a prepared Attempt before exposing the bounded Planning Session.
func (s *NativeService) ClaimPlan(ctx context.Context, epicID, issueID string) (ClaimedPlan, error) {
	s.planningMu.Lock()
	defer s.planningMu.Unlock()
	store, ok := s.store.(nativePlanningStore)
	if !ok || s.planning == nil {
		return ClaimedPlan{}, ErrFactoryUnavailable
	}
	epic, attempt, err := store.ClaimFactoryPlan(ctx, epicID, issueID, planningProfile, time.Now())
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		return ClaimedPlan{}, ErrWorkEpicNotFound
	}
	if err != nil {
		return ClaimedPlan{}, err
	}
	request := PlanningSessionRequest{EpicID: epic.ID, WorkID: issueID, AttemptID: attempt.ID, AgentToken: attempt.AgentToken, Repository: epic.InitialProject, Title: "plan " + issueID + " (@factory)"}
	session, launchErr := s.planning.LaunchPlanningSession(ctx, request)
	if launchErr != nil {
		if session.ID != "" {
			_ = s.planning.StopPlanningSession(context.WithoutCancel(ctx), session)
		}
		_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "launch_failed", Message: "Planning Session could not be launched"}, time.Now())
		return ClaimedPlan{}, ErrFactoryUnavailable
	}
	if activated, err := store.ActivateFactoryAttempt(ctx, attempt.ID, session, time.Now()); err != nil || !activated {
		_ = s.planning.StopPlanningSession(context.WithoutCancel(ctx), session)
		_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "activation_failed", Message: "Planning Session could not be recorded"}, time.Now())
		if err == nil {
			err = errors.New("factory attempt was no longer prepared")
		}
		return ClaimedPlan{}, fmt.Errorf("recording Planning Session: %w", err)
	}
	if err := s.planning.PromptPlanningSession(ctx, session, request); err != nil {
		_ = s.planning.StopPlanningSession(context.WithoutCancel(ctx), session)
		_, _ = store.FailFactoryAttempt(context.WithoutCancel(ctx), attempt.ID, model.FactoryAttemptFailure{Type: "prompt_failed", Message: "Planning Session could not be prompted"}, time.Now())
		return ClaimedPlan{}, ErrFactoryUnavailable
	}
	attempt.Phase, attempt.Session = model.FactoryAttemptActive, session
	return ClaimedPlan{Attempt: attempt, Session: session}, nil
}

func (s *NativeService) SubmitProposal(ctx context.Context, req SubmitProposalRequest) (ProposalRevision, error) {
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return ProposalRevision{}, ErrFactoryUnavailable
	}
	epic, err := s.store.GetFactoryEpic(ctx, req.EpicID)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		return ProposalRevision{}, ErrWorkEpicNotFound
	}
	if err != nil {
		return ProposalRevision{}, err
	}
	if (req.AttemptID == "") != (req.AttemptToken == "") {
		return ProposalRevision{}, errors.New("attempt ID and token are required")
	}
	issues, err := s.store.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		return ProposalRevision{}, err
	}
	rootMolID := ""
	for _, issue := range issues {
		if issue.Kind == "mol" && issue.ParentID == "" {
			rootMolID = issue.ID
			break
		}
	}
	if err := validateProposalManifest(req.Manifest, epic, rootMolID); err != nil {
		return ProposalRevision{}, err
	}
	manifestJSON, err := json.Marshal(req.Manifest)
	if err != nil {
		return ProposalRevision{}, fmt.Errorf("encoding proposal manifest: %w", err)
	}
	content, err := json.Marshal(struct {
		Manifest  json.RawMessage `json:"manifest"`
		Rationale string          `json:"rationaleMarkdown"`
	}{manifestJSON, req.RationaleMarkdown})
	if err != nil {
		return ProposalRevision{}, fmt.Errorf("encoding proposal: %w", err)
	}
	hash := sha256.Sum256(content)
	proposal := model.NativeProposalRevision{EpicID: req.EpicID, MolID: req.Manifest.MolID, Project: req.Manifest.Project, ManifestJSON: string(manifestJSON), RationaleMarkdown: req.RationaleMarkdown, ContentHash: hex.EncodeToString(hash[:])}
	var saved model.NativeProposalRevision
	if req.AttemptID != "" {
		var authorized bool
		saved, authorized, err = store.SaveFactoryProposalRevisionForAttempt(ctx, proposal, req.AttemptID, req.AttemptToken)
		if err == nil && !authorized {
			return ProposalRevision{}, ErrActionNotPermitted
		}
	} else {
		saved, err = store.SaveFactoryProposalRevision(ctx, proposal)
	}
	if err != nil {
		return ProposalRevision{}, err
	}
	return nativeProposal(saved)
}

func (s *NativeService) GetProposal(ctx context.Context, epicID string, revision int) (ProposalRevision, error) {
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return ProposalRevision{}, ErrFactoryUnavailable
	}
	proposal, err := store.GetFactoryProposalRevision(ctx, epicID, revision)
	if err != nil {
		return ProposalRevision{}, err
	}
	return nativeProposal(proposal)
}

func (s *NativeService) ListProposals(ctx context.Context, epicID string) ([]ProposalRevision, error) {
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return nil, ErrFactoryUnavailable
	}
	if _, err := s.GetWorkEpic(ctx, epicID); err != nil {
		return nil, err
	}
	proposals, err := store.ListFactoryProposalRevisions(ctx, epicID)
	if err != nil {
		return nil, err
	}
	result := make([]ProposalRevision, 0, len(proposals))
	for _, proposal := range proposals {
		decoded, err := nativeProposal(proposal)
		if err != nil {
			return nil, err
		}
		result = append(result, decoded)
	}
	return result, nil
}

func validateProposalManifest(manifest ProposalManifest, epic model.NativeEpic, rootMolID string) error {
	if manifest.EpicID != epic.ID || manifest.MolID != rootMolID || manifest.Project != epic.InitialProject {
		return errors.New("proposal manifest scope does not match Epic")
	}
	keys, implementations := map[string]bool{}, 0
	for _, node := range manifest.Nodes {
		if !model.ValidNativeFormulaKey(node.Key) || keys[node.Key] {
			return errors.New("proposal manifest keys must be unique and stable")
		}
		keys[node.Key] = true
		if node.Requirement != "required" && node.Requirement != "optional" && node.Requirement != "reference" {
			return errors.New("proposal manifest requirement class is invalid")
		}
		if node.Pinned && node.Requirement != "reference" {
			return errors.New("only reference proposal nodes may be pinned")
		}
		if node.Type == "implementation" && node.Requirement == "required" {
			implementations++
		}
	}
	if implementations != 1 {
		return errors.New("proposal manifest requires exactly one required implementation node")
	}
	return nil
}

func nativeProposal(proposal model.NativeProposalRevision) (ProposalRevision, error) {
	var manifest ProposalManifest
	if err := json.Unmarshal([]byte(proposal.ManifestJSON), &manifest); err != nil {
		return ProposalRevision{}, fmt.Errorf("decoding proposal manifest: %w", err)
	}
	return ProposalRevision{EpicID: proposal.EpicID, MolID: proposal.MolID, Project: proposal.Project, Revision: proposal.Revision, Manifest: manifest, RationaleMarkdown: proposal.RationaleMarkdown, ContentHash: proposal.ContentHash, CreatedAt: proposal.CreatedAt}, nil
}

func (s *NativeService) Pour(ctx context.Context, id string) ([]Issue, error) {
	epic, err := s.store.GetFactoryEpic(ctx, id)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		return nil, ErrWorkEpicNotFound
	}
	if err != nil {
		return nil, err
	}
	formula, err := s.nativeFormula(ctx, epic.FormulaID, epic.FormulaVersion)
	if err != nil {
		return nil, err
	}
	_, issues, err := s.store.PourFactoryEpic(ctx, id, formula)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	return nativeIssues(issues), err
}

func (s *NativeService) nativeFormula(ctx context.Context, id string, version int) (model.NativeFormula, error) {
	view, err := s.GetFormula(ctx, id, version)
	if err != nil {
		return model.NativeFormula{}, err
	}
	if !view.Valid {
		return model.NativeFormula{}, fmt.Errorf("%w: %s", ErrInvalidFormula, strings.Join(view.Errors, "; "))
	}
	definition, err := compileNativeFormula(view.Source)
	if err != nil {
		return model.NativeFormula{}, fmt.Errorf("%w: Formula is invalid", ErrFactoryUnavailable)
	}
	formula := model.NativeFormula{ID: view.ID, Version: view.Version, Source: view.Source, Hash: view.Hash, Inputs: definition.Inputs}
	if id == "ocman/tracer" && version == 1 {
		formula.Hash = BuiltInTracerFormula().Hash
	}
	for _, node := range definition.Nodes {
		formula.Nodes = append(formula.Nodes, model.NativeFormulaNode{Key: node.Key, Kind: node.Kind})
	}
	for _, edge := range definition.Edges {
		formula.Edges = append(formula.Edges, model.NativeFormulaEdge{From: edge.From, To: edge.To, Type: edge.Type})
	}
	for _, composition := range definition.Composition {
		child, err := s.nativeFormula(ctx, composition.Formula, composition.Revision)
		if err != nil {
			return model.NativeFormula{}, err
		}
		formula.Composition = append(formula.Composition, model.NativeFormulaComposition{Key: composition.Key, Requirement: composition.Requirement, Bindings: composition.Bindings, Formula: child})
	}
	return formula, nil
}

func (s *NativeService) ListIssues(ctx context.Context, epicID string) ([]Issue, error) {
	issues, err := s.store.ListFactoryIssues(ctx, epicID)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	result := nativeIssues(issues)
	if store, ok := s.store.(nativePlanningStore); ok && err == nil {
		attempts, attemptErr := store.ListFactoryAttempts(ctx, epicID)
		if attemptErr != nil {
			return nil, attemptErr
		}
		latest := map[string]model.FactoryAttempt{}
		for _, attempt := range attempts {
			if current, exists := latest[attempt.WorkID]; !exists || current.Sequence < attempt.Sequence {
				latest[attempt.WorkID] = attempt
			}
		}
		for i := range result {
			if attempt, exists := latest[result[i].ID]; exists {
				result[i].AttemptID, result[i].Session = attempt.ID, attempt.Session
				if attempt.Result != nil {
					result[i].Conclusion = attempt.Result.Summary
					result[i].PRURL = attempt.Result.PRURL
				}
			}
			if recovery, ok := s.store.(nativeRecoveryStore); ok {
				gate, found, recoveryErr := recovery.GetFactoryRecoveryGate(ctx, result[i].ID)
				if recoveryErr != nil {
					return nil, recoveryErr
				}
				if found {
					result[i].Recovery = &gate
				}
			}
			if authority, ok := s.store.(nativeAuthorityStore); ok {
				gate, found, authorityErr := authority.GetFactoryAuthorityEscalationGate(ctx, result[i].ID)
				if authorityErr != nil {
					return nil, authorityErr
				}
				if found {
					result[i].Authority = &gate
				}
			}
		}
	}
	return result, err
}

func (s *NativeService) ListIssueComments(ctx context.Context, epicID, issueID string) ([]IssueComment, error) {
	store, ok := s.store.(nativeCommentStore)
	if !ok {
		return nil, ErrFactoryUnavailable
	}
	comments, err := store.ListFactoryIssueComments(ctx, epicID, issueID)
	if errors.Is(err, model.ErrInvalidGraphMutation) {
		err = fmt.Errorf("%w: issue not found", ErrInvalidRequest)
	}
	return comments, err
}

func (s *NativeService) AddIssueComment(ctx context.Context, epicID, issueID, actor, body string) (IssueComment, error) {
	store, ok := s.store.(nativeCommentStore)
	if !ok {
		return IssueComment{}, ErrFactoryUnavailable
	}
	comment, err := store.AppendFactoryIssueComment(ctx, epicID, issueID, actor, body, time.Now())
	if errors.Is(err, model.ErrInvalidGraphMutation) {
		err = fmt.Errorf("%w: issue and comment body are required", ErrInvalidRequest)
	}
	return comment, err
}

func (s *NativeService) ListRemovedIssues(ctx context.Context, epicID string) ([]Issue, error) {
	store, ok := s.store.(interface {
		ListRemovedFactoryIssues(context.Context, string) ([]model.NativeIssue, error)
	})
	if !ok {
		return nil, ErrFactoryUnavailable
	}
	issues, err := store.ListRemovedFactoryIssues(ctx, epicID)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	return nativeIssues(issues), err
}

func nativeEpic(epic model.NativeEpic) WorkEpic {
	origin := FormulaOriginCustom
	if epic.FormulaID == BuiltInTracerFormula().ID {
		origin = FormulaOriginBuiltIn
	}
	return WorkEpic{ID: epic.ID, Status: epic.Status, Goal: epic.Goal, Brief: epic.Brief, InitialProject: epic.InitialProject, InstantiationID: epic.InstantiationID, FormulaID: epic.FormulaID, FormulaVersion: epic.FormulaVersion, FormulaRevision: epic.FormulaVersion, FormulaHash: epic.FormulaHash, FormulaOrigin: origin}
}
func nativeEpics(epics []model.NativeEpic) []WorkEpic {
	out := make([]WorkEpic, len(epics))
	for i := range epics {
		out[i] = nativeEpic(epics[i])
	}
	return out
}
func nativeIssues(issues []model.NativeIssue) []Issue {
	out := make([]Issue, len(issues))
	for i := range issues {
		issue := issues[i]
		out[i] = Issue{ID: issue.ID, EpicID: issue.EpicID, ParentID: issue.ParentID, Requirement: issue.Requirement, FormulaID: issue.FormulaID, FormulaVersion: issue.FormulaVersion, FormulaHash: issue.FormulaHash, Bindings: issue.Bindings, Kind: issue.Kind, Title: issue.Title, Status: issue.Status, Description: issue.Description, PlanRevision: issue.PlanRevision, ManifestKey: issue.ManifestKey, Outcome: issue.Outcome, OutcomeReason: issue.OutcomeReason, DispatchState: issue.DispatchState, Blockers: issue.Blockers, RetryAt: issue.RetryAt, RetryAttempts: issue.RetryAttempts, RemovedAt: issue.RemovedAt}
	}
	return out
}
