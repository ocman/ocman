package dagu

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/NoUseFreak/ocman/internal/workflows"
	"gopkg.in/yaml.v3"
)

// Compile turns a published workflow version into a Dagu DAG spec.
//
// Dagu is a pure executor: it resolves the graph, runs steps, passes
// outputs, and propagates skips. Ocman keeps everything else, so a
// compiled spec deliberately carries no schedule (ocman owns triggering)
// and no auto-retry (ocman owns run history and retry decisions).
//
// Dagu 2.10 constraints encoded here:
//   - keys are snake_case; camelCase is rejected outright by the loader
//   - `command:` is used for shell steps, never `run:`, which resolves a
//     step against DAG names and would silently mis-target a command
//     whose first word matches an existing DAG
//   - `retry_policy.limit: 0` is mandatory. Dagu defaults to 3 automatic
//     DAG retries, which would silently re-execute agent steps and bill
//     an LLM three times per failure.
//   - step names carry the ocman node ID, not the display name, because
//     the run mirror joins dagu nodes back to ocman rows on it
//   - a precondition is a bare shell command: exit 0 proceeds, non-zero
//     skips, and the skip propagates to descendants. Command
//     substitution with `expected` does not evaluate, so conditions are
//     delegated to the shim, which owns ocman's CEL evaluator.
type CompileOptions struct {
	// RunID is the ocman run ID. It doubles as the dagu dagRunId, so the
	// two systems join without a lookup table.
	RunID string
	// Shim is the ocman binary that executes agent, approval, join, and
	// condition steps. Empty uses "ocman" from PATH.
	Shim string
	// ResolveVersion returns the pinned definition behind a map node's
	// subworkflow version. Only required when the definition maps.
	ResolveVersion func(versionID string) (workflows.Definition, error)
}

// Compiled is a parent spec plus the child DAG specs it references.
// Dagu resolves `dag.run` targets by name from its DAGs directory, so
// children must be on disk before the parent starts.
type Compiled struct {
	Spec     []byte
	Children map[string][]byte
}

const defaultShim = "ocman"

type retryPolicy struct {
	Limit int `yaml:"limit"`
}

type daguWith struct {
	DAG    string            `yaml:"dag"`
	Params map[string]string `yaml:"params,omitempty"`
}

type daguParallel struct {
	Items         string `yaml:"items"`
	MaxConcurrent int    `yaml:"max_concurrent,omitempty"`
}

type daguPrecondition struct {
	Condition string `yaml:"condition"`
}

type daguStep struct {
	Name          string             `yaml:"name"`
	Command       string             `yaml:"command,omitempty"`
	Action        string             `yaml:"action,omitempty"`
	With          *daguWith          `yaml:"with,omitempty"`
	Parallel      *daguParallel      `yaml:"parallel,omitempty"`
	Env           map[string]string  `yaml:"env,omitempty"`
	Output        string             `yaml:"output,omitempty"`
	Depends       []string           `yaml:"depends,omitempty"`
	Preconditions []daguPrecondition `yaml:"preconditions,omitempty"`
}

type daguSpec struct {
	WorkingDir     string            `yaml:"working_dir,omitempty"`
	RetryPolicy    retryPolicy       `yaml:"retry_policy"`
	MaxActiveSteps int               `yaml:"max_active_steps,omitempty"`
	Params         map[string]string `yaml:"params,omitempty"`
	Steps          []daguStep        `yaml:"steps"`
}

// Item parameters a mapped child DAG receives from its parent. A child
// spec is shared by every item of every run, so the parent run, map
// node, and stable item key all arrive as parameters rather than being
// baked in. That is what lets one compiled child be cached per version.
const (
	paramParentRun = "ocman_parent_run"
	paramMapNode   = "ocman_map_node"
	paramItemKey   = "ocman_item_key"
	paramItem      = "ocman_item"
)

func Compile(definition workflows.Definition, options CompileOptions) (Compiled, error) {
	if definition.ID == "" || len(definition.Nodes) == 0 {
		return Compiled{}, fmt.Errorf("workflow requires an ID and nodes")
	}
	if options.RunID == "" {
		return Compiled{}, fmt.Errorf("compile requires a run ID")
	}
	compiled := Compiled{Children: map[string][]byte{}}
	spec, err := compileDefinition(definition, options, false, compiled.Children)
	if err != nil {
		return Compiled{}, err
	}
	compiled.Spec = spec
	if len(compiled.Children) == 0 {
		compiled.Children = nil
	}
	return compiled, nil
}

