package workflows

import (
	"encoding/json"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/state"
)

// subPrefix namespaces a subworkflow's inlined node id under the
// referencing parent node so ids stay unique after inlining.
func subPrefix(parentNodeID, childNodeID string) string {
	return parentNodeID + "/" + childNodeID
}

// pinnedSubworkflow resolves a subworkflow reference to its active
// version's already-inlined definition. Because every stored version is
// fully inlined at publish time, resolving one level here transitively
// pins the whole nested tree to concrete revisions.
func (s *Service) pinnedSubworkflow(ref SubworkflowRef) (Definition, string, error) {
	if ref.WorkflowID == "" {
		return Definition{}, "", fmt.Errorf("subworkflow reference requires workflowId")
	}
	row, err := s.store.GetActiveWorkflowVersion(ref.WorkflowID)
	if err != nil {
		return Definition{}, "", fmt.Errorf("subworkflow %q has no active version: %w", ref.WorkflowID, err)
	}
	var definition Definition
	if err := json.Unmarshal([]byte(row.DefinitionJSON), &definition); err != nil {
		return Definition{}, "", fmt.Errorf("decoding subworkflow %q: %w", ref.WorkflowID, err)
	}
	return definition, row.ID, nil
}

// inlineSubworkflows expands every subworkflow node in the definition
// into the referenced workflow's pinned nodes and dependencies, with ids
// namespaced under the referencing node. Map nodes are not inlined here
// (their fan-out is dynamic and resolved per run); their per-item
// subworkflow reference is only validated for existence and recursion.
// The returned definition has no subworkflow nodes left.
//
// ponytail: inlined nodes are re-validated against the parent's directory,
// so a subworkflow containing command nodes (which use the workflow-level
// directory) only composes cleanly when the parent declares the same
// directory. Approval and agent nodes carry their own directory/prompt and
// compose without that constraint — which covers the map/join per-item
// pipelines #317 targets. Add per-node directory override if a reusable
// command subworkflow needs a different root than its parent.
func (s *Service) inlineSubworkflows(definition Definition) (Definition, error) {
	if err := s.checkSubworkflowRecursion(definition); err != nil {
		return Definition{}, err
	}
	// Capture the authored reference graph before inlining removes the
	// subworkflow nodes.
	authoredRefs := definitionRefs(definition)
	var nodes []Node
	var deps []Dependency
	for _, node := range definition.Nodes {
		if node.Type == "map" && node.Map != nil {
			// Pin the per-item subworkflow to a concrete active version so
			// a later edit cannot alter this parent version's mapped runs.
			_, versionID, err := s.pinnedSubworkflow(node.Map.Subworkflow)
			if err != nil {
				return Definition{}, err
			}
			pinned := *node.Map
			pinned.VersionID = versionID
			node.Map = &pinned
			nodes = append(nodes, node)
			continue
		}
		if node.Type != "subworkflow" {
			nodes = append(nodes, node)
			continue
		}
		if node.Subworkflow == nil {
			return Definition{}, fmt.Errorf("subworkflow node %q requires a subworkflow reference", node.ID)
		}
		sub, _, err := s.pinnedSubworkflow(*node.Subworkflow)
		if err != nil {
			return Definition{}, err
		}
		for _, child := range sub.Nodes {
			child.ID = subPrefix(node.ID, child.ID)
			nodes = append(nodes, child)
		}
		for _, dep := range sub.Dependencies {
			deps = append(deps, Dependency{From: subPrefix(node.ID, dep.From), To: subPrefix(node.ID, dep.To)})
		}
		// A subworkflow node's incoming/outgoing edges connect to the
		// subworkflow's roots (no upstream inside it) and leaves (no
		// downstream inside it) respectively so the pipeline stays intact.
		roots, leaves := subRootsAndLeaves(sub)
		for _, dep := range definition.Dependencies {
			if dep.To == node.ID {
				for _, root := range roots {
					deps = append(deps, Dependency{From: dep.From, To: subPrefix(node.ID, root)})
				}
			}
			if dep.From == node.ID {
				for _, leaf := range leaves {
					deps = append(deps, Dependency{From: subPrefix(node.ID, leaf), To: dep.To})
				}
			}
		}
	}
	// Carry over edges that do not touch a subworkflow node.
	subIDs := map[string]bool{}
	for _, node := range definition.Nodes {
		if node.Type == "subworkflow" {
			subIDs[node.ID] = true
		}
	}
	for _, dep := range definition.Dependencies {
		if subIDs[dep.From] || subIDs[dep.To] {
			continue
		}
		deps = append(deps, dep)
	}
	definition.Nodes = nodes
	definition.Dependencies = deps
	// Preserve the pre-inline reference graph so a later publish can
	// detect an indirect cycle even though the subworkflow nodes are gone.
	definition.SubworkflowRefs = dedupeRefs(authoredRefs)
	return definition, nil
}

