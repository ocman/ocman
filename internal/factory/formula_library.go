package factory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
	"gopkg.in/yaml.v3"
)

type FormulaOrigin string

const (
	FormulaOriginBuiltIn FormulaOrigin = "built-in"
	FormulaOriginCustom  FormulaOrigin = "custom"
)

var (
	ErrFormulaNotFound         = errors.New("formula not found")
	ErrInvalidFormula          = errors.New("invalid formula")
	ErrBuiltInFormulaImmutable = errors.New("built-in formula is immutable")
	ErrFormulaReferenced       = errors.New("formula revision is referenced")
	formulaIDPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	placeholderPattern         = regexp.MustCompile(`\$\{([^}]+)\}`)
	sha256Pattern              = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const defaultFormulaYAML = `schema: 1
name: Shipped delivery
parameters:
  goal:
    type: string
  initial_project:
    type: local-project
nodes:
  - key: planning
    kind: agent-work
    title: "Plan: ${goal}"
    description: "Plan delivery for ${initial_project}"
    profile: factory-plan/v1
    project_parameter: initial_project
  - key: approval
    kind: plan-approval
    title: Plan approval
  - key: delivery
    kind: delivery
    title: "Deliver: ${goal}"
    profile: factory-deliver/v1
    project_parameter: initial_project
  - key: provider-check
    kind: provider-check
    title: Exact provider checks
    project_parameter: initial_project
    exact_revision: true
  - key: human-merge
    kind: human-merge
    title: Human merge
    project_parameter: initial_project
edges:
  - from: approval
    to: planning
    type: blocks
  - from: delivery
    to: planning
    type: blocks
  - from: provider-check
    to: delivery
    type: blocks
  - from: human-merge
    to: provider-check
    type: blocks
`

type FormulaSummary struct {
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	Origin          FormulaOrigin        `json:"origin"`
	CurrentRevision int                  `json:"currentRevision"`
	ContentHash     string               `json:"contentHash"`
	Archived        bool                 `json:"archived"`
	Revisions       []FormulaRevisionRef `json:"revisions"`
}

type FormulaRevisionRef struct {
	Revision    int    `json:"revision"`
	ContentHash string `json:"contentHash"`
}

type FormulaRevision struct {
	FormulaSummary
	Revision       int    `json:"revision"`
	SchemaVersion  int    `json:"schemaVersion"`
	DefinitionYAML string `json:"definitionYaml"`
}

type FormulaDraft struct {
	SourceID       string        `json:"sourceId"`
	SourceRevision int           `json:"sourceRevision"`
	Origin         FormulaOrigin `json:"origin"`
	DefinitionYAML string        `json:"definitionYaml"`
}

type FormulaValidation struct {
	Valid       bool     `json:"valid"`
	Schema      int      `json:"schema"`
	ContentHash string   `json:"contentHash,omitempty"`
	Errors      []string `json:"errors"`
}

type FormulaPreview struct {
	Name        string        `json:"name"`
	FormulaHash string        `json:"formulaHash"`
	Nodes       []PreviewNode `json:"nodes"`
	Edges       []PreviewEdge `json:"edges"`
}

type PreviewNode struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Profile string `json:"profile,omitempty"`
	Project string `json:"project,omitempty"`
}

type PreviewEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type SaveFormulaRequest struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	DefinitionYAML string `json:"definitionYaml"`
}

type formulaDefinition struct {
	Schema     int                         `yaml:"schema"`
	Name       string                      `yaml:"name"`
	Parameters map[string]formulaParameter `yaml:"parameters"`
	Nodes      []formulaNode               `yaml:"nodes"`
	Edges      []formulaEdge               `yaml:"edges"`
}

type formulaParameter struct {
	Type string `yaml:"type"`
}

type formulaNode struct {
	Key              string `yaml:"key"`
	Kind             string `yaml:"kind"`
	Title            string `yaml:"title"`
	Description      string `yaml:"description,omitempty"`
	Profile          string `yaml:"profile,omitempty"`
	ProjectParameter string `yaml:"project_parameter,omitempty"`
	ExactRevision    bool   `yaml:"exact_revision,omitempty"`
}

type formulaEdge struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
	Type string `yaml:"type"`
}

