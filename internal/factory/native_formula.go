package factory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// GetFormula returns the exact immutable native Formula revision.
func (s *NativeService) GetFormula(ctx context.Context, id string, version int) (NativeFormulaView, error) {
	formula := BuiltInTracerFormula()
	if id == formula.ID && version == 1 {
		formula.Version, formula.Source = 1, tracerFormulaSource
	}
	if id == formula.ID && version == 2 {
		formula.Version, formula.Source = 2, tracerFormulaV2Source
	}
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
	return NativeFormulaView{ID: formula.ID, Version: formula.Version, Name: compiled.Name, Source: formula.Source, Hash: compiled.Hash, SourceHash: sourceHash(formula.Source), Compiled: json.RawMessage(compiled.JSON), Inputs: compiled.Inputs, Nodes: compiled.Nodes, Edges: compiled.Edges, Composition: compiled.Composition, Prompts: compiled.viewPrompts(), Steps: compiled.Steps, Valid: true, Errors: []string{}}, nil
}

func (s *NativeService) ListFormulas(ctx context.Context) ([]NativeFormulaView, error) {
	current := BuiltInTracerFormula()
	builtIn, err := s.GetFormula(ctx, current.ID, current.Version)
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
	return NativeFormulaView{ID: id, Version: version, Name: compiled.Name, Source: source, Hash: compiled.Hash, SourceHash: sourceHash(source), Compiled: json.RawMessage(compiled.JSON), Inputs: compiled.Inputs, Nodes: compiled.Nodes, Edges: compiled.Edges, Composition: compiled.Composition, Prompts: compiled.viewPrompts(), Steps: compiled.Steps, Valid: len(problems) == 0, Errors: problems}, nil
}

func nativeFormulaView(id string, revision int, _ string, source, hash, compiledJSON string) (NativeFormulaView, error) {
	compiled, err := compileNativeFormula(source)
	if err != nil {
		return NativeFormulaView{}, fmt.Errorf("%w: recompiling native Formula: %w", ErrFormulaCorrupt, err)
	}
	if compiledJSON != compiled.JSON || hash != compiled.Hash {
		return NativeFormulaView{}, fmt.Errorf("%w: compiled content does not match source", ErrFormulaCorrupt)
	}
	return NativeFormulaView{ID: id, Version: revision, Name: compiled.Name, Source: source, Hash: compiled.Hash, SourceHash: sourceHash(source), Compiled: json.RawMessage(compiled.JSON), Inputs: compiled.Inputs, Nodes: compiled.Nodes, Edges: compiled.Edges, Composition: compiled.Composition, Prompts: compiled.viewPrompts(), Steps: compiled.Steps, Valid: true, Errors: []string{}}, nil
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
	if id == "ocman/tracer" && revision == 3 {
		return tracerWorkflowSource, nil
	}
	if id == "ocman/tracer" && revision == 2 {
		return tracerFormulaV2Source, nil
	}
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
	for _, node := range definition.Nodes {
		runtimeNode := model.NativeFormulaNode{Key: node.Key, Kind: node.Kind}
		if step, ok := definition.Steps[node.Key]; ok {
			runtimeNode.Kind, runtimeNode.Workflow = workflowRuntimeKind(definition.Steps, node.Key), &step
		}
		formula.Nodes = append(formula.Nodes, runtimeNode)
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
