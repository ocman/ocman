package owned

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2"
)

func storedFixture(t *testing.T) (*Adapter, *nativePlatform, string) {
	t.Helper()
	a, p, _ := fixture(t)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	history := a.history
	if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "source", SessionID: row.ID}, Directory: "/stored", Title: "stored", UpdatedAt: 30, ReadFormat: statev2.ConversationReadFormat}, Messages: []statev2.Message{
		{ID: "u", Role: "user", ConversationVisible: true, CreatedAt: 10, Extensions: json.RawMessage(`{"role":"user"}`)},
		{ID: "a", Role: "assistant", ConversationVisible: true, Cost: 0.5, TokensInput: 7, TokensOutput: 3, TokensCacheRead: 2, TokensCacheWrite: 1, Finish: "stop", Position: 1, CreatedAt: 20, Extensions: json.RawMessage(`{"role":"assistant","modelID":"model","providerID":"p","tokens":{"input":7,"output":3,"cache":{"read":2,"write":1}},"cost":0.5,"finish":"stop"}`)},
	}, Parts: []statev2.Part{{ID: "part", MessageID: "u", Kind: "text", Text: "stored text", TextPresent: true}}}); err != nil {
		t.Fatal(err)
	}
	p.rows = []db.Session{{ID: "native", Directory: "/native", Title: "native", TotalCost: 99}, {ID: "native-only", Directory: "/bypass"}}
	return a, p, row.ID
}

func TestRetainedReadsDoNotBorrowExecutionAuthorityFromOfflineSources(t *testing.T) {
	a, p, id := storedFixture(t)
	a.unavailable = errors.New("owner source disconnected")
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil || len(rows) != 1 || rows[0].Status == db.StatusBusy || rows[0].LiveConnection {
		t.Fatalf("offline observations leaked: %+v %v", rows, err)
	}
	detail, err := a.Session(t.Context(), id, 10, 0)
	if err != nil || detail == nil || detail.ConversationSource != "statev2" || len(detail.Messages) != 2 || len(detail.Parts) != 1 || detail.Parts[0].ID != "part" || detail.Messages[0].ID != "u" {
		t.Fatalf("offline conversation lost: %+v %v", detail, err)
	}
	if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "must not execute"}); !errors.Is(err, platforms.ErrHistoryUnavailable) || len(p.sent) != 0 {
		t.Fatalf("offline read authorized execution: %v", err)
	}
	if _, err := a.Session(t.Context(), id, -1, 0); err == nil {
		t.Fatal("invalid paging accepted")
	}
	if _, err := a.SessionMetadata(t.Context(), "native"); err == nil {
		t.Fatal("native caller reference accepted")
	}
}

func TestStoredReadScopePublicationAndArchiveSelection(t *testing.T) {
	a, _, id := storedFixture(t)
	child, err := a.historyIdentity(t.Context(), "child")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "source", SessionID: child.ID}, ParentID: id, Title: "child", UpdatedAt: 20}}); err != nil {
		t.Fatal(err)
	}
	unpublished, _, err := a.store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/hidden", Origin: sessioncatalog.User, RequestKey: "not-ready", PayloadHash: strings.Repeat("0", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "source", SessionID: unpublished.ID}, Directory: "/hidden"}}); err != nil {
		t.Fatal(err)
	}
	if rows, err := a.Sessions(t.Context(), "", 0); err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("inactive child or unpublished row leaked: %+v %v", rows, err)
	}
	if _, err := a.SessionMetadata(t.Context(), unpublished.ID); !errors.Is(err, sessioncatalog.ErrCreating) {
		t.Fatal(err)
	}
	if _, err := a.UnreadCounts(t.Context(), map[string]int64{unpublished.ID: 0}); err != nil {
		t.Fatal(err)
	}
	children, err := a.SessionDescendants(t.Context(), id)
	if err != nil || !reflect.DeepEqual(children, []string{id, child.ID}) {
		t.Fatalf("stored descendants: %v %v", children, err)
	}
	if rows, err := a.SessionsInactiveBefore(t.Context(), 30); err != nil || len(rows) != 1 || rows[0].ID != child.ID {
		t.Fatalf("archive cutoff: %+v %v", rows, err)
	}
	if rows, err := a.Sessions(t.Context(), "/elsewhere", 0); err != nil || len(rows) != 0 {
		t.Fatalf("directory filter: %+v %v", rows, err)
	}
	if rows, err := a.Sessions(t.Context(), "", 31); err != nil || len(rows) != 0 {
		t.Fatalf("time filter: %+v %v", rows, err)
	}
}

func TestBoundReadModelFailsClosedWithoutStoreOrAfterStoreCloses(t *testing.T) {
	a, _, id := storedFixture(t)
	_ = a.history.Close()
	for name, read := range map[string]func() error{
		"list":        func() error { _, err := a.Sessions(t.Context(), "", 0); return err },
		"summary":     func() error { _, err := a.SessionSummary(t.Context(), id); return err },
		"metadata":    func() error { _, err := a.SessionMetadata(t.Context(), id); return err },
		"usage":       func() error { _, err := a.SessionUsage(t.Context(), id); return err },
		"unread":      func() error { _, err := a.UnreadCounts(t.Context(), map[string]int64{id: 0}); return err },
		"descendants": func() error { _, err := a.SessionDescendants(t.Context(), id); return err },
		"archive":     func() error { _, err := a.SessionsInactiveBefore(t.Context(), 100); return err },
	} {
		if err := read(); err == nil {
			t.Fatalf("%s fell back to native DB", name)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.Session(ctx, id, 10, 0); err == nil {
		t.Fatal("canceled read accepted")
	}
}

func TestCanonicalPersistedReadsNeverDelegateToNativeReaders(t *testing.T) {
	a, _, id := storedFixture(t)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil || len(rows) != 1 || rows[0].ID != id || rows[0].Title != "stored" || rows[0].TotalCost != 0.5 || rows[0].TotalInputTokens != 7 || rows[0].Status != db.StatusBusy {
		t.Fatalf("list bypassed statev2 or lost live observation: %+v %v", rows, err)
	}
	for name, read := range map[string]func() (*db.Session, error){"metadata": func() (*db.Session, error) { return a.SessionMetadata(t.Context(), id) }, "summary": func() (*db.Session, error) { return a.SessionSummary(t.Context(), id) }} {
		row, err := read()
		if err != nil || row == nil || row.Title != "stored" || row.Directory != "/stored" {
			t.Fatalf("%s bypassed statev2: %+v %v", name, row, err)
		}
	}
	usage, err := a.SessionUsage(t.Context(), id)
	if err != nil || usage[id].Cost != 0.5 || usage[id].Tokens.CacheRead != 2 {
		t.Fatalf("usage bypassed statev2: %+v %v", usage, err)
	}
	unread, err := a.UnreadCounts(t.Context(), map[string]int64{id: 10})
	if err != nil || unread[id] != 1 {
		t.Fatalf("unread bypassed statev2: %v %v", unread, err)
	}
}