func (s *Service) CopyFormula(ctx context.Context, id string, revision int) (FormulaDraft, error) {
	if id == DefaultFormulaID {
		if revision != 0 && revision != DefaultFormulaVersion {
			return FormulaDraft{}, ErrFormulaNotFound
		}
		return FormulaDraft{SourceID: id, SourceRevision: DefaultFormulaVersion, Origin: FormulaOriginBuiltIn, DefinitionYAML: defaultFormulaYAML}, nil
	}
	saved, err := s.GetFormulaRevision(ctx, id, revision)
	if err != nil {
		return FormulaDraft{}, err
	}
	return FormulaDraft{SourceID: id, SourceRevision: saved.Revision, Origin: FormulaOriginCustom, DefinitionYAML: saved.DefinitionYAML}, nil
}

func (s *Service) ValidateFormula(definitionYAML string) FormulaValidation {
	definition, normalized, validation := parseFormula(definitionYAML)
	if !validation.Valid {
		return validation
	}
	validation.Errors = validateFormulaPolicy(definition)
	validation.Valid = len(validation.Errors) == 0
	if validation.Valid {
		validation.ContentHash = formulaHash(normalized)
	}
	return validation
}

func parseFormula(definitionYAML string) (formulaDefinition, string, FormulaValidation) {
	normalized := strings.TrimSpace(definitionYAML) + "\n"
	decoder := yaml.NewDecoder(strings.NewReader(normalized))
	decoder.KnownFields(true)
	var definition formulaDefinition
	if err := decoder.Decode(&definition); err != nil {
		return definition, normalized, FormulaValidation{Errors: []string{err.Error()}}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return definition, normalized, FormulaValidation{Errors: []string{"definition must contain exactly one YAML document"}}
	}
	return definition, normalized, FormulaValidation{Valid: true, Schema: definition.Schema, Errors: []string{}}
}

func validateFormulaPolicy(definition formulaDefinition) []string {
	var problems []string
	if definition.Schema != 1 {
		problems = append(problems, "schema must be 1")
	}
	if strings.TrimSpace(definition.Name) == "" {
		problems = append(problems, "name is required")
	}
	wantParameters := map[string]string{"goal": "string", "initial_project": "local-project"}
	for name, parameter := range definition.Parameters {
		if wantParameters[name] == "" {
			problems = append(problems, "parameter "+name+" is not schema-approved")
		} else if parameter.Type != wantParameters[name] {
			problems = append(problems, fmt.Sprintf("parameter %s must have type %s", name, wantParameters[name]))
		}
	}
	for name := range wantParameters {
		if _, ok := definition.Parameters[name]; !ok {
			problems = append(problems, "parameter "+name+" is required")
		}
	}

	keys := make(map[string]formulaNode, len(definition.Nodes))
	if len(definition.Nodes) > 64 || len(definition.Edges) > 256 {
		problems = append(problems, "Formula graph exceeds the safety limit")
	}
	kindCount := make(map[string]int)
	kindKeys := make(map[string]string)
	deliveryProjects := make(map[string]bool)
	deliveryKeys := make(map[string]string)
	checkProjects := make(map[string]bool)
	checkKeys := make(map[string]string)
	mergeProjects := make(map[string]bool)
	mergeKeys := make(map[string]string)
	for _, node := range definition.Nodes {
		if !stableID.MatchString(node.Key) || keys[node.Key].Key != "" {
			problems = append(problems, "node keys must be unique stable identifiers")
			continue
		}
		keys[node.Key] = node
		kindCount[node.Kind]++
		kindKeys[node.Kind] = node.Key
		for _, match := range placeholderPattern.FindAllStringSubmatch(node.Title+"\n"+node.Description, -1) {
			if _, ok := wantParameters[match[1]]; !ok {
				problems = append(problems, "template parameter "+match[1]+" is not schema-approved")
			}
		}
		switch node.Kind {
		case "agent-work":
			if node.Profile != planningProfile || node.ProjectParameter != "initial_project" {
				problems = append(problems, "Planning Work must keep immutable profile factory-plan/v1 and initial_project scope")
			}
		case "plan-approval":
			if node.Profile != "" || node.ProjectParameter != "" {
				problems = append(problems, "Plan approval cannot carry a profile or project")
			}
		case "delivery":
			if node.Profile != "factory-deliver/v1" || node.ProjectParameter == "" {
				problems = append(problems, "Delivery must keep immutable profile factory-deliver/v1 and a project")
			}
			deliveryProjects[node.ProjectParameter] = true
			deliveryKeys[node.ProjectParameter] = node.Key
		case "provider-check":
			if !node.ExactRevision || node.ProjectParameter == "" {
				problems = append(problems, "each project requires an exact provider check")
			}
			checkProjects[node.ProjectParameter] = node.ExactRevision
			checkKeys[node.ProjectParameter] = node.Key
		case "human-merge":
			if node.ProjectParameter == "" {
				problems = append(problems, "each project requires human merge")
			}
			mergeProjects[node.ProjectParameter] = true
			mergeKeys[node.ProjectParameter] = node.Key
		default:
			problems = append(problems, "node "+node.Key+" has unsupported kind "+node.Kind)
		}
	}
	if kindCount["agent-work"] != 1 || kindCount["plan-approval"] != 1 {
		problems = append(problems, "Formula requires one Planning Work and one Plan approval")
	}
	if len(deliveryProjects) == 0 {
		problems = append(problems, "Formula requires one Delivery per project")
	}
	for project := range deliveryProjects {
		if !checkProjects[project] {
			problems = append(problems, "project "+project+" requires an exact provider check")
		}
		if !mergeProjects[project] {
			problems = append(problems, "project "+project+" requires human merge")
		}
	}
	edges := make(map[string]bool, len(definition.Edges))
	for _, edge := range definition.Edges {
		edges[edge.From+"\x00"+edge.To] = edge.Type == "blocks"
	}
	if !edges[kindKeys["plan-approval"]+"\x00"+kindKeys["agent-work"]] {
		problems = append(problems, "Formula must keep the Plan approval gating chain")
	}
	for project := range deliveryProjects {
		if !edges[deliveryKeys[project]+"\x00"+kindKeys["agent-work"]] ||
			!edges[checkKeys[project]+"\x00"+deliveryKeys[project]] ||
			!edges[mergeKeys[project]+"\x00"+checkKeys[project]] {
			problems = append(problems, "project "+project+" must keep its Delivery, exact-check, and human-merge gating chain")
		}
	}
	if graphUnsafe(keys, definition.Edges) {
		problems = append(problems, "Formula graph must reference existing nodes and remain acyclic")
	}
	sort.Strings(problems)
	return problems
}

