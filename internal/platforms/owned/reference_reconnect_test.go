package owned

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/google/uuid"
)

func TestReconnectBindsOwnerProofWithoutRekeyingActiveState(t *testing.T) {
	core, refs, remote, gateway := restoreFixture(t)
	scope := string(remote.ID())
	if err := core.PinSession(t.Context(), scope, "native"); err != nil {
		t.Fatal(err)
	}
	if err := core.EnqueueMessage(t.Context(), state.QueuedMessage{ID: "queue", Platform: scope, SessionID: "native", Text: "retained"}); err != nil {
		t.Fatal(err)
	}
	if err := refs.QuarantineState(t.Context(), core, scope); err != nil {
		t.Fatal(err)
	}
	pending, err := core.SessionReferences(t.Context(), scope)
	if err != nil || len(pending) != 1 {
		t.Fatalf("quarantine: %+v %v", pending, err)
	}
	remote.err = platforms.ErrPlatformUnreachable
	if bound, err := refs.ResolvePending(t.Context(), gateway, scope); bound != 0 || !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("offline proof: %d %v", bound, err)
	}
	id := "ocm_" + uuid.NewString()
	remote.err = nil
	remote.result = map[string]platforms.SessionIdentity{"native": {ID: id, OwnerID: "owner", Platform: "test", SourceKey: "source", Origin: string(sessioncatalog.External), CreationState: string(sessioncatalog.Ready)}}
	if bound, err := refs.ResolvePending(t.Context(), gateway, scope); bound != 1 || err != nil {
		t.Fatalf("owner proof: %d %v", bound, err)
	}
	active, err := core.SessionReferences(t.Context(), scope)
	if err != nil || len(active) != 1 || active[0] != pending[0] {
		t.Fatalf("live recovery rekeyed active state: %+v %v", active, err)
	}
	queue, err := core.ListQueuedMessages(t.Context(), scope, pending[0])
	if err != nil || len(queue) != 1 || queue[0].Text != "retained" {
		t.Fatalf("live recovery changed held work: %+v %v", queue, err)
	}
	calls := remote.calls
	remote.err = platforms.ErrPlatformUnreachable
	if bound, err := refs.ResolvePending(t.Context(), gateway, scope); err != nil || bound != 0 || remote.calls != calls {
		t.Fatalf("immutable proof was requested again: %d %v", bound, err)
	}
	// Simulate the next pre-serving migration with the owner offline again.
	if err := refs.QuarantineState(t.Context(), core, scope); err != nil {
		t.Fatal(err)
	}
	queue, err = core.ListQueuedMessages(t.Context(), scope, id)
	if err != nil || len(queue) != 1 || queue[0].ID != "queue" {
		t.Fatalf("startup failed to apply durable proof: %+v %v", queue, err)
	}
	if _, err := refs.ResolvePending(t.Context(), gateway, ""); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("unattributed proof accepted: %v", err)
	}
}
