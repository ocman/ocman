package remote

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/google/uuid"
)

func TestCanonicalWorktreeReplyCannotPublishNativeHandleOrReplayUncertainCreation(t *testing.T) {
	host := newRemoteHost(&RemoteConn{protocolVersion: canonicalSessionProtocol})
	for _, payload := range []string{`{"sessionId":"native","worktreePath":"/repo/worktree"}`, `invalid JSON`} {
		result, err := host.decodeWorktreeSession([]byte(payload))
		var pending *platforms.CreationPendingError
		if result != nil || !errors.As(err, &pending) || pending.SessionID != "" {
			t.Fatalf("invalid result gained authority or replayed: %+v %v", result, err)
		}
	}
	id := "ocm_" + uuid.NewString()
	result, err := host.decodeWorktreeSession([]byte(`{"sessionId":"` + id + `","worktreePath":"/repo/worktree","reused":true}`))
	if err != nil || result.SessionID != id || result.WorktreePath != "/repo/worktree" || !result.Reused {
		t.Fatalf("canonical result changed: %+v %v", result, err)
	}
}
