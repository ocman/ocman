package remote

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
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
	emptyManager := &Manager{}
	if _, err := emptyManager.RegisterWebhookInbox(t.Context(), "local", "routine", relay.URL, "enroll"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("missing local store error = %v", err)
	}
	if err := emptyManager.PollWebhookInbox(t.Context(), "local", "routine"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("missing local poll store error = %v", err)
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

func TestRemoteWebhookRPCRoundTrip(t *testing.T) {
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

	server := NewServer(platforms.NewRegistry(), localStubHost{}, "remote", "test").UseInboxStore(store).UseWebhookDispatcher(nil)
	grpcConn := startTestServer(t, "token", server)
	client := pb.NewOcmanClient(grpcConn)
	remote := &RemoteConn{client: client, remoteID: "owner"}
	inbox, err := remote.RegisterWebhookInboxWithSecret(t.Context(), "routine", relay.URL, "enroll", "secret", "X-Secret")
	if err != nil || inbox.ID != "inbox" {
		t.Fatalf("register = %+v, %v", inbox, err)
	}
	if err := remote.PollWebhookInbox(t.Context(), "routine"); err != nil {
		t.Fatalf("poll: %v", err)
	}
	manager := &Manager{remotes: map[int64]*managedRemote{1: {conn: remote}}}
	manager.SetWebhookDispatcher(nil)
	if _, err := manager.RegisterWebhookInboxWithSecret(t.Context(), "owner", "routine", relay.URL, "enroll", "secret", "X-Secret"); err != nil {
		t.Fatalf("manager register: %v", err)
	}
	if err := manager.PollWebhookInbox(t.Context(), "owner", "routine"); err != nil {
		t.Fatalf("manager poll: %v", err)
	}
	for name, call := range map[string]func() error{
		"malformed register": func() error {
			_, err := client.RegisterWebhookInbox(t.Context(), &pb.JsonReq{Payload: []byte("{")})
			return err
		},
		"malformed poll": func() error {
			_, err := client.PollWebhookInbox(t.Context(), &pb.JsonReq{Payload: []byte("{")})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("malformed request succeeded")
			}
		})
	}

	unconfigured := pb.NewOcmanClient(startTestServer(t, "token", NewServer(platforms.NewRegistry(), localStubHost{}, "remote", "test")))
	if _, err := unconfigured.RegisterWebhookInbox(t.Context(), &pb.JsonReq{}); err == nil {
		t.Fatal("register succeeded without a store")
	}
	if _, err := unconfigured.PollWebhookInbox(t.Context(), &pb.JsonReq{}); err == nil {
		t.Fatal("poll succeeded without a store")
	}
}
