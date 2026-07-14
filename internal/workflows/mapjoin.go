package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/NoUseFreak/ocman/internal/state"
)

// JoinItem is one mapped item's outcome in a join result. Key is the
// stable key; State is its terminal node/run state (successful/failed/
// canceled); Index is its input position.
type JoinItem struct {
	Key   string `json:"key"`
	Index int    `json:"index"`
	State string `json:"state"`
}

// JoinResult is a join node's aggregated, input-ordered output plus the
// success policy that decided the join's own outcome.
type JoinResult struct {
	Policy  string     `json:"policy"`
	Success int        `json:"success"`
	Failed  int        `json:"failed"`
	Total   int        `json:"total"`
	Items   []JoinItem `json:"items"`
}

// itemPlaceholder matches ${item.field} references in per-item agent
// prompts, substituted from each mapped item's payload at launch time.
var itemPlaceholder = regexp.MustCompile(`\$\{item\.([A-Za-z0-9_]+)\}`)

// driveMapNodes advances every ready/running map node in the run: it
// expands the declared JSON array into per-item child runs (idempotent on
// stable key), reconciles finished child runs, and settles the map node
// once all items are terminal. Returns whether any progress was made so
// the dispatch loop knows to re-evaluate readiness. Called under the
// dispatch lock.
func (s *Service) driveMapNodes(ctx context.Context, runID string) (bool, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil || run.State != StateActive {
		return false, err
	}
	progressed := false
	for _, node := range run.Nodes {
		definition := mapConfig(run.Version.Definition.Nodes, node.NodeID)
		switch {
		case definition != nil && (node.State == NodeReady || node.State == NodeRunning):
			moved, err := s.driveMapNode(ctx, run, node, definition)
			if err != nil {
				return progressed, err
			}
			progressed = progressed || moved
		case node.Type == "join" && node.State == NodeReady:
			moved, err := s.settleJoinNode(ctx, run, node)
			if err != nil {
				return progressed, err
			}
			progressed = progressed || moved
		}
	}
	return progressed, nil
}

func (s *Service) driveMapNode(ctx context.Context, run RunDetail, node NodeRun, config *MapConfig) (bool, error) {
	if len(node.Attempts) == 0 {
		return false, nil
	}
	attempt := node.Attempts[len(node.Attempts)-1]
	// First entry: hold the parent run-concurrency slot and expand.
	if node.State == NodeReady && attempt.State == AttemptWaiting {
		started, err := s.store.StartWorkflowNode(run.ID, node.NodeID, resourceRequests(run.Version.Definition, node.NodeID), s.now().UnixMilli())
		if err != nil || !started {
			return false, err
		}
		if err := s.expandMap(ctx, run, node, config); err != nil {
			// A structural error (missing array, duplicate keys) fails the
			// map node — and thus the run — with a visible reason.
			if settleErr := s.store.SettleWorkflowNode(run.ID, node.NodeID, attempt.ID, false, "{}", err.Error(), s.now().UnixMilli()); settleErr != nil {
				return false, settleErr
			}
			s.changed(run.ID)
			return true, nil
		}
		s.changed(run.ID)
		return true, nil
	}
	// A restart can interrupt expansion after the map went running but
	// before every item was created. Re-expand idempotently (existing
	// stable keys are skipped) so no declared item is ever dropped.
	if err := s.expandMap(ctx, run, node, config); err != nil {
		if settleErr := s.store.SettleWorkflowNode(run.ID, node.NodeID, attempt.ID, false, "{}", err.Error(), s.now().UnixMilli()); settleErr != nil {
			return false, settleErr
		}
		s.changed(run.ID)
		return true, nil
	}
	// Reconcile child runs and settle the map when every item is terminal.
	return s.reconcileMap(ctx, run, node, config, attempt)
}

