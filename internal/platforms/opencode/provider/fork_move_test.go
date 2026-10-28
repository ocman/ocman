package opencode

import (
	"context"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

// The ForkSession / MoveSession request contracts and their failure
// mapping are pinned by the mutatingOps tables in
// operations_more_test.go.

// TestAdapterForkSession_BadResponse errors when OpenCode returns no ID.
func TestAdapterForkSession_BadResponse(t *testing.T) {
	const sid = "ses_fork_bad"
	const dir = "/tmp/proj-fork-bad"

	port := fakePort(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	withTestPort(t, dir, port)
	a := New(newBoundTestDBWithSession(t, sid, dir), nil, nil, ocapi.Auth{})

	if _, err := a.ForkSession(context.Background(), platforms.ForkSessionRequest{SessionID: sid}); err == nil {
		t.Error("ForkSession: expected error for response with no id")
	}
}
