package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
)

func TestPRLinkChangesBroadcastSessionChange(t *testing.T) {
	s := testServer(t)
	s.registry.Register(&fakePlatform{PlatformID: "fake", SessionFn: func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id}}, nil
	}})
	seedPRWatch(t, s, "fake", "s1", true)
	payload, _ := json.Marshal(prworkflow.Link{Platform: "fake", SessionID: "s1", RemoteID: "local", Watching: true, Number: 42, Remote: forge.Remote{Host: "github.com", Repo: "team/repo"}})
	if _, err := s.stateDB.SQL().ExecContext(t.Context(), `UPDATE session_pull_request SET payload=?`, string(payload)); err != nil {
		t.Fatal(err)
	}
	sub, unsub := s.broadcastHub.subscribe()
	defer unsub()
	for _, action := range []string{"unwatch", "unlink"} {
		r := httptest.NewRequest(http.MethodPost, "/api/session/s1/pull-requests?platform=fake", strings.NewReader(`{"action":"`+action+`","host":"github.com","repo":"team/repo","number":42}`))
		w := httptest.NewRecorder()
		serveModule(s, s.prWorkflowModule(), w, r)
		if w.Code >= 300 {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
		events := sub.takeBatch()
		if len(events) != 1 || events[0].event != "ocman.session.changed" {
			t.Fatalf("%s events = %v", action, events)
		}
		var payload map[string]string
		if err := json.Unmarshal(events[0].data, &payload); err != nil || payload["platform"] != "fake" || payload["sessionID"] != "s1" {
			t.Fatalf("unexpected payload: %s, %v", events[0].data, err)
		}
	}
}
