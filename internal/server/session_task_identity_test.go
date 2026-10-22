package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/google/uuid"
)

type taskIdentityPlatform struct {
	*fakePlatform
	info platforms.SessionIdentity
}

func (*taskIdentityPlatform) SessionOwnerID() string    { return "remote" }
func (*taskIdentityPlatform) CanonicalSessionIDs() bool { return true }
func (p *taskIdentityPlatform) SessionIdentity(context.Context, platforms.SessionIdentityRequest) (*platforms.SessionIdentity, error) {
	return &p.info, nil
}
func (p *taskIdentityPlatform) SessionIdentities(context.Context, []string) ([]platforms.SessionIdentity, error) {
	return []platforms.SessionIdentity{p.info}, nil
}

func TestCanonicalTaskReadIgnoresOppositeOwnerNativeCache(t *testing.T) {
	s, local, _ := identityHTTPFixture(t)
	local.sessionDetailFn = func(string) (*platforms.SessionDetail, error) {
		t.Fatal("remote task was read from colliding local native ID")
		return nil, nil
	}
	remoteID := "ocm_" + uuid.NewString()
	remote := &taskIdentityPlatform{fakePlatform: &fakePlatform{id: "r-remote:test"}, info: platforms.SessionIdentity{ID: remoteID, OwnerID: "remote", Platform: "test", SourceKey: "remote-source", Origin: "external", CreationState: "ready"}}
	remote.sessionDetailFn = func(id string) (*platforms.SessionDetail, error) {
		if id != remoteID {
			t.Fatalf("remote adapter received noncanonical ID %s", id)
		}
		return &platforms.SessionDetail{Messages: []db.Message{{ID: "remote-message", SessionID: id}}, Parts: []db.Part{}}, nil
	}
	s.registry.Register(remote)
	s.registry.RememberSessions("test", []db.Session{{ID: "native"}})
	for _, id := range []string{remoteID, "native"} {
		w := httptest.NewRecorder()
		s.requireAuth(s.handleSessionTasks)(w, httptest.NewRequest(http.MethodGet, "/api/session/root/tasks?platform=r-remote:test&ids="+id, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"remote-message"`) || !strings.Contains(w.Body.String(), remoteID) {
			t.Fatalf("wrong task response: %d %s", w.Code, w.Body.String())
		}
	}
}