func graphUnsafe(nodes map[string]formulaNode, edges []formulaEdge) bool {
	adjacent := make(map[string][]string, len(nodes))
	for _, edge := range edges {
		if edge.Type != "blocks" || nodes[edge.From].Key == "" || nodes[edge.To].Key == "" {
			return true
		}
		adjacent[edge.From] = append(adjacent[edge.From], edge.To)
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var cycle func(string) bool
	cycle = func(key string) bool {
		if visiting[key] {
			return true
		}
		if visited[key] {
			return false
		}
		visiting[key] = true
		for _, next := range adjacent[key] {
			if cycle(next) {
				return true
			}
		}
		visiting[key] = false
		visited[key] = true
		return false
	}
	for key := range nodes {
		if cycle(key) {
			return true
		}
	}
	return false
}

func formulaHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func (s *Service) PreviewFormula(definitionYAML string, parameters map[string]string) (FormulaPreview, error) {
	definition, normalized, validation := parseFormula(definitionYAML)
	if validation.Valid {
		validation.Errors = validateFormulaPolicy(definition)
		validation.Valid = len(validation.Errors) == 0
	}
	if !validation.Valid {
		return FormulaPreview{}, fmt.Errorf("%w: %s", ErrInvalidFormula, strings.Join(validation.Errors, "; "))
	}
	if strings.TrimSpace(parameters["goal"]) == "" || strings.TrimSpace(parameters["initial_project"]) == "" || len(parameters) != 2 {
		return FormulaPreview{}, fmt.Errorf("%w: goal and initial_project are required", ErrInvalidFormula)
	}
	preview := FormulaPreview{Name: definition.Name, FormulaHash: formulaHash(normalized), Nodes: []PreviewNode{{Key: "epic", Kind: "work-epic", Title: parameters["goal"]}}}
	for _, node := range definition.Nodes {
		preview.Nodes = append(preview.Nodes, PreviewNode{Key: node.Key, Kind: node.Kind, Title: renderFormulaText(node.Title, parameters), Profile: node.Profile, Project: parameters[node.ProjectParameter]})
	}
	for _, edge := range definition.Edges {
		preview.Edges = append(preview.Edges, PreviewEdge(edge))
	}
	return preview, nil
}

func renderFormulaText(text string, parameters map[string]string) string {
	return placeholderPattern.ReplaceAllStringFunc(text, func(value string) string {
		match := placeholderPattern.FindStringSubmatch(value)
		return parameters[match[1]]
	})
}

func (s *Service) SaveFormula(ctx context.Context, req SaveFormulaRequest) (FormulaRevision, error) {
	if req.ID == DefaultFormulaID {
		return FormulaRevision{}, ErrBuiltInFormulaImmutable
	}
	if s.formulas == nil {
		return FormulaRevision{}, fmt.Errorf("%w: formula store is unavailable", ErrFactoryUnavailable)
	}
	if !s.mutationOwned() {
		return FormulaRevision{}, fmt.Errorf("%w: this process does not own Factory mutations", ErrFactoryUnavailable)
	}
	if !formulaIDPattern.MatchString(req.ID) || strings.TrimSpace(req.Name) == "" {
		return FormulaRevision{}, fmt.Errorf("%w: stable ID and name are required", ErrInvalidFormula)
	}
	definition, normalized, validation := parseFormula(req.DefinitionYAML)
	if validation.Valid {
		validation.Errors = validateFormulaPolicy(definition)
		validation.Valid = len(validation.Errors) == 0
	}
	if !validation.Valid || definition.Name != req.Name {
		return FormulaRevision{}, fmt.Errorf("%w: definition is invalid or its name does not match", ErrInvalidFormula)
	}
	validation.ContentHash = formulaHash(normalized)
	evidence, _ := json.Marshal(validation)
	saved, err := s.formulas.SaveFactoryFormulaRevision(ctx, req.ID, req.Name, normalized, validation.ContentHash, string(evidence), 1, time.Now())
	if err != nil {
		if strings.Contains(err.Error(), "immutable") {
			return FormulaRevision{}, ErrBuiltInFormulaImmutable
		}
		return FormulaRevision{}, fmt.Errorf("save Formula: %w", err)
	}
	formula, saved, err := s.formulas.GetFactoryFormulaRevision(ctx, req.ID, saved.Revision)
	if err != nil {
		return FormulaRevision{}, fmt.Errorf("read saved Formula: %w", err)
	}
	return formulaRevisionFromState(formula, saved), nil
}

func (s *Service) ListFormulas(ctx context.Context) ([]FormulaSummary, error) {
	builtInHash := formulaHash(defaultFormulaYAML)
	builtIn := FormulaSummary{ID: DefaultFormulaID, Name: "Shipped delivery", Origin: FormulaOriginBuiltIn, CurrentRevision: DefaultFormulaVersion, ContentHash: builtInHash, Revisions: []FormulaRevisionRef{{Revision: DefaultFormulaVersion, ContentHash: builtInHash}}}
	if s.formulas == nil {
		return []FormulaSummary{builtIn}, nil
	}
	rows, err := s.formulas.ListFactoryFormulas(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Formulas: %w", err)
	}
	result := []FormulaSummary{builtIn}
	for _, row := range rows {
		if row.Source != "custom" {
			continue
		}
		_, revision, err := s.formulas.GetFactoryFormulaRevision(ctx, row.ID, row.CurrentRevision)
		if err != nil {
			return nil, fmt.Errorf("read Formula %s: %w", row.ID, err)
		}
		summary := FormulaSummary{ID: row.ID, Name: row.Name, Origin: FormulaOriginCustom, CurrentRevision: row.CurrentRevision, ContentHash: revision.ContentHash, Archived: row.ArchivedAt != 0}
		for number := 1; number <= row.CurrentRevision; number++ {
			_, saved, err := s.formulas.GetFactoryFormulaRevision(ctx, row.ID, number)
			if err != nil {
				return nil, fmt.Errorf("read Formula %s revision %d: %w", row.ID, number, err)
			}
			summary.Revisions = append(summary.Revisions, FormulaRevisionRef{Revision: number, ContentHash: saved.ContentHash})
		}
		result = append(result, summary)
	}
	return result, nil
}

func (s *Service) GetFormulaRevision(ctx context.Context, id string, revision int) (FormulaRevision, error) {
	if id == DefaultFormulaID {
		if revision != 0 && revision != DefaultFormulaVersion {
			return FormulaRevision{}, ErrFormulaNotFound
		}
		hash := formulaHash(defaultFormulaYAML)
		return FormulaRevision{FormulaSummary: FormulaSummary{ID: id, Name: "Shipped delivery", Origin: FormulaOriginBuiltIn, CurrentRevision: DefaultFormulaVersion, ContentHash: hash, Revisions: []FormulaRevisionRef{{Revision: DefaultFormulaVersion, ContentHash: hash}}}, Revision: DefaultFormulaVersion, SchemaVersion: 1, DefinitionYAML: defaultFormulaYAML}, nil
	}
	if s.formulas == nil {
		return FormulaRevision{}, ErrFormulaNotFound
	}
	formula, saved, err := s.formulas.GetFactoryFormulaRevision(ctx, id, revision)
	if errors.Is(err, sql.ErrNoRows) {
		return FormulaRevision{}, ErrFormulaNotFound
	}
	if err != nil {
		return FormulaRevision{}, fmt.Errorf("get Formula revision: %w", err)
	}
	return formulaRevisionFromState(formula, saved), nil
}

func formulaRevisionFromState(formula state.FactoryFormula, revision state.FactoryFormulaRevision) FormulaRevision {
	return FormulaRevision{FormulaSummary: FormulaSummary{ID: formula.ID, Name: formula.Name, Origin: FormulaOriginCustom, CurrentRevision: formula.CurrentRevision, ContentHash: revision.ContentHash, Archived: formula.ArchivedAt != 0}, Revision: revision.Revision, SchemaVersion: revision.SchemaVersion, DefinitionYAML: revision.DefinitionYAML}
}

func (s *Service) ArchiveFormula(ctx context.Context, id string) error {
	if id == DefaultFormulaID {
		return ErrBuiltInFormulaImmutable
	}
	if s.formulas == nil {
		return ErrFormulaNotFound
	}
	if !s.mutationOwned() {
		return fmt.Errorf("%w: this process does not own Factory mutations", ErrFactoryUnavailable)
	}
	changed, err := s.formulas.ArchiveFactoryFormula(ctx, id, time.Now())
	if err != nil {
		return fmt.Errorf("archive Formula: %w", err)
	}
	if !changed {
		return ErrFormulaNotFound
	}
	return nil
}

func (s *Service) DeleteFormula(ctx context.Context, id string) error {
	if id == DefaultFormulaID {
		return ErrBuiltInFormulaImmutable
	}
	if !s.mutationOwned() {
		return fmt.Errorf("%w: this process does not own Factory mutations", ErrFactoryUnavailable)
	}
	epics, err := s.ListWorkEpics(ctx)
	if err != nil {
		return err
	}
	for _, epic := range epics {
		if epic.FormulaID == id {
			return ErrFormulaReferenced
		}
	}
	if s.formulas == nil {
		return ErrFormulaNotFound
	}
	changed, err := s.formulas.DeleteFactoryFormula(ctx, id)
	if err != nil {
		return fmt.Errorf("delete Formula: %w", err)
	}
	if !changed {
		return ErrFormulaNotFound
	}
	return nil
}

func (s *Service) mutationOwned() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.owned
}

