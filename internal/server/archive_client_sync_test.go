package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestArchiveSessionNotifiesAllClients(t *testing.T) {
	for _, archived := range []bool{true, false} {
		t.Run(map[bool]string{true: "archive", false: "reopen"}[archived], func(t *testing.T) {
			registry := platforms.NewRegistry()
			registry.Register(&fakePlatform{id: "r-box:opencode"})
			srv := &Server{stateDB: openTestStateDB(t), registry: registry, broadcastHub: newBroadcastHub()}
			first, stopFirst := srv.broadcastHub.subscribe()
			defer stopFirst()
			second, stopSecond := srv.broadcastHub.subscribe()
			defer stopSecond()
			body, _ := json.Marshal(map[string]any{"platform": "r-box:opencode", "sessionId": "s1", "timeUpdated": 42, "archived": archived})
			response := httptest.NewRecorder()
			srv.handleArchiveSession(response, httptest.NewRequest(http.MethodPost, "/api/session/archive", strings.NewReader(string(body))))
			if response.Code != http.StatusOK {
				t.Fatalf("archive: %d %s", response.Code, response.Body.String())
			}
			for _, sub := range []*broadcastSub{first, second} {
				var ev broadcastEvent
				select {
				case ev = <-sub.ch:
				default:
					t.Fatal("missing archive broadcast")
				}
				var payload struct {
					SessionID string `json:"sessionID"`
					Platform  string `json:"platform"`
					Patch     struct {
						Archived bool `json:"archived"`
					} `json:"patch"`
				}
				if err := json.Unmarshal(ev.data, &payload); err != nil {
					t.Fatal(err)
				}
				if ev.event != "ocman.session.changed" || payload.SessionID != "s1" || payload.Platform != "r-box:opencode" || payload.Patch.Archived != archived {
					t.Fatalf("wrong archive event: %s %s", ev.event, ev.data)
				}
			}
		})
	}
}
