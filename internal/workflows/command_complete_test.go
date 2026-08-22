package workflows

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

// failingCompleteStore wraps a real Store and fails
// CompleteWorkflowCommand a fixed number of times.
type failingCompleteStore struct {
	Store
	remaining int
	calls     int
	unknown   int
}

func (f *failingCompleteStore) CompleteWorkflowCommand(ctx context.Context, runID, nodeID string, result state.WorkflowCommandResult, now int64) error {
	f.calls++
	if f.remaining > 0 {
		f.remaining--
		return errors.New("database is locked")
	}
	return f.Store.CompleteWorkflowCommand(ctx, runID, nodeID, result, now)
}

func (f *failingCompleteStore) MarkWorkflowAttemptUnknown(ctx context.Context, runID, nodeID string, attemptID int64, reason string, now int64) error {
	f.unknown++
	return f.Store.MarkWorkflowAttemptUnknown(ctx, runID, nodeID, attemptID, reason, now)
}

type okExecutor struct{}

func (okExecutor) Execute(context.Context, CommandRequest) CommandResult {
	return CommandResult{State: AttemptSuccessful, ExitCode: 0, Stdout: `{"ok":true}`}
}

// runOneCommandNode publishes and drives a single-command workflow to a
// terminal node state, returning the run detail.
func runOneCommandNode(t *testing.T, h *harness) RunDetail {
	t.Helper()
	directory := t.TempDir()
	version, err := h.svc.PublishJSON(t.Context(), commandDefinition(t, directory, []Node{
		{ID: "run", Name: "Run", Type: "command", Command: []string{"work"},
			Permission: []PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}},
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	run, err := h.svc.Start(t.Context(), version.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := h.svc.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
		detail, err := h.svc.GetRun(t.Context(), run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Nodes[0].State != NodeReady && detail.Nodes[0].State != NodePending {
			return detail
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("command node never reached a terminal state")
	return RunDetail{}
}

// TestCompleteCommandAttemptRetries covers the transient case. The
// terminal write is the only transition out of the running attempt
// state, so a lost error strands the node's capacity forever.
func TestCompleteCommandAttemptRetries(t *testing.T) {
	h := newHarness(t)
	store := &failingCompleteStore{Store: h.db, remaining: 2}
	h.svc = NewService(Deps{Store: store, CommandExecutor: okExecutor{}, Blobs: h.blobs, Now: h.clock})

	detail := runOneCommandNode(t, h)

	if store.calls != 3 {
		t.Errorf("CompleteWorkflowCommand calls = %d, want 3 (two failures then a success)", store.calls)
	}
	if store.unknown != 0 {
		t.Errorf("marked unknown %d times, want 0 — the retry succeeded", store.unknown)
	}
	if detail.Nodes[0].State != NodeSuccessful {
		t.Errorf("node state = %q, want %q", detail.Nodes[0].State, NodeSuccessful)
	}
}

// TestCompleteCommandAttemptFallsBackToUnknown covers the persistent
// case. Discarding the error left the node running until an ocman
// restart triggered recoverInterrupted — the run stalled silently with
// no signal anywhere. Marking the attempt unknown releases its leases
// and pauses the run so a human is asked to resolve it.
func TestCompleteCommandAttemptFallsBackToUnknown(t *testing.T) {
	h := newHarness(t)
	store := &failingCompleteStore{Store: h.db, remaining: 99}
	h.svc = NewService(Deps{Store: store, CommandExecutor: okExecutor{}, Blobs: h.blobs, Now: h.clock})

	detail := runOneCommandNode(t, h)

	if store.calls != commandCompleteAttempts {
		t.Errorf("CompleteWorkflowCommand calls = %d, want %d", store.calls, commandCompleteAttempts)
	}
	if store.unknown != 1 {
		t.Fatalf("marked unknown %d times, want 1", store.unknown)
	}
	if detail.Nodes[0].State != NodeUnknown {
		t.Errorf("node state = %q, want %q — the node must not stay running", detail.Nodes[0].State, NodeUnknown)
	}
	if detail.State != StatePaused {
		t.Errorf("run state = %q, want %q", detail.State, StatePaused)
	}
	attempts := detail.Nodes[0].Attempts
	last := attempts[len(attempts)-1]
	if !strings.Contains(last.Error, "recording command completion failed") {
		t.Errorf("attempt error = %q, want it to name the failed completion", last.Error)
	}
}