// compileDefinition renders one definition. child marks a mapped
// per-item pipeline, whose shim steps address their ocman run through
// parent run + item key parameters instead of a fixed run ID.
func compileDefinition(definition workflows.Definition, options CompileOptions, child bool, children map[string][]byte) ([]byte, error) {
	if err := rejectUnsupported(definition); err != nil {
		return nil, err
	}
	variables, err := outputVariables(definition)
	if err != nil {
		return nil, err
	}
	shim := options.Shim
	if shim == "" {
		shim = defaultShim
	}
	spec := daguSpec{
		WorkingDir:     definition.Directory,
		RetryPolicy:    retryPolicy{Limit: 0},
		MaxActiveSteps: definition.Concurrency,
	}
	if child {
		spec.Params = map[string]string{paramParentRun: "", paramMapNode: "", paramItemKey: "", paramItem: ""}
	}
	for _, node := range definition.Nodes {
		step, err := compileNode(definition, node, variables, options, shim, child, children)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.ID, err)
		}
		spec.Steps = append(spec.Steps, step)
	}
	return yaml.Marshal(spec)
}

func compileNode(definition workflows.Definition, node workflows.Node, variables map[string]string, options CompileOptions, shim string, child bool, children map[string][]byte) (daguStep, error) {
	step := daguStep{Name: node.ID, Output: variables[node.ID]}
	for _, dependency := range definition.Dependencies {
		if dependency.To != node.ID {
			continue
		}
		step.Depends = append(step.Depends, dependency.From)
		if dependency.Condition != "" {
			// Ocman owns the CEL evaluator. Exit 0 proceeds, non-zero
			// skips this step and, by dagu's propagation, its descendants.
			step.Preconditions = append(step.Preconditions, daguPrecondition{
				Condition: shellCommand([]string{shim, "workflow-step", "condition",
					"--run", options.RunID, "--node", node.ID, "--from", dependency.From}),
			})
		}
	}
	switch node.Type {
	case "command":
		if len(node.Command) == 0 {
			return daguStep{}, fmt.Errorf("command node requires a command")
		}
		arguments := make([]string, len(node.Command))
		for i, argument := range node.Command {
			translated, err := translateReferences(argument, definition, node.ID, variables)
			if err != nil {
				return daguStep{}, err
			}
			arguments[i] = translated
		}
		step.Command = shellCommand(arguments)
		if len(node.Environment) > 0 {
			step.Env = make(map[string]string, len(node.Environment))
			for name, value := range node.Environment {
				translated, err := translateReferences(value, definition, node.ID, variables)
				if err != nil {
					return daguStep{}, err
				}
				step.Env[name] = translated
			}
		}
	case "agent", "approval":
		// The shim reads the node's real configuration (prompt, model,
		// schema) from ocman's own database, so prompts and credentials
		// never reach the spec or dagu's on-disk step logs. Upstream node
		// results are the one thing dagu holds, so they are handed over
		// as a single JSON object dagu interpolates.
		step.Command, step.Env = shimStep(shim, node.Type, node.ID, options.RunID, child)
		if upstream := upstreamEnvironment(definition, node.ID, variables); upstream != "" {
			step.Env["OCMAN_UPSTREAM"] = upstream
		}
	case "join":
		// A failed policy must fail the run, not skip it, so a join is a
		// command that exits non-zero rather than a precondition.
		source := mapNodeFor(definition, node.ID)
		if source == "" {
			return daguStep{}, fmt.Errorf("join node is not referenced by any map node")
		}
		step.Command, step.Env = shimStep(shim, "join", node.ID, options.RunID, child)
		step.Env["OCMAN_MAP_RESULT"] = "${" + variables[source] + "}"
	case "map":
		if err := compileMap(definition, node, variables, options, &step, children); err != nil {
			return daguStep{}, err
		}
	default:
		return daguStep{}, fmt.Errorf("node type %q is not supported by the Dagu runner yet", node.Type)
	}
	return step, nil
}

