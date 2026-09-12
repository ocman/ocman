package remote

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestManagerLocalWebhookLifecycle(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/inboxes":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"inbox","ingestionUrl":"https://relay.test/i/inbox","managementToken":"manage","fetchToken":"fetch","acknowledgmentToken":"ack","keyVersion":1}`))
		case r.Method == http.MethodGet && r.URL.Path == "/inboxes/inbox/deliveries":
			_, _ = w.Write([]byte(`{"deliveries":[],"cursor":""}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer relay.Close()

	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.CreateRoutine(t.Context(), state.Routine{
		ID: "routine", Name: "Routine", Prompt: "run", Directory: "/repo", RemoteID: "local",
		SessionMode: "new", ScheduleKind: "none", Enabled: true, CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{store: store}

	inbox, err := manager.RegisterWebhookInbox(t.Context(), "local", "routine", relay.URL, "enroll")
	if err != nil || inbox.ID != "inbox" {
		t.Fatalf("register = %+v, %v", inbox, err)
	}
	if err := manager.PollWebhookInbox(t.Context(), "local", "routine"); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if _, err := manager.RegisterWebhookInbox(t.Context(), "missing", "routine", relay.URL, "enroll"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("missing remote error = %v", err)
	}
}

func TestRemoteConnWebhookRequiresConnection(t *testing.T) {
	conn := &RemoteConn{}
	if _, err := conn.RegisterWebhookInbox(t.Context(), "routine", "https://relay", "token"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("register error = %v", err)
	}
	if err := conn.PollWebhookInbox(t.Context(), "routine"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("poll error = %v", err)
	}
}
