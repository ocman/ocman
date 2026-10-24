package autoapprove

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/api"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/provider"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
)

type canonicalWatchNative struct{ *opencode.Adapter }

func (*canonicalWatchNative) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "opencode", SourceKey: "test-source"}, nil
}

func (*canonicalWatchNative) SessionIdentityMetadata(_ context.Context, id string) (*db.Session, error) {
	return &db.Session{ID: id, Directory: "/repo"}, nil
}

func TestHeadlessWatcherProjectsCanonicalEventsBeforeApplicationCallbacks(t *testing.T) {
	database, err := state.Open(statetest.Path(t, sessioncatalog.Schema), sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	catalogue, err := sessioncatalog.New(database)
	if err != nil {
		t.Fatal(err)
	}
	native := &canonicalWatchNative{Adapter: opencode.New(nil, nil, nil, ocapi.Auth{})}
	adapter, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/event":
			w.Header().Set("Content-Type", "text/event-stream")
			writeFlush(w, `data: {"type":"session.status","properties":{"sessionID":"native","status":{"type":"busy"}}}`+"\n\n")
			writeFlush(w, `data: {"type":"permission.asked","properties":{"sessionID":"native","id":"native-permission","permission":"bash","metadata":{"sessionID":"opaque"}}}`+"\n\n")
			writeFlush(w, `data: {"directory":"/repo","payload":{"type":"ocman.queue.changed","properties":{"sessionID":"native"}}}`+"\n\n")
		case "/session/status":
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	port := strings.TrimPrefix(server.URL, "http://127.0.0.1:")
	var statusID, permissionID, queueID string
	svc := NewService(Deps{
		OpencodePlatform:       func() platforms.Platform { return adapter },
		BroadcastSessionStatus: func(id string, status db.SessionStatus) { statusID = id },
		BroadcastQueueChanged:  func(_ string, id string) { queueID = id },
	})
	watcher := newAutoApproveWatcher(svc)
	watcher.discoverPorts = func() map[string]string { return map[string]string{"/repo": port} }
	watcher.onPermission = func(_ platforms.ID, p platforms.Platform, id, request, _ string, _ []string, metadata map[string]any) {
		permissionID = id
		if p != adapter || request != "native-permission" || metadata["sessionID"] != "opaque" {
			t.Errorf("permission callback lost adapter or opaque content")
		}
	}
	if err := watcher.streamOnce(t.Context(), port); err != nil {
		t.Fatal(err)
	}
	row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "test-source", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	if !sessioncatalog.IsID(row.ID) || statusID != row.ID || permissionID != row.ID || queueID != row.ID {
		t.Fatalf("native reference reached callbacks: status=%q permission=%q queue=%q canonical=%q", statusID, permissionID, queueID, row.ID)
	}
}