// expandMap reads the declared JSON array, validates unique stable keys,
// and creates a child run per item (idempotent on the stable key so a
// restart never duplicates completed work).
//
// ponytail: each item runs as its own isolated child workflow_run holding
// the parent's map node concurrency slot while active; per-item child
// sessions are not yet folded into the parent's cost/token/duration budget
// aggregation (budgetExceeded counts only same-run sessions). Add cross-run
// budget rollup when a real map campaign needs a hard descendant cost cap.
func (s *Service) expandMap(ctx context.Context, run RunDetail, node NodeRun, config *MapConfig) error {
	array, err := s.mapInputArray(ctx, run.ID, node.NodeID, config)
	if err != nil {
		return err
	}
	// Fast path: once every declared item exists (the common case on
	// reconcile ticks and after a completed expansion), skip re-creating
	// them. Duplicate-key validation already ran on first expansion.
	existing, err := s.store.ListWorkflowMapItems(run.ID, node.NodeID)
	if err != nil {
		return err
	}
	if len(existing) == len(array) {
		return nil
	}
	seen := map[string]bool{}
	pinned, err := s.store.GetWorkflowVersion(config.VersionID)
	if err != nil {
		return fmt.Errorf("pinned per-item subworkflow unavailable: %w", err)
	}
	for index, raw := range array {
		key, err := stableKey(raw, config.Key)
		if err != nil {
			return err
		}
		if seen[key] {
			return fmt.Errorf("duplicate stable key %q in map input", key)
		}
		seen[key] = true
		now := s.now().UnixMilli()
		child := s.newChildRun(*pinned, run.ID, node.NodeID, key, index, now)
		created, err := s.store.CreateWorkflowMapItem(state.WorkflowMapItem{
			RunID: run.ID, MapNode: node.NodeID, ItemKey: key, ItemIndex: index,
			ChildRunID: child.ID, State: NodeRunning, CreatedAt: now,
		}, child)
		if err != nil {
			return err
		}
		if created {
			// Seed the item payload as an artifact the per-item pipeline
			// consumes; ${item.*} placeholders in agent prompts are
			// substituted from it at launch time.
			s.storeArtifact(child.ID, node.NodeID, 0, "item", KindJSON, raw, 0)
		}
	}
	return nil
}

// reconcileMap updates each item's recorded state from its child run and,
// once all items are terminal, settles the map node. Fail-fast fails the
// run on the first failed item.
func (s *Service) reconcileMap(ctx context.Context, run RunDetail, node NodeRun, config *MapConfig, attempt Attempt) (bool, error) {
	items, err := s.store.ListWorkflowMapItems(run.ID, node.NodeID)
	if err != nil {
		return false, err
	}
	allTerminal := true
	anyFailed := false
	for _, item := range items {
		childState := item.State
		if item.ChildRunID != "" {
			child, err := s.store.GetWorkflowRun(item.ChildRunID)
			if err == nil {
				childState = child.State
			}
		}
		terminal := childState == StateSuccessful || childState == StateFailed || childState == StateCanceled
		if terminal && childState != item.State {
			if err := s.store.SetWorkflowMapItemState(run.ID, node.NodeID, item.ItemKey, childState); err != nil {
				return false, err
			}
		}
		if !terminal {
			allTerminal = false
		}
		if childState == StateFailed || childState == StateCanceled {
			anyFailed = true
		}
	}
	if config.FailFast && anyFailed {
		// Stop unrelated in-flight items immediately.
		s.cancelChildRuns(ctx, run.ID)
		if err := s.store.SettleWorkflowNode(run.ID, node.NodeID, attempt.ID, false, "{}", "map stopped by fail-fast after an item failed", s.now().UnixMilli()); err != nil {
			return false, err
		}
		s.changed(run.ID)
		return true, nil
	}
	if !allTerminal {
		return false, nil
	}
	// The map node itself always settles successfully once items finish;
	// the join node applies the success policy over the per-item outcomes.
	if err := s.store.SettleWorkflowNode(run.ID, node.NodeID, attempt.ID, true, "{}", "", s.now().UnixMilli()); err != nil {
		return false, err
	}
	s.changed(run.ID)
	return true, nil
}

