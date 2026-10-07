package server

import (
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

func TestCreatedRoutineEventCarriesFilterMetadata(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()
	srv.broadcastSessionCreated(sessionsvc.CreatedSession{ID: "ses", Platform: "r-owner:opencode", Directory: "/repo", RoutineID: "rt"})
	select {
	case event := <-sub.ch:
		var payload struct {
			Session db.Session `json:"session"`
		}
		if err := json.Unmarshal(event.data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Session.RoutineID != "rt" || payload.Session.Platform != "r-owner:opencode" {
			t.Fatalf("session=%+v", payload.Session)
		}
	default:
		t.Fatal("no creation event")
	}
}
