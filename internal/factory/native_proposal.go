package factory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type ManifestNode struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Requirement string   `json:"requirement"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Project     string   `json:"project,omitempty"`
	DependsOn   []string `json:"dependsOn,omitempty"`
	Pinned      bool     `json:"pinned,omitempty"`
}

type ManifestEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type ProposalManifest struct {
	EpicID  string         `json:"epicId"`
	MolID   string         `json:"molId"`
	Project string         `json:"project"`
	Nodes   []ManifestNode `json:"nodes"`
	Edges   []ManifestEdge `json:"edges,omitempty"`
}

type SubmitProposalRequest struct {
	EpicID            string           `json:"epicId"`
	Manifest          ProposalManifest `json:"manifest"`
	RationaleMarkdown string           `json:"rationaleMarkdown,omitempty"`
	// Import is set only by the explicit existing-plan import action. It
	// skips the planning attempt, never the approval gate.
	Import bool `json:"-"`
	// AttemptID/AttemptToken prove a Planning Session owns EpicID.
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
	ImplementationModel string   `json:"implementationModel,omitempty"`
	IssueID             string   `json:"issueId"`
	ProposalRevision    int      `json:"proposalRevision"`
	ProposalHash        string   `json:"proposalHash"`
	Outcome             string   `json:"outcome,omitempty"`
	Resolution          string   `json:"resolution"`
	Feedback            string   `json:"feedback,omitempty"`
	ReviewIssueIDs      []string `json:"reviewIssueIds,omitempty"`
}

type PlanGateDecisionRequest struct {
	ImplementationModel string `json:"implementationModel,omitempty"`
	ExpectedRevision    int    `json:"expectedRevision"`
	ExpectedHash        string `json:"expectedHash"`
	Actor               string `json:"actor,omitempty"`
	Feedback            string `json:"feedback,omitempty"`
}

type ClaimedPlan struct {
	Attempt model.FactoryAttempt  `json:"attempt"`
	Session model.PlanningSession `json:"session"`
}

type Materialization struct {
	ID               string                          `json:"id"`
	IssueID          string                          `json:"issueId"`
	ProposalRevision int                             `json:"proposalRevision"`
	ProposalHash     string                          `json:"proposalHash"`
	ManifestKey      string                          `json:"manifestKey"`
	ImplementationID string                          `json:"implementationId"`
	Issues           []model.NativeMaterializedIssue `json:"issues"`
}

func (s *NativeService) SubmitProposal(ctx context.Context, req SubmitProposalRequest) (ProposalRevision, error) {
	store, ok := s.store.(nativePlanningStore)
	if !ok {
		return ProposalRevision{}, ErrFactoryUnavailable
	}
	proposal, err := s.proposalForRequest(ctx, req)
	if err != nil {
		return ProposalRevision{}, err
	}
	if req.Import && req.AttemptID != "" {
		return ProposalRevision{}, fmt.Errorf("%w: imported plans cannot include attempt credentials", ErrInvalidRequest)
	}
	var saved model.NativeProposalRevision
	if req.Import {
		var authorized bool
		saved, authorized, err = store.ImportFactoryProposalRevision(ctx, proposal)
		if err == nil && !authorized {
			return ProposalRevision{}, fmt.Errorf("%w: plan import requires an open Epic with unclaimed planning work and an unresolved approval gate", ErrInvalidRequest)
		}
	} else if req.AttemptID != "" {
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

func (s *NativeService) SubmitScopePlan(ctx context.Context, req SubmitProposalRequest) (ProposalRevision, error) {
	store, ok := s.store.(nativeProjectRequestStore)
	if !ok || req.AttemptID == "" || req.AttemptToken == "" {
		return ProposalRevision{}, ErrActionNotPermitted
	}
	proposal, err := s.proposalForRequest(ctx, req)
	if err != nil {
		return ProposalRevision{}, err
	}
	for _, node := range req.Manifest.Nodes {
		if node.Requirement == "reference" || node.Type != "implementation" {
			return ProposalRevision{}, fmt.Errorf("%w: scope Plan can only add implementation work", ErrInvalidRequest)
		}
	}
	saved, err := store.ApplyFactoryScopePlan(ctx, proposal, req.AttemptID, req.AttemptToken, time.Now())
	if err != nil {
		return ProposalRevision{}, err
	}
	_ = s.Dispatch(ctx)
	return nativeProposal(saved)
}

func (s *NativeService) proposalForRequest(ctx context.Context, req SubmitProposalRequest) (model.NativeProposalRevision, error) {
	epic, err := s.store.GetFactoryEpic(ctx, req.EpicID)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		return model.NativeProposalRevision{}, ErrWorkEpicNotFound
	}
	if err != nil {
		return model.NativeProposalRevision{}, err
	}
	if (req.AttemptID == "") != (req.AttemptToken == "") {
		return model.NativeProposalRevision{}, fmt.Errorf("%w: attempt ID and token are required", ErrInvalidRequest)
	}
	issues, err := s.store.ListFactoryIssues(ctx, epic.ID)
	if err != nil {
		return model.NativeProposalRevision{}, err
	}
	rootMolID := ""
	for _, issue := range issues {
		if issue.Kind == "mol" && issue.ParentID == "" {
			rootMolID = issue.ID
			break
		}
	}
	if req.AttemptID != "" {
		if attempts, ok := s.store.(nativeAuthorityStore); ok {
			attempt, found, attemptErr := attempts.GetFactoryAttempt(ctx, req.AttemptID)
			if attemptErr != nil {
				return model.NativeProposalRevision{}, attemptErr
			}
			if found {
				if projectRequests, ok := s.store.(nativeProjectRequestStore); ok {
					if _, scopeExpansion, gateErr := projectRequests.GetFactoryProjectRequestGateForPlan(ctx, attempt.WorkID); gateErr != nil {
						return model.NativeProposalRevision{}, gateErr
					} else if scopeExpansion {
						for _, issue := range issues {
							if issue.ID == attempt.WorkID {
								rootMolID = issue.ParentID
								break
							}
						}
					}
				}
			}
		}
	}
	req.Manifest.Project, err = s.canonicalIssueProject(ctx, epic, req.Manifest.Project)
	if err != nil {
		return model.NativeProposalRevision{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	workflow := false
	for _, issue := range issues {
		workflow = workflow || issue.Workflow != nil
	}
	for i := range req.Manifest.Nodes {
		if workflow && req.Manifest.Nodes[i].Type == "delivery" {
			return model.NativeProposalRevision{}, fmt.Errorf("%w: workflow delivery belongs in the Formula, not the implementation plan", ErrInvalidRequest)
		}
		req.Manifest.Nodes[i].Project, err = s.canonicalIssueProject(ctx, epic, req.Manifest.Nodes[i].Project)
		if err != nil {
			return model.NativeProposalRevision{}, fmt.Errorf("%w: node %q: %w", ErrInvalidRequest, req.Manifest.Nodes[i].Key, err)
		}
	}
	if err := validateProposalManifest(req.Manifest, epic, rootMolID); err != nil {
		return model.NativeProposalRevision{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	manifestJSON, err := json.Marshal(req.Manifest)
	if err != nil {
		return model.NativeProposalRevision{}, fmt.Errorf("encoding proposal manifest: %w", err)
	}
	content, err := json.Marshal(struct {
		Manifest  json.RawMessage `json:"manifest"`
		Rationale string          `json:"rationaleMarkdown"`
	}{manifestJSON, req.RationaleMarkdown})
	if err != nil {
		return model.NativeProposalRevision{}, fmt.Errorf("encoding proposal: %w", err)
	}
	hash := sha256.Sum256(content)
	return model.NativeProposalRevision{EpicID: req.EpicID, MolID: req.Manifest.MolID, Project: req.Manifest.Project, ManifestJSON: string(manifestJSON), RationaleMarkdown: req.RationaleMarkdown, ContentHash: hex.EncodeToString(hash[:])}, nil
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
	keys, deliveries, deliveryKeys, implementations, required := map[string]ManifestNode{}, map[string]bool{}, map[string]string{}, map[string]bool{}, 0
	for _, node := range manifest.Nodes {
		if _, exists := keys[node.Key]; !model.ValidNativeFormulaKey(node.Key) || exists {
			return errors.New("proposal manifest keys must be unique and stable")
		}
		keys[node.Key] = node
		if node.Requirement != "required" && node.Requirement != "optional" && node.Requirement != "reference" {
			return errors.New("proposal manifest requirement class is invalid")
		}
		if node.Pinned && node.Requirement != "reference" {
			return errors.New("only reference proposal nodes may be pinned")
		}
		if node.Type != "implementation" && node.Type != "delivery" {
			return errors.New("proposal manifest node type is invalid")
		}
		if node.Type == "delivery" && (node.Requirement != "required" || node.Pinned || len(node.DependsOn) != 0) {
			return errors.New("proposal delivery placeholder is invalid")
		}
		if node.Type == "delivery" && deliveries[node.Project] {
			return errors.New("proposal has duplicate delivery placeholders for a project")
		}
		deliveries[node.Project] = node.Type == "delivery" || deliveries[node.Project]
		if node.Type == "delivery" {
			deliveryKeys[node.Project] = node.Key
		}
		if node.Type == "implementation" && node.Requirement == "required" {
			required++
		}
		if node.Type == "implementation" && node.Requirement != "reference" {
			implementations[node.Project] = true
		}
	}
	if required == 0 {
		return errors.New("proposal manifest requires at least one required implementation node")
	}
	for project, delivery := range deliveries {
		if delivery && !implementations[project] {
			return errors.New("proposal delivery placeholder requires implementation work in its project")
		}
	}
	edges := append([]ManifestEdge(nil), manifest.Edges...)
	for _, node := range manifest.Nodes {
		for _, dependency := range node.DependsOn {
			edges = append(edges, ManifestEdge{From: node.Key, To: dependency, Type: "blocks"})
		}
	}
	dependencies := make(map[string][]string, len(keys))
	seenEdges := map[string]bool{}
	for _, edge := range edges {
		from, fromExists := keys[edge.From]
		to, toExists := keys[edge.To]
		pair := edge.From + "\x00" + edge.To
		validType := edge.Type == "blocks" || edge.Type == "on_failure" || edge.Type == "merge_gated"
		validDelivery := edge.Type == "merge_gated" && to.Type == "delivery" && from.Type == "implementation" && from.Project != to.Project
		if !fromExists || !toExists || from.Requirement == "reference" || to.Requirement == "reference" || !validType || (edge.Type == "merge_gated" && !validDelivery) || (edge.Type != "merge_gated" && (from.Type == "delivery" || to.Type == "delivery")) || seenEdges[pair] {
			return errors.New("proposal manifest dependency is invalid")
		}
		seenEdges[pair] = true
		dependencies[edge.From] = append(dependencies[edge.From], edge.To)
	}
	for _, node := range manifest.Nodes {
		if node.Type == "implementation" && node.Requirement == "required" && deliveryKeys[node.Project] != "" {
			dependencies[deliveryKeys[node.Project]] = append(dependencies[deliveryKeys[node.Project]], node.Key)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(key string) error {
		if visiting[key] {
			return errors.New("proposal manifest dependencies must be acyclic")
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		for _, dependency := range dependencies[key] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[key], visited[key] = false, true
		return nil
	}
	for key := range keys {
		if err := visit(key); err != nil {
			return err
		}
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