// settleJoinNode aggregates the upstream map's per-item outcomes in input
// order and applies the join's success policy.
func (s *Service) settleJoinNode(ctx context.Context, run RunDetail, node NodeRun) (bool, error) {
	if len(node.Attempts) == 0 {
		return false, nil
	}
	attempt := node.Attempts[len(node.Attempts)-1]
	if attempt.State != AttemptWaiting {
		return false, nil
	}
	config := joinConfig(run.Version.Definition.Nodes, node.NodeID)
	if config == nil {
		return false, nil
	}
	mapNode := mapNodeForJoin(run.Version.Definition.Nodes, node.NodeID)
	items, err := s.store.ListWorkflowMapItems(run.ID, mapNode)
	if err != nil {
		return false, err
	}
	result := JoinResult{Policy: config.Policy, Total: len(items)}
	for _, item := range items {
		state := item.State
		if state == StateSuccessful {
			result.Success++
		} else {
			result.Failed++
		}
		result.Items = append(result.Items, JoinItem{Key: item.ItemKey, Index: item.ItemIndex, State: state})
	}
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].Index < result.Items[j].Index })
	success := joinSucceeds(config, result)
	payload, err := json.Marshal(map[string]JoinResult{"result": result})
	if err != nil {
		return false, err
	}
	errMsg := ""
	if !success {
		errMsg = fmt.Sprintf("join policy %q not satisfied (%d/%d succeeded)", config.Policy, result.Success, result.Total)
	}
	if err := s.store.SettleWorkflowNode(run.ID, node.NodeID, attempt.ID, success, string(payload), errMsg, s.now().UnixMilli()); err != nil {
		return false, err
	}
	// Publish the join result as a consumable artifact for downstream nodes.
	if success {
		encoded, _ := json.Marshal(result)
		s.storeArtifact(run.ID, node.NodeID, attempt.ID, "result", KindJSON, encoded, run.Version.Definition.RetentionDays)
	}
	s.changed(run.ID)
	return true, nil
}

// joinSucceeds applies the join success policy to the aggregated result.
func joinSucceeds(config *JoinConfig, result JoinResult) bool {
	switch config.Policy {
	case JoinAlways:
		return true
	case JoinMinimumSuccess:
		return result.Success >= config.MinSuccess
	default: // all-success
		return result.Failed == 0
	}
}

// mapInputArray resolves the declared JSON array artifact the map consumes
// from its upstream dependencies.
func (s *Service) mapInputArray(ctx context.Context, runID, nodeID string, config *MapConfig) ([]json.RawMessage, error) {
	artifacts, err := s.ConsumableArtifacts(ctx, runID, nodeID)
	if err != nil {
		return nil, err
	}
	var chosen *Artifact
	for i := range artifacts {
		if artifacts[i].Name == config.Source {
			chosen = &artifacts[i]
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("map input artifact %q not produced by an upstream node", config.Source)
	}
	_, payload, err := s.DownloadArtifact(ctx, chosen.ID)
	if err != nil {
		return nil, fmt.Errorf("reading map input %q: %w", config.Source, err)
	}
	var array []json.RawMessage
	if err := json.Unmarshal(payload, &array); err != nil {
		return nil, fmt.Errorf("map input %q is not a JSON array: %w", config.Source, err)
	}
	return array, nil
}

// stableKey extracts the declared stable key field from one item object.
func stableKey(raw json.RawMessage, field string) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", fmt.Errorf("map item is not a JSON object: %w", err)
	}
	value, ok := object[field]
	if !ok {
		return "", fmt.Errorf("map item missing stable key field %q", field)
	}
	var asString string
	if err := json.Unmarshal(value, &asString); err == nil && asString != "" {
		return asString, nil
	}
	var asNumber json.Number
	if err := json.Unmarshal(value, &asNumber); err == nil && asNumber.String() != "" {
		return asNumber.String(), nil
	}
	return "", fmt.Errorf("map item stable key %q must be a non-empty string or number", field)
}

