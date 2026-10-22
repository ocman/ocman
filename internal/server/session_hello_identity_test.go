package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/google/uuid"
)

func TestRemoteHelloPromptReplyKeepsOwnerAfterReconnect(t *testing.T) {
	s, local, _ := identityHTTPFixture(t)
	local.respondPermissionFn = func(platforms.RespondPermissionRequest) error {
		t.Fatal("remote hello prompt reply reached local native collision")
		return nil
	}
	ownedID := "ocm_" + uuid.NewString()
	remote := &taskIdentityPlatform{fakePlatform: &fakePlatform{id: "r-remote:test"}, info: platforms.SessionIdentity{ID: ownedID, OwnerID: "remote", Platform: "test", SourceKey: "remote-source", Origin: "external", CreationState: "ready"}}
	replies := 0
	remote.respondPermissionFn = func(req platforms.RespondPermissionRequest) error {
		if req.SessionID != ownedID || req.PermissionID != "permission" {
			t.Fatalf("wrong canonical prompt reply: %+v", req)
		}
		replies++
		return nil
	}
	s.registry.Register(remote)
	s.registry.RememberSessions("test", []db.Session{{ID: "native"}})
	s.registry.RememberSessions("r-remote:test", []db.Session{{ID: ownedID}})
	for range 2 {
		w := httptest.NewRecorder()
		s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			frame := `event: oce` + "\n" + `data: {"platform":"r-remote:test","sessionId":"SESSION","event":{"type":"stream.hello","overlay":{"prompts":[{"id":"permission","sessionId":"SESSION"}],"inflight":{"messages":[{"id":"message","sessionId":"SESSION"}],"parts":[{"id":"part","messageId":"message","sessionId":"SESSION","data":{"text":"native"}}]}}}}` + "\n\n"
			_, _ = fmt.Fprint(w, strings.ReplaceAll(frame, "SESSION", ownedID))
		})(w, httptest.NewRequest(http.MethodGet, "/api/session/native/stream?platform=r-remote:test", nil))
		if strings.Contains(w.Body.String(), `"sessionId":"native"`) || !strings.Contains(w.Body.String(), `"id":"part"`) || !strings.Contains(w.Body.String(), `"sessionId":"`+ownedID+`"`) {
			t.Fatalf("native hello overlay: %s", w.Body.String())
		}
		r := httptest.NewRequest(http.MethodPost, "/api/session/"+ownedID+"/permissions/permission", strings.NewReader(`{"reply":"reject"}`))
		reply := httptest.NewRecorder()
		s.withOwnedSessionPath(s.requireAuth(s.handleSessionPermission))(reply, r)
		if reply.Code != http.StatusNoContent {
			t.Fatalf("remote prompt reply failed: %d %s", reply.Code, reply.Body.String())
		}
	}
	if replies != 2 {
		t.Fatalf("remote prompt was not answered after each connect: %d", replies)
	}
}
