package sessionsvc

import (
	"fmt"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

// MCP creates supply model/agent metadata without a request key, both directly
// and through the host's worktree-session callback.
func TestNamedCreationWithPartialIdentity(t *testing.T) {
	r := newConnectionRig(t)
	selected := r.connections[1]
	identity := &platforms.CreationIdentity{Origin: "user", Model: "p/model", Agent: "plan"}
	var ids, keys []string
	for i := range 2 {
		r.native[1].createResp = &platforms.CreateSessionResponse{ID: fmt.Sprintf("native-%d", i)}
		response, err := r.svc.Create(t.Context(), selected.ID, platforms.CreateSessionRequest{
			Directory: "/repo", Creation: identity,
		})
		if err != nil {
			t.Fatalf("create with MCP-style identity failed: %v", err)
		}
		ids = append(ids, response.ID)
		native := r.native[1].creates[len(r.native[1].creates)-1].Creation
		keys = append(keys, native.RequestKey)
		if native.RequestKey == "" || native.Model != identity.Model || native.Agent != identity.Agent || native.Origin != identity.Origin {
			t.Fatalf("creation metadata lost: %+v", native)
		}
		connection, err := r.store.SessionConnection(t.Context(), response.ID)
		if err != nil || connection != selected.ID {
			t.Fatalf("wrong connection: %q, %v", connection, err)
		}
	}
	if ids[0] == ids[1] || keys[0] == keys[1] {
		t.Fatal("independent creates reused an identity or request key")
	}
	if identity.RequestKey != "" || identity.SessionID != "" || len(r.native[0].creates) != 0 {
		t.Fatal("creation mutated caller metadata or fell back to the default account")
	}
}