// newChildRun builds an isolated per-item run pinned to the map's
// subworkflow version, linked back to its parent map node.
func (s *Service) newChildRun(version state.WorkflowVersion, parentRunID, mapNode, key string, index int, now int64) state.WorkflowRun {
	dependencies := make(map[string]bool, len(version.Dependencies))
	for _, dep := range version.Dependencies {
		dependencies[dep.To] = true
	}
	run := state.WorkflowRun{
		ID: newID("wfr"), WorkflowID: version.WorkflowID, VersionID: version.ID,
		State: StateActive, CreatedAt: now, UpdatedAt: now,
		ParentRunID: parentRunID, ParentNodeID: mapNode, ItemKey: key, ItemIndex: index,
	}
	for _, node := range version.Nodes {
		nodeState := NodePending
		readyAt := int64(0)
		if !dependencies[node.ID] {
			nodeState = NodeReady
			readyAt = now
		}
		run.Nodes = append(run.Nodes, state.WorkflowNodeRun{NodeID: node.ID, Type: node.Type, Position: node.Position, State: nodeState, ReadyAt: readyAt})
	}
	return run
}

// cancelChildRuns cancels every still-active mapped-item child run of a
// parent so fail-fast / parent cancellation does not leave orphaned work.
// It cancels at the store level (and any live agent session) directly,
// without re-entering the dispatch lock, so it is safe to call from within
// a dispatch pass.
func (s *Service) cancelChildRuns(ctx context.Context, parentRunID string) {
	children, err := s.store.ListWorkflowChildRuns(parentRunID)
	if err != nil {
		return
	}
	for _, child := range children {
		if child.State != StateActive && child.State != StatePaused {
			continue
		}
		full, err := s.store.GetWorkflowRun(child.ID)
		if err == nil && s.agent != nil {
			for _, node := range full.Nodes {
				for _, attempt := range node.Attempts {
					if attempt.State == AttemptRunning && attempt.SessionID != "" {
						_ = s.agent.Cancel(ctx, AgentSession{ID: attempt.SessionID, Platform: attempt.Platform, State: attempt.SessionState, Directory: attempt.Directory})
					}
				}
			}
		}
		_ = s.store.SetWorkflowRunState(child.ID, child.State, StateCanceled, s.now().UnixMilli())
		s.changed(child.ID)
	}
}

func mapConfig(nodes []Node, id string) *MapConfig {
	for _, node := range nodes {
		if node.ID == id && node.Type == "map" {
			return node.Map
		}
	}
	return nil
}

func joinConfig(nodes []Node, id string) *JoinConfig {
	for _, node := range nodes {
		if node.ID == id && node.Type == "join" {
			return node.Join
		}
	}
	return nil
}

// mapNodeForJoin returns the map node id whose Join points at joinID.
func mapNodeForJoin(nodes []Node, joinID string) string {
	for _, node := range nodes {
		if node.Type == "map" && node.Map != nil && node.Map.Join == joinID {
			return node.ID
		}
	}
	return ""
}

// itemPrompt substitutes ${item.*} placeholders in a per-item agent
// prompt from the child run's seeded item payload. Non-child runs and
// prompts without placeholders are returned unchanged.
func (s *Service) itemPrompt(ctx context.Context, run RunDetail, prompt string) string {
	if run.ParentRunID == "" || !itemPlaceholder.MatchString(prompt) {
		return prompt
	}
	artifacts, err := s.ListArtifacts(ctx, run.ID)
	if err != nil {
		return prompt
	}
	for _, artifact := range artifacts {
		if artifact.Name != "item" {
			continue
		}
		_, payload, err := s.DownloadArtifact(ctx, artifact.ID)
		if err != nil {
			return prompt
		}
		return substituteItemPrompt(prompt, payload)
	}
	return prompt
}

// substituteItemPrompt replaces ${item.field} references in a per-item
// agent prompt with the mapped item's payload values.
func substituteItemPrompt(prompt string, payload []byte) string {
	if len(payload) == 0 || !itemPlaceholder.MatchString(prompt) {
		return prompt
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return prompt
	}
	return itemPlaceholder.ReplaceAllStringFunc(prompt, func(match string) string {
		field := itemPlaceholder.FindStringSubmatch(match)[1]
		raw, ok := object[field]
		if !ok {
			return match
		}
		var asString string
		if err := json.Unmarshal(raw, &asString); err == nil {
			return asString
		}
		return string(raw)
	})
}
