package factory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

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
	Steps       map[string]model.WorkflowStep `json:"steps,omitempty"`
	Prompts     map[string]string             `json:"prompts,omitempty"`
	ID          string                        `json:"id"`
	Version     int                           `json:"version"`
	Name        string                        `json:"name"`
	Source      string                        `json:"source"`
	Hash        string                        `json:"hash"`
	SourceHash  string                        `json:"sourceHash"`
	Compiled    json.RawMessage               `json:"compiled"`
	Inputs      []string                      `json:"inputs"`
	Nodes       []FormulaGraphNode            `json:"nodes"`
	Edges       []FormulaGraphEdge            `json:"edges"`
	Composition []FormulaComposition          `json:"composition"`
	Valid       bool                          `json:"valid"`
	Errors      []string                      `json:"errors"`
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
	compiled, err := compileNativeFormula(tracerWorkflowSource)
	if err != nil {
		panic("invalid built-in tracer Formula: " + err.Error())
	}
	return TracerFormula{ID: "ocman/tracer", Version: 3, Source: tracerWorkflowSource, Hash: compiled.Hash}
}

func sourceHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

type compiledNativeFormula struct {
	SourceDigest string                        `json:"sourceHash,omitempty"`
	Steps        map[string]model.WorkflowStep `json:"steps,omitempty"`
	Version      int                           `json:"version"`
	Name         string                        `json:"name"`
	Inputs       []string                      `json:"inputs"`
	Nodes        []FormulaGraphNode            `json:"nodes"`
	Edges        []FormulaGraphEdge            `json:"edges"`
	Composition  []FormulaComposition          `json:"composition"`
	Prompts      map[string]string             `json:"prompts,omitempty"`
}

type nativeDefinition struct {
	compiledNativeFormula
	JSON string
	Hash string
}

// compileNativeFormula accepts only the deliberately small TOML schema used by native Factory.
func compileNativeFormula(source string) (nativeDefinition, error) {
	if regexp.MustCompile(`(?m)^version\s*:`).MatchString(source) {
		return compileWorkflow(source)
	}
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
			stage := strings.TrimPrefix(key, "prompt_")
			promptKey := key == "prompt_planning" || key == "prompt_scope_expansion" || key == "prompt_implementation" || key == "prompt_delivery"
			if key != "version" && key != "name" && !promptKey {
				return nativeDefinition{}, fmt.Errorf("line %d: unsupported top-level key", lineNo+1)
			}
			if seenRoot[key] {
				return nativeDefinition{}, fmt.Errorf("line %d: duplicate key %s", lineNo+1, key)
			}
			seenRoot[key] = true
			if promptKey {
				text, err := unquoteTOML(value)
				if err != nil || strings.TrimSpace(text) == "" || len(text) > 32768 {
					return nativeDefinition{}, fmt.Errorf("line %d: %s must be a non-empty TOML string of at most 32768 bytes", lineNo+1, key)
				}
				if result.Prompts == nil {
					result.Prompts = map[string]string{}
				}
				result.Prompts[stage] = text
			} else if key == "version" {
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