func compileMap(definition workflows.Definition, node workflows.Node, variables map[string]string, options CompileOptions, step *daguStep, children map[string][]byte) error {
	configuration := node.Map
	if configuration == nil {
		return fmt.Errorf("map node requires a map configuration")
	}
	if configuration.Key == "" {
		return fmt.Errorf("map node requires a stable item key")
	}
	if configuration.VersionID == "" {
		return fmt.Errorf("map node requires a pinned subworkflow version")
	}
	if options.ResolveVersion == nil {
		return fmt.Errorf("map node requires a version resolver")
	}
	items, err := translateReferences(configuration.Source, definition, node.ID, variables)
	if err != nil {
		return err
	}
	childDefinition, err := options.ResolveVersion(configuration.VersionID)
	if err != nil {
		return fmt.Errorf("resolve mapped version %q: %w", configuration.VersionID, err)
	}
	name := SafeDAGName(childDefinition.ID, configuration.VersionID)
	if _, done := children[name]; !done {
		// Reserve the name before recursing so a self-referential map
		// cannot spin forever.
		children[name] = nil
		spec, err := compileDefinition(childDefinition, options, true, children)
		if err != nil {
			return fmt.Errorf("mapped version %q: %w", configuration.VersionID, err)
		}
		children[name] = spec
	}
	step.Action = "dag.run"
	step.With = &daguWith{DAG: name, Params: map[string]string{
		paramParentRun: options.RunID,
		paramMapNode:   node.ID,
		paramItemKey:   "${ITEM." + configuration.Key + "}",
		paramItem:      "${ITEM}",
	}}
	step.Parallel = &daguParallel{Items: items, MaxConcurrent: definition.Concurrency}
	return nil
}

// shimStep builds an ocman shim invocation. In a mapped child the run is
// addressed by parent run and stable item key, because dagu creates the
// per-item runs itself and ocman only learns their identity from the
// key it passed down.
func shimStep(shim, kind, nodeID, runID string, child bool) (string, map[string]string) {
	command := shellCommand([]string{shim, "workflow-step", kind, "--node", nodeID})
	if !child {
		return shellCommand([]string{shim, "workflow-step", kind, "--run", runID, "--node", nodeID}), map[string]string{}
	}
	return command, map[string]string{
		"OCMAN_PARENT_RUN": "${" + paramParentRun + "}",
		"OCMAN_MAP_NODE":   "${" + paramMapNode + "}",
		"OCMAN_ITEM_KEY":   "${" + paramItemKey + "}",
		"OCMAN_ITEM":       "${" + paramItem + "}",
	}
}

func mapNodeFor(definition workflows.Definition, joinID string) string {
	for _, node := range definition.Nodes {
		if node.Type == "map" && node.Map != nil && node.Map.Join == joinID {
			return node.ID
		}
	}
	return ""
}

// SafeDAGName derives a filesystem- and dagu-safe DAG name for a pinned
// version. Sanitizing alone can collide (two ids differing only in
// stripped characters), so a digest of the exact version id is always
// appended.
func SafeDAGName(workflowID, versionID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		default:
			return '-'
		}
	}, workflowID)
	safe = strings.Trim(safe, "-")
	if safe == "" {
		safe = "workflow"
	}
	if len(safe) > 48 {
		safe = strings.Trim(safe[:48], "-")
	}
	// The digest covers both ids: sanitizing is lossy, so two distinct
	// workflow ids can fold to the same prefix.
	digest := sha256.Sum256([]byte(workflowID + "\x00" + versionID))
	return "ocman-" + safe + "-" + hex.EncodeToString(digest[:])[:12]
}

// rejectUnsupported fails fast on definition features that have no
// verified Dagu translation yet, rather than emitting a spec that would
// silently drop them.
func rejectUnsupported(definition workflows.Definition) error {
	if len(definition.Secrets) > 0 {
		return fmt.Errorf("secrets are not supported by the Dagu runner yet")
	}
	if len(definition.Pools) > 0 {
		return fmt.Errorf("resource pools are not supported by the Dagu runner yet")
	}
	if definition.Workspace != nil {
		return fmt.Errorf("managed workspaces are not supported by the Dagu runner yet")
	}
	if definition.Limits != nil {
		return fmt.Errorf("run limits are not supported by the Dagu runner yet")
	}
	if definition.FailFast {
		return fmt.Errorf("fail-fast is not supported by the Dagu runner yet")
	}
	for _, node := range definition.Nodes {
		switch {
		case node.Repeat != nil:
			return fmt.Errorf("node %q: repeat is not supported by the Dagu runner yet", node.ID)
		case node.Lease != nil:
			return fmt.Errorf("node %q: workspace leases are not supported by the Dagu runner yet", node.ID)
		case len(node.Resources) > 0:
			return fmt.Errorf("node %q: resource requests are not supported by the Dagu runner yet", node.ID)
		}
	}
	known := make(map[string]bool, len(definition.Nodes))
	for _, node := range definition.Nodes {
		known[node.ID] = true
	}
	for _, dependency := range definition.Dependencies {
		// Dagu resolves depends by name and would fail at run time, well
		// after ocman reported the run as started.
		if !known[dependency.From] || !known[dependency.To] {
			return fmt.Errorf("dependency %q -> %q references an unknown node", dependency.From, dependency.To)
		}
	}
	return nil
}

