package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

// The run mirror is how a Dagu-executed run reaches the UI. Ocman polls
// rather than taking callbacks from Dagu: polling survives an ocman
// restart mid-run, cannot miss an event, and yields incremental per-step
// state that a completion hook could not.
const workflowMirrorTickInterval = 2 * time.Second

// runnerDagu labels runs driven by the Dagu runner.
const runnerDagu = "dagu"

// mirrorRunState maps a Dagu run status onto an ocman run state. Only
// terminal Dagu states settle a run; anything else leaves it active, so
// an unrecognised label can never strand a run in a terminal state.
func mirrorRunState(status string) string {
	switch status {
	case "succeeded":
		return workflows.StateSuccessful
	case "failed", "partial success":
		return workflows.StateFailed
	case "canceled", "cancelled", "aborted":
		return workflows.StateCanceled
	default:
		return workflows.StateActive
	}
}

// mirrorNodeState maps a Dagu step status onto an ocman node state.
func mirrorNodeState(status string) string {
	switch status {
	case "succeeded":
		return workflows.NodeSuccessful
	case "failed":
		return workflows.NodeFailed
	case "skipped":
		return workflows.NodeSkipped
	case "running":
		return workflows.NodeRunning
	case "canceled", "cancelled", "aborted":
		return workflows.NodeCanceled
	case "not_started", "queued", "":
		return workflows.NodePending
	default:
		// An unknown label is surfaced rather than guessed: the run view
		// has an explicit resolve-unknown path for exactly this case.
		return workflows.NodeUnknown
	}
}

// mirrorSnapshot converts a Dagu run into the projection ocman stores.
func mirrorSnapshot(run dagu.Run) state.WorkflowMirrorSnapshot {
	snapshot := state.WorkflowMirrorSnapshot{
		State:       mirrorRunState(run.Status),
		CompletedAt: run.FinishedAt,
	}
	for _, node := range run.Nodes {
		snapshot.Nodes = append(snapshot.Nodes, state.WorkflowMirrorNode{
			NodeID:      node.Name,
			State:       mirrorNodeState(node.Status),
			StartedAt:   node.StartedAt,
			CompletedAt: node.FinishedAt,
			Stdout:      node.Log,
			Error:       node.Error,
		})
	}
	return snapshot
}

// runWorkflowMirror keeps ocman's run rows in step with the external
// runner for as long as a run is active.
func (s *Server) runWorkflowMirror(ctx context.Context) {
	if s.stateDB == nil || s.daguManager == nil {
		return
	}
	ticker := time.NewTicker(workflowMirrorTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("workflow-mirror", func() { s.mirrorActiveRuns(ctx) })
		}
	}
}

func (s *Server) mirrorActiveRuns(ctx context.Context) {
	active, err := s.stateDB.ListActiveExternalWorkflowRuns()
	if err != nil {
		log.WithError(err).Warn("workflow-mirror: list active runs")
		return
	}
	for _, run := range active {
		if run.Runner != runnerDagu {
			continue
		}
		s.mirrorRun(ctx, run)
	}
}

func (s *Server) mirrorRun(ctx context.Context, run state.ExternalWorkflowRun) {
	detail, err := s.daguManager.GetRun(ctx, run.WorkflowID, run.ExternalID)
	if err != nil {
		// A transient runner outage must not settle the run; the next
		// tick retries.
		log.WithError(err).WithField("run", run.RunID).Debug("workflow-mirror: read external run")
		return
	}
	changed, err := s.stateDB.MirrorWorkflowRun(run.RunID, mirrorSnapshot(detail), time.Now().UnixMilli())
	if err != nil {
		log.WithError(err).WithField("run", run.RunID).Warn("workflow-mirror: apply snapshot")
		return
	}
	if changed {
		s.broadcastWorkflowRunUpdated(run.RunID)
	}
}

// loopbackEndpoint turns a listen address into a URL the workflow-step
// shim can reach. A wildcard or empty host binds every interface, so
// loopback is always a valid way back in and keeps the callback off the
// network.
func loopbackEndpoint(addr string) string {
	_, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || port == "" {
		return "http://127.0.0.1:8229"
	}
	return "http://127.0.0.1:" + port
}

// daguRunner routes a freshly created run to Dagu when the compiler can
// express it. Ocman still owns authoring, versioning, triggering, and
// history; Dagu only executes the graph.
//
// A definition the compiler rejects is declined rather than failed, so
// features Dagu cannot yet express — resource pools, managed
// workspaces, path leases, secrets, run limits, fail-fast, and repeat —
// keep running on ocman's own dispatcher.
type daguRunner struct{ s *Server }

func (r daguRunner) StartRun(ctx context.Context, runID string, definition workflows.Definition) (bool, error) {
	if r.s.daguManager == nil || r.s.stateDB == nil {
		return false, nil
	}
	compiled, err := r.s.daguManager.CompileRun(definition, runID)
	if err != nil {
		log.WithError(err).WithField("workflow", definition.ID).
			Debug("workflow-runner: definition not expressible in Dagu, using the native dispatcher")
		return false, nil
	}
	run, err := r.s.daguManager.StartCompiled(ctx, definition.ID, runID, compiled)
	if err != nil {
		// The runner was supposed to take this run and could not. Failing
		// is right: silently falling back would run an agent twice if the
		// handoff turns out to have partly succeeded.
		return false, fmt.Errorf("start run on Dagu: %w", err)
	}
	if err := r.s.stateDB.SetWorkflowRunExternal(runID, run.ID, runnerDagu, time.Now().UnixMilli()); err != nil {
		return false, fmt.Errorf("link run to Dagu execution: %w", err)
	}
	return true, nil
}

// workflowVersionDefinition resolves a pinned version id to its
// immutable definition, which is how a map node's per-item child is
// compiled.
func (s *Server) workflowVersionDefinition(versionID string) (workflows.Definition, error) {
	if s.stateDB == nil {
		return workflows.Definition{}, fmt.Errorf("state database is unavailable")
	}
	version, err := s.stateDB.GetWorkflowVersion(versionID)
	if err != nil || version == nil {
		return workflows.Definition{}, fmt.Errorf("workflow version %q not found", versionID)
	}
	var definition workflows.Definition
	if err := json.Unmarshal([]byte(version.DefinitionJSON), &definition); err != nil {
		return workflows.Definition{}, fmt.Errorf("reading definition %q: %w", versionID, err)
	}
	return definition, nil
}
