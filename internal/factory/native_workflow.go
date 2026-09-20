package factory

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"gopkg.in/yaml.v3"
)

//go:embed tracer-v3.yaml
var tracerWorkflowSource string

func (definition compiledNativeFormula) viewPrompts() map[string]string {
	if definition.Steps != nil {
		return nil
	}
	return effectiveFormulaPrompts(definition.Prompts)
}

func compileWorkflow(source string) (nativeDefinition, error) {
	var workflow struct {
		Version int                           `yaml:"version"`
		Name    string                        `yaml:"name"`
		Steps   map[string]model.WorkflowStep `yaml:"steps"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(source))
	decoder.KnownFields(true)
	if err := decoder.Decode(&workflow); err != nil {
		return nativeDefinition{}, fmt.Errorf("invalid workflow YAML: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nativeDefinition{}, errors.New("formula must contain exactly one YAML document")
	}
	if workflow.Version != 2 || strings.TrimSpace(workflow.Name) == "" || len(workflow.Steps) == 0 || len(workflow.Steps) > 100 {
		return nativeDefinition{}, errors.New("workflow requires version: 2, a name, and 1–100 named steps")
	}
	result := compiledNativeFormula{Version: 2, Name: workflow.Name, Inputs: []string{"goal", "initial_project"}, Steps: workflow.Steps, SourceDigest: sourceHash(source)}
	counts := map[string]int{}
	keys := make([]string, 0, len(workflow.Steps))
	for key := range workflow.Steps {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		step := workflow.Steps[key]
		if !model.ValidNativeFormulaKey(key) {
			return nativeDefinition{}, fmt.Errorf("step %q must have a stable key", key)
		}
		if step.Kind != "planning" && step.Kind != "approval" && step.Kind != "implementation" && step.Kind != "verification" && step.Kind != "delivery" {
			return nativeDefinition{}, fmt.Errorf("step %s: unsupported kind %q", key, step.Kind)
		}
		if step.Kind != "approval" && (strings.TrimSpace(step.Prompt) == "" || len(step.Prompt) > 32768) {
			return nativeDefinition{}, fmt.Errorf("step %s requires a prompt of 1–32768 bytes", key)
		}
		if step.Config.Concurrency != 0 && (step.Kind != "implementation" || step.Config.Concurrency != 1) {
			return nativeDefinition{}, fmt.Errorf("step %s: only implementation concurrency: 1 is supported for the shared workspace", key)
		}
		if step.Config.ScopeExpansionPrompt != "" && (step.Kind != "planning" || len(step.Config.ScopeExpansionPrompt) > 32768) {
			return nativeDefinition{}, fmt.Errorf("step %s: scope_expansion_prompt belongs to planning and is limited to 32768 bytes", key)
		}
		if step.Config.Model != "" && (!strings.Contains(step.Config.Model, "/") || strings.ContainsAny(step.Config.Model, " \t\r\n")) {
			return nativeDefinition{}, fmt.Errorf("step %s: model must be provider/model", key)
		}
		if step.Kind == "approval" && step.Config.Model != "" {
			return nativeDefinition{}, fmt.Errorf("step %s: approval does not use a model", key)
		}
		seen := map[string]bool{}
		for _, need := range step.Needs {
			if _, found := workflow.Steps[need]; !found || need == key || seen[need] {
				return nativeDefinition{}, fmt.Errorf("step %s: invalid or duplicate dependency %q", key, need)
			}
			seen[need] = true
			result.Edges = append(result.Edges, FormulaGraphEdge{From: key, To: need, Type: "blocks"})
		}
		step.Key = key
		sort.Strings(step.Needs)
		workflow.Steps[key] = step
		counts[step.Kind]++
		result.Nodes = append(result.Nodes, FormulaGraphNode{Key: key, Kind: step.Kind})
	}
	if counts["planning"] != 1 || counts["implementation"] != 1 || counts["delivery"] != 1 || counts["approval"] == 0 {
		return nativeDefinition{}, errors.New("workflow requires one planning, one implementation, one delivery, and at least one approval step")
	}
	if hasFormulaCycle(result.Edges) {
		return nativeDefinition{}, errors.New("workflow dependencies must be acyclic")
	}
	var plan, implement, delivery string
	for key, step := range workflow.Steps {
		switch step.Kind {
		case "planning":
			plan = key
		case "implementation":
			implement = key
		case "delivery":
			delivery = key
		}
	}
	depends := func(from, to string) bool { return workflowDependsOn(workflow.Steps, from, to) }
	if len(workflow.Steps[plan].Needs) != 0 {
		return nativeDefinition{}, errors.New("planning must be the first step")
	}
	approvals := 0
	for key, step := range workflow.Steps {
		if step.Kind == "approval" && len(step.Needs) == 1 && step.Needs[0] == plan {
			approvals++
			if !depends(implement, key) {
				return nativeDefinition{}, errors.New("implementation must depend on the initial plan approval")
			}
		}
		if key != plan && !depends(key, plan) {
			return nativeDefinition{}, fmt.Errorf("step %s must depend on planning", key)
		}
		if key != delivery && !depends(delivery, key) {
			return nativeDefinition{}, fmt.Errorf("delivery must depend on step %s", key)
		}
		if step.Kind == "verification" && !depends(key, implement) {
			return nativeDefinition{}, fmt.Errorf("verification step %s must depend on implementation", key)
		}
	}
	if approvals != 1 {
		return nativeDefinition{}, errors.New("implementation must depend on one initial approval directly after planning")
	}
	// Include source provenance so saving comments or formatting never loses edits.
	sort.Slice(result.Edges, func(i, j int) bool {
		if result.Edges[i].From == result.Edges[j].From {
			return result.Edges[i].To < result.Edges[j].To
		}
		return result.Edges[i].From < result.Edges[j].From
	})
	raw, err := json.Marshal(result)
	if err != nil {
		return nativeDefinition{}, err
	}
	hash := sha256.Sum256(raw)
	return nativeDefinition{compiledNativeFormula: result, JSON: string(raw), Hash: hex.EncodeToString(hash[:])}, nil
}

func workflowDependsOn(steps map[string]model.WorkflowStep, from, to string) bool {
	pending := append([]string{}, steps[from].Needs...)
	seen := map[string]bool{}
	for len(pending) > 0 {
		key := pending[0]
		pending = pending[1:]
		if key == to {
			return true
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		pending = append(pending, steps[key].Needs...)
	}
	return false
}

func workflowRuntimeKind(steps map[string]model.WorkflowStep, key string) string {
	step := steps[key]
	switch step.Kind {
	case "planning":
		return "plan"
	case "implementation":
		return "materialization"
	case "verification", "delivery":
		return "workflow_template"
	case "approval":
		if len(step.Needs) == 1 && steps[step.Needs[0]].Kind == "planning" {
			return "gate"
		}
		return "approval"
	default:
		return step.Kind
	}
}