// outputVariables assigns each output-producing node a Dagu variable
// name. Dagu variables are shell-style identifiers, so node IDs are
// folded to upper case with every other character replaced; the fold can
// collide, and a collision would silently cross-wire two nodes' results.
func outputVariables(definition workflows.Definition) (map[string]string, error) {
	variables := make(map[string]string, len(definition.Nodes))
	owners := make(map[string]string, len(definition.Nodes))
	for _, node := range definition.Nodes {
		switch node.Type {
		case "command", "agent", "map", "join":
		default:
			continue
		}
		name := "OUT_" + strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z':
				return r - 32
			case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
				return r
			default:
				return '_'
			}
		}, node.ID)
		if owner, taken := owners[name]; taken {
			return nil, fmt.Errorf("nodes %q and %q both map to Dagu variable %s; rename one", owner, node.ID, name)
		}
		owners[name] = node.ID
		variables[node.ID] = name
	}
	return variables, nil
}

var nodeReference = regexp.MustCompile(`\$\{nodes\.([^{}]+)\}`)

// translateReferences rewrites ocman ${nodes.<id>.output...} references
// into the Dagu variable references the runner resolves at execution
// time. Only the output channel translates: dagu captures a step's
// stdout and nothing else, so envelope fields such as status have no
// equivalent and are rejected instead of silently resolving to nothing.
func translateReferences(input string, definition workflows.Definition, nodeID string, variables map[string]string) (string, error) {
	if !strings.Contains(input, "${nodes") {
		return input, nil
	}
	if strings.Contains(nodeReference.ReplaceAllString(input, ""), "${nodes") {
		return "", fmt.Errorf("malformed node result reference")
	}
	ancestors := workflows.TransitiveDependencies(definition, nodeID)
	var failure error
	output := nodeReference.ReplaceAllStringFunc(input, func(reference string) string {
		tail := nodeReference.FindStringSubmatch(reference)[1]
		referenced, path := longestNodeMatch(tail, ancestors)
		if referenced == "" {
			failure = fmt.Errorf("%q does not identify a dependency of %q", tail, nodeID)
			return reference
		}
		variable, ok := variables[referenced]
		if !ok {
			failure = fmt.Errorf("node %q produces no output to reference", referenced)
			return reference
		}
		if path != "output" && !strings.HasPrefix(path, "output.") {
			failure = fmt.Errorf("%q is not supported; the Dagu runner exposes only the output channel", reference)
			return reference
		}
		selector := strings.TrimPrefix(path, "output")
		// Dagu resolves nested object paths but not array indices: the
		// reference survives interpolation and reaches the shell as a
		// bad substitution at run time.
		for _, segment := range strings.Split(strings.TrimPrefix(selector, "."), ".") {
			if segment != "" && strings.IndexFunc(segment, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
				failure = fmt.Errorf("%q indexes an array; the Dagu runner resolves object fields only", reference)
				return reference
			}
		}
		return "${" + variable + selector + "}"
	})
	if failure != nil {
		return "", failure
	}
	return output, nil
}

func longestNodeMatch(reference string, ancestors map[string]bool) (string, string) {
	var match string
	for id := range ancestors {
		if (reference == id || strings.HasPrefix(reference, id+".")) && len(id) > len(match) {
			match = id
		}
	}
	return match, strings.TrimPrefix(strings.TrimPrefix(reference, match), ".")
}

// upstreamEnvironment hands a shim step every upstream node result as one
// JSON object, letting ocman's own interpolator resolve prompts without
// the prompt text ever entering the spec.
func upstreamEnvironment(definition workflows.Definition, nodeID string, variables map[string]string) string {
	var names []string
	for ancestor := range workflows.TransitiveDependencies(definition, nodeID) {
		if _, ok := variables[ancestor]; ok {
			names = append(names, ancestor)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	fields := make([]string, len(names))
	for i, name := range names {
		fields[i] = fmt.Sprintf("%q: ${%s}", name, variables[name])
	}
	return "{" + strings.Join(fields, ", ") + "}"
}