func dedupeRefs(refs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ref := range refs {
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

// subRootsAndLeaves returns the ids of a subworkflow's root nodes (no
// incoming edge) and leaf nodes (no outgoing edge). Used to splice a
// subworkflow's boundary onto the parent's edges during inlining.
func subRootsAndLeaves(sub Definition) (roots, leaves []string) {
	hasIn := map[string]bool{}
	hasOut := map[string]bool{}
	for _, dep := range sub.Dependencies {
		hasIn[dep.To] = true
		hasOut[dep.From] = true
	}
	for _, node := range sub.Nodes {
		if !hasIn[node.ID] {
			roots = append(roots, node.ID)
		}
		if !hasOut[node.ID] {
			leaves = append(leaves, node.ID)
		}
	}
	return roots, leaves
}

// checkSubworkflowRecursion rejects direct and indirect recursive
// subworkflow references reachable from the definition being published.
// It walks the reference graph: every subworkflow/map node names a
// workflow id, and each referenced (already-inlined) version exposes its
// own remaining references through the metadata edges recorded at its
// publish time. Because a published version is acyclic, a cycle can only
// be introduced by the new definition, so a DFS from the new definition's
// direct references that returns to its own id is a recursion.
func (s *Service) checkSubworkflowRecursion(definition Definition) error {
	visiting := map[string]bool{definition.ID: true}
	var walk func(workflowID string, path []string) error
	walk = func(workflowID string, path []string) error {
		refs, err := s.subworkflowRefs(workflowID)
		if err != nil {
			return err
		}
		for _, ref := range refs {
			if ref == definition.ID || visiting[ref] {
				return fmt.Errorf("recursive subworkflow cycle through %q", ref)
			}
			visiting[ref] = true
			if err := walk(ref, append(path, ref)); err != nil {
				return err
			}
			delete(visiting, ref)
		}
		return nil
	}
	for _, ref := range definitionRefs(definition) {
		if ref == definition.ID {
			return fmt.Errorf("recursive subworkflow cycle through %q", ref)
		}
		visiting[ref] = true
		if err := walk(ref, []string{ref}); err != nil {
			return err
		}
		delete(visiting, ref)
	}
	return nil
}

// definitionRefs returns the workflow ids a definition references through
// subworkflow and map nodes.
func definitionRefs(definition Definition) []string {
	var refs []string
	for _, node := range definition.Nodes {
		switch {
		case node.Type == "subworkflow" && node.Subworkflow != nil:
			refs = append(refs, node.Subworkflow.WorkflowID)
		case node.Type == "map" && node.Map != nil:
			refs = append(refs, node.Map.Subworkflow.WorkflowID)
		}
	}
	return refs
}

// subworkflowRefs returns the workflow ids referenced by a stored
// workflow's active version. A stored subworkflow version is already
// inlined, so its only remaining references are map-node per-item
// subworkflows recorded in the definition JSON.
func (s *Service) subworkflowRefs(workflowID string) ([]string, error) {
	row, err := s.store.GetActiveWorkflowVersion(workflowID)
	if err != nil {
		return nil, fmt.Errorf("subworkflow %q has no active version: %w", workflowID, err)
	}
	var definition Definition
	if err := json.Unmarshal([]byte(row.DefinitionJSON), &definition); err != nil {
		return nil, fmt.Errorf("decoding subworkflow %q: %w", workflowID, err)
	}
	return definition.SubworkflowRefs, nil
}

// validateAuthoredDefinition validates the structure of composition
// nodes (subworkflow / map / join) on the authored graph before
// inlining. Base node types are validated after inlining by
// validateDefinition against the fully expanded graph.
func validateAuthoredDefinition(definition Definition) error {
	ids := map[string]bool{}
	for _, node := range definition.Nodes {
		if node.ID != "" {
			ids[node.ID] = true
		}
	}
	for _, node := range definition.Nodes {
		switch node.Type {
		case "subworkflow":
			if node.Subworkflow == nil || node.Subworkflow.WorkflowID == "" {
				return fmt.Errorf("subworkflow node %q requires a workflowId", node.ID)
			}
		case "map":
			if err := validateMapNode(node, ids); err != nil {
				return err
			}
		case "join":
			if err := validateJoinNode(node); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateMapNode checks a map node declares an input source, a stable
// key field, a per-item subworkflow, and a join node that exists.
func validateMapNode(node Node, ids map[string]bool) error {
	m := node.Map
	if m == nil {
		return fmt.Errorf("map node %q requires a map configuration", node.ID)
	}
	if m.Source == "" {
		return fmt.Errorf("map node %q requires an input source artifact", node.ID)
	}
	if m.Key == "" {
		return fmt.Errorf("map node %q requires a stable key field", node.ID)
	}
	if m.Subworkflow.WorkflowID == "" {
		return fmt.Errorf("map node %q requires a per-item subworkflow", node.ID)
	}
	if m.Join == "" {
		return fmt.Errorf("map node %q requires a join node", node.ID)
	}
	if !ids[m.Join] {
		return fmt.Errorf("map node %q references missing join node %q", node.ID, m.Join)
	}
	return nil
}

// validateJoinNode checks a join node declares a supported success
// policy (and a positive minimum for minimum-success).
func validateJoinNode(node Node) error {
	j := node.Join
	if j == nil {
		return fmt.Errorf("join node %q requires a join configuration", node.ID)
	}
	switch j.Policy {
	case JoinAllSuccess, JoinAlways:
	case JoinMinimumSuccess:
		if j.MinSuccess <= 0 {
			return fmt.Errorf("join node %q minimumSuccess must be positive", node.ID)
		}
	default:
		return fmt.Errorf("join node %q has unsupported policy %q", node.ID, j.Policy)
	}
	return nil
}

// versionNodeRows builds the denormalized node/dependency rows persisted
// alongside a version from a (already inlined) definition.
func versionNodeRows(definition Definition) ([]state.WorkflowNode, []state.WorkflowDependency) {
	nodes := make([]state.WorkflowNode, 0, len(definition.Nodes))
	for position, node := range definition.Nodes {
		nodes = append(nodes, state.WorkflowNode{ID: node.ID, Name: node.Name, Type: node.Type, Position: position})
	}
	deps := make([]state.WorkflowDependency, 0, len(definition.Dependencies))
	for _, dep := range definition.Dependencies {
		deps = append(deps, state.WorkflowDependency{From: dep.From, To: dep.To})
	}
	return nodes, deps
}
