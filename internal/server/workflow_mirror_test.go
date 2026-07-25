package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

// An unrecognised runner status must never settle a run. Guessing
// "finished" would strand a still-executing run in a terminal state that
// nothing ever reopens.
func TestMirrorRunStateLeavesUnknownStatusesActive(t *testing.T) {
	for status, want := range map[string]string{
		"succeeded":       workflows.StateSuccessful,
		"failed":          workflows.StateFailed,
		"partial success": workflows.StateFailed,
		"canceled":        workflows.StateCanceled,
		"cancelled":       workflows.StateCanceled,
		"aborted":         workflows.StateCanceled,
		"running":         workflows.StateActive,
		"queued":          workflows.StateActive,
		"":                workflows.StateActive,
		"something new":   workflows.StateActive,
	} {
		if got := mirrorRunState(status); got != want {
			t.Errorf("mirrorRunState(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestMirrorNodeStateMapsDaguLabels(t *testing.T) {
	for status, want := range map[string]string{
		"succeeded":   workflows.NodeSuccessful,
		"failed":      workflows.NodeFailed,
		"skipped":     workflows.NodeSkipped,
		"running":     workflows.NodeRunning,
		"aborted":     workflows.NodeCanceled,
		"not_started": workflows.NodePending,
		"":            workflows.NodePending,
		// Surfaced, not guessed: the run view has an explicit
		// resolve-unknown path for this.
		"weird": workflows.NodeUnknown,
	} {
		if got := mirrorNodeState(status); got != want {
			t.Errorf("mirrorNodeState(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestMirrorSnapshotCarriesStepLogsAndTimings(t *testing.T) {
	snapshot := mirrorSnapshot(dagu.Run{
		Status: "failed", FinishedAt: 500,
		Nodes: []dagu.Node{
			{Name: "build", Status: "succeeded", StartedAt: 100, FinishedAt: 200, Log: "built\n"},
			{Name: "ship", Status: "failed", StartedAt: 200, FinishedAt: 300, Error: "boom"},
		},
	})
	if snapshot.State != workflows.StateFailed || snapshot.CompletedAt != 500 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if len(snapshot.Nodes) != 2 {
		t.Fatalf("nodes = %+v", snapshot.Nodes)
	}
	// The step name carries the ocman node id, which is what joins a
	// Dagu step back to its row.
	if snapshot.Nodes[0].NodeID != "build" || snapshot.Nodes[0].Stdout != "built\n" {
		t.Errorf("build = %+v", snapshot.Nodes[0])
	}
	if snapshot.Nodes[1].State != workflows.NodeFailed || snapshot.Nodes[1].Error != "boom" {
		t.Errorf("ship = %+v", snapshot.Nodes[1])
	}
}
