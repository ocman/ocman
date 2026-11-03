package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestPlannedCreationIdentityIsStable(t *testing.T) {
	a := &Adapter{}
	if id := a.PlannedSessionID("ocm_00000000-0000-4000-8000-000000000001"); id != "ses_ocman00000000000040008000000000000001" {
		t.Fatalf("planned ID=%q", id)
	}
	if a.PlannedSessionID("not-canonical") != "" {
		t.Fatal("invalid canonical ID acquired a native plan")
	}
}

func TestCreateSessionSendsPreRecordedNativeID(t *testing.T) {
	const planned = "ses_ocman00000000000040008000000000000001"
	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["id"] != planned {
			t.Errorf("native creation lost durable ID: %v %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + planned + `"}`))
	})
	response, err := (&Adapter{rt: td}).CreateSession(context.Background(), platforms.CreateSessionRequest{Port: port, NativeID: planned})
	if err != nil || response.ID != planned {
		t.Fatalf("create: %+v %v", response, err)
	}
}
