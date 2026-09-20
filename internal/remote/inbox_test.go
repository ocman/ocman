package remote

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/state"
)

type testInboxStore struct {
	*state.DB
	raw *sql.DB
}

func openInboxStore(t *testing.T) *testInboxStore {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	store, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return &testInboxStore{DB: store, raw: raw}
}

func connectInboxRemote(t *testing.T, id string, store *state.DB) *RemoteConn {
	t.Helper()
	srv := NewServer(platforms.NewRegistry(), localStubHost{}, id, "v-test").UseInboxStore(store)
	ln, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "tok", TrustedOverlay: true}, srv)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = ln.Serve() }()
	t.Cleanup(ln.Stop)

	conn := NewRemoteConn("grpc://"+ln.Addr(), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	return conn
}

func TestRemoteConnInboxRoundTrip(t *testing.T) {
	store := openInboxStore(t)
	first, err := store.CreateInboxItem(t.Context(), "first", "body one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateInboxItem(t.Context(), "second", "body two")
	if err != nil {
		t.Fatal(err)
	}
	conn := connectInboxRemote(t, "remote-1", store.DB)

	items, err := conn.InboxItems(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]state.InboxItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	if len(items) != 2 || !reflect.DeepEqual(byID[first.ID], first) || !reflect.DeepEqual(byID[second.ID], second) {
		t.Fatalf("InboxItems = %#v", items)
	}
	if err := conn.MarkInboxItemRead(t.Context(), first.ID); err != nil {
		t.Fatal(err)
	}
	if err := conn.ArchiveInboxItems(t.Context(), []string{second.ID}, false); err != nil {
		t.Fatal(err)
	}
	items, err = conn.InboxItems(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != first.ID || items[0].ReadAt == 0 {
		t.Fatalf("after read/archive = %#v", items)
	}
	if err := conn.ArchiveInboxItems(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	if items, err = conn.InboxItems(t.Context()); err != nil || len(items) != 0 {
		t.Fatalf("after archive all read = %#v, %v", items, err)
	}
}

func TestRemoteInboxPermissionRoundTrip(t *testing.T) {
	store := openInboxStore(t)
	permission := state.InboxPermission{Platform: "opencode", SessionID: "child-session", PermissionID: "permission", Permission: "bash", Patterns: []string{"git status"}, Metadata: map[string]any{"command": "git status"}}
	if err := store.EnsurePermissionInboxItem(t.Context(), permission); err != nil {
		t.Fatal(err)
	}
	conn := connectInboxRemote(t, "remote-1", store.DB)
	items, err := conn.InboxItems(t.Context())
	if err != nil || len(items) != 1 || items[0].Category != state.InboxPermissionCategory || !reflect.DeepEqual(items[0].Permission, &permission) {
		t.Fatalf("remote permission: %+v, %v", items, err)
	}
	if err := store.ResolvePermissionInboxItem(t.Context(), permission.Platform, permission.SessionID, permission.PermissionID); err != nil {
		t.Fatal(err)
	}
	items, err = conn.InboxItems(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("resolved remote permission: %+v, %v", items, err)
	}
}

func TestServerInboxRejectsMissingStoreAndMalformedJSON(t *testing.T) {
	server := NewServer(platforms.NewRegistry(), localStubHost{}, "remote-1", "v-test")
	if _, err := server.InboxItems(t.Context(), &pb.Empty{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("InboxItems without store = %v", err)
	}
	if _, err := server.MarkInboxItemRead(t.Context(), &pb.JsonReq{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("MarkInboxItemRead without store = %v", err)
	}
	if _, err := server.MarkInboxItemUnread(t.Context(), &pb.JsonReq{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("MarkInboxItemUnread without store = %v", err)
	}
	if _, err := server.ArchiveInboxItems(t.Context(), &pb.JsonReq{}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ArchiveInboxItems without store = %v", err)
	}

	server.UseInboxStore(openInboxStore(t).DB)
	bad := &pb.JsonReq{Payload: []byte("{")}
	if _, err := server.MarkInboxItemRead(t.Context(), bad); err == nil {
		t.Fatal("MarkInboxItemRead accepted malformed JSON")
	}
	if _, err := server.MarkInboxItemUnread(t.Context(), bad); err == nil {
		t.Fatal("MarkInboxItemUnread accepted malformed JSON")
	}
	if _, err := server.ArchiveInboxItems(t.Context(), bad); err == nil {
		t.Fatal("ArchiveInboxItems accepted malformed JSON")
	}
}

func TestManagerInboxKeepsOwnerLocalIDsIsolated(t *testing.T) {
	localStore := openInboxStore(t)
	remoteStore := openInboxStore(t)
	localItem, err := localStore.CreateInboxItem(t.Context(), "local", "local body")
	if err != nil {
		t.Fatal(err)
	}
	remoteItem, err := remoteStore.CreateInboxItem(t.Context(), "remote", "remote body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remoteStore.raw.ExecContext(t.Context(), `UPDATE inbox_item SET id = ? WHERE id = ?`, localItem.ID, remoteItem.ID); err != nil {
		t.Fatal(err)
	}
	remoteItem.ID = localItem.ID

	conn := connectInboxRemote(t, "remote-1", remoteStore.DB)
	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), localStore.DB, "opencode")
	t.Cleanup(mgr.Stop)
	mr := &managedRemote{localID: 1, conn: conn}
	mgr.remotes[1] = mr
	if !mgr.publishAdapters(1, mr, newRemotePlatform(conn, "opencode", func() string { return "remote" }), newRemoteHost(conn)) {
		t.Fatal("publishing remote")
	}

	// IDs are opaque but owner-local; the explicit source selects the store.
	if err := mgr.MarkInboxItemRead(t.Context(), "remote-1", remoteItem.ID); err != nil {
		t.Fatal(err)
	}
	localItems, _ := mgr.InboxItems(t.Context(), "local")
	remoteItems, _ := mgr.InboxItems(t.Context(), "remote-1")
	if len(localItems) != 1 || localItems[0].ReadAt != 0 {
		t.Fatalf("local items = %#v", localItems)
	}
	if len(remoteItems) != 1 || remoteItems[0].ReadAt == 0 {
		t.Fatalf("remote items = %#v", remoteItems)
	}
	if err := mgr.MarkInboxItemRead(t.Context(), "local", localItem.ID); err != nil {
		t.Fatal(err)
	}
	if err := mgr.MarkInboxItemUnread(t.Context(), "remote-1", remoteItem.ID); err != nil {
		t.Fatal(err)
	}
	localItems, _ = mgr.InboxItems(t.Context(), "local")
	remoteItems, _ = mgr.InboxItems(t.Context(), "remote-1")
	if localItems[0].ReadAt == 0 || remoteItems[0].ReadAt != 0 {
		t.Fatalf("unread mutation crossed owners: local=%+v remote=%+v", localItems, remoteItems)
	}
	if err := mgr.MarkInboxItemUnread(t.Context(), "local", localItem.ID); err != nil {
		t.Fatal(err)
	}
	localItems, _ = mgr.InboxItems(t.Context(), "local")
	if localItems[0].ReadAt != 0 {
		t.Fatalf("local unread mutation failed: %+v", localItems)
	}
	if err := mgr.ArchiveInboxItems(t.Context(), "local", []string{localItem.ID}, false); err != nil {
		t.Fatal(err)
	}
	if localItems, _ = mgr.InboxItems(t.Context(), "local"); len(localItems) != 0 {
		t.Fatalf("local items after archive = %#v", localItems)
	}
	if remoteItems, _ = mgr.InboxItems(t.Context(), "remote-1"); len(remoteItems) != 1 {
		t.Fatalf("remote item archived through local owner: %#v", remoteItems)
	}
	if err := mgr.ArchiveInboxItems(t.Context(), "remote-1", []string{remoteItem.ID}, false); err != nil {
		t.Fatal(err)
	}
	if remoteItems, _ = mgr.InboxItems(t.Context(), "remote-1"); len(remoteItems) != 0 {
		t.Fatalf("remote items after archive = %#v", remoteItems)
	}
	read, err := localStore.CreateInboxItem(t.Context(), "read", "body")
	if err != nil {
		t.Fatal(err)
	}
	if err := localStore.MarkInboxItemRead(t.Context(), read.ID); err != nil {
		t.Fatal(err)
	}
	if err := mgr.ArchiveInboxItems(t.Context(), "local", nil, true); err != nil {
		t.Fatal(err)
	}
	if localItems, _ = mgr.InboxItems(t.Context(), "local"); len(localItems) != 0 {
		t.Fatalf("local read items after archive = %#v", localItems)
	}
}

func TestManagerInboxOwnerSelectionFailsClosed(t *testing.T) {
	store := openInboxStore(t)
	item, err := store.CreateInboxItem(t.Context(), "keep unread", "body")
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), store.DB, "opencode")

	for _, source := range []string{"", "missing"} {
		if err := mgr.MarkInboxItemUnread(t.Context(), source, item.ID); !errors.Is(err, ErrRemoteOffline) {
			t.Errorf("MarkInboxItemUnread source %q = %v, want ErrRemoteOffline", source, err)
		}
		if err := mgr.MarkInboxItemRead(t.Context(), source, item.ID); !errors.Is(err, ErrRemoteOffline) {
			t.Errorf("MarkInboxItemRead source %q = %v, want ErrRemoteOffline", source, err)
		}
	}
	items, err := store.ListInboxItems(t.Context())
	if err != nil || len(items) != 1 || items[0].ReadAt != 0 {
		t.Fatalf("local Inbox changed after unknown owner: %#v, %v", items, err)
	}

	conn := &RemoteConn{remoteID: "offline", health: HealthOffline}
	mr := &managedRemote{localID: 1, conn: conn, platform: &remotePlatform{}}
	mgr.remotes[1] = mr
	if _, err := mgr.InboxItems(t.Context(), "offline"); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("InboxItems offline = %v, want ErrRemoteOffline", err)
	}
	if err := mgr.MarkInboxItemRead(t.Context(), "offline", item.ID); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("MarkInboxItemRead offline = %v, want ErrRemoteOffline", err)
	}
	if err := mgr.MarkInboxItemUnread(t.Context(), "offline", item.ID); !errors.Is(err, ErrRemoteOffline) {
		t.Fatalf("MarkInboxItemUnread offline = %v, want ErrRemoteOffline", err)
	}
	for _, source := range []string{"missing", "offline"} {
		if err := mgr.ArchiveInboxItems(t.Context(), source, []string{item.ID}, false); !errors.Is(err, ErrRemoteOffline) {
			t.Errorf("ArchiveInboxItems source %q = %v, want ErrRemoteOffline", source, err)
		}
	}
}