func definitionForRevision(revision FormulaRevision) (formulaDefinition, error) {
	definition, _, validation := parseFormula(revision.DefinitionYAML)
	if !validation.Valid || len(validateFormulaPolicy(definition)) != 0 {
		return formulaDefinition{}, ErrInvalidFormula
	}
	return definition, nil
}

func graphForFormula(definition formulaDefinition, req CreateWorkEpicRequest, provenance func(string) map[string]string) graphPlan {
	parameters := map[string]string{"goal": req.Goal, "initial_project": req.InitialProject}
	epic := provenance("work-epic")
	epic["ocman.goal"], epic["ocman.initial_project"] = req.Goal, req.InitialProject
	epicRefs := map[string]string{"ocman.planning_work_id": "planning", "ocman.plan_approval_gate_id": "approval"}
	plan := graphPlan{CommitMessage: "ocman factory: instantiate " + req.InstantiationID, Nodes: []graphNode{{Key: "epic", Title: req.Goal, Type: "epic", Metadata: epic, MetadataRefs: epicRefs}}}
	for _, node := range definition.Nodes {
		provenanceKind := node.Kind
		if node.Kind == "plan-approval" || node.Kind == "provider-check" || node.Kind == "human-merge" {
			provenanceKind = "gate"
		}
		metadata := provenance(provenanceKind)
		if node.Profile != "" {
			metadata["ocman.permission_profile"] = node.Profile
		}
		if node.ExactRevision {
			metadata["ocman.exact_revision"] = "true"
		}
		typeName := "task"
		switch node.Kind {
		case "plan-approval", "provider-check", "human-merge":
			typeName = "gate"
			metadata["ocman.gate_type"] = node.Kind
		case "delivery":
			metadata["ocman.project"] = parameters[node.ProjectParameter]
		}
		if node.ProjectParameter != "" {
			metadata["ocman.project"] = parameters[node.ProjectParameter]
		}
		plan.Nodes = append(plan.Nodes, graphNode{Key: node.Key, Title: renderFormulaText(node.Title, parameters), Type: typeName, Description: renderFormulaText(node.Description, parameters), ParentKey: "epic", Metadata: metadata, MetadataRefs: map[string]string{"ocman.work_epic_id": "epic"}})
	}
	for _, edge := range definition.Edges {
		plan.Edges = append(plan.Edges, graphEdge{FromKey: edge.From, ToKey: edge.To, Type: edge.Type})
	}
	return plan
}
