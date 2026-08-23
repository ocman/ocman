package opencode

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// These tests cover the otherwise-uncovered Adapter delegation
// methods (Sessions, Owns, Session, SessionsInactiveBefore,
// SessionChanges, NewWithPricing). They use the same
// newTestDBWithSession helper as the live-path tests so the schema
// and seed data stay in one place.

func TestAdapter_NewWithPricing(t *testing.T) {
	a := NewWithPricing(nil, nil, nil)
	if a == nil {
		t.Fatalf("NewWithPricing returned nil")
	}
	if a.Available(context.Background()) {
		t.Errorf("Available should be false with nil DB")
	}
}

func TestAdapter_Sessions_NilDBReturnsEmpty(t *testing.T) {
	a := New(nil, nil)
	got, err := a.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if got != nil {
		t.Errorf("Sessions on nil DB = %v, want nil", got)
	}
}

func TestAdapter_Sessions_ReturnsDBRows(t *testing.T) {
	const sid = "sess-1"
	const dir = "/tmp/proj"
	database := newTestDBWithSession(t, sid, dir)
	// Pretend no live OpenCode instances so the live-overlay step
	// is a no-op.
	restore := setDiscoverPortsImplForTests(func() map[string]string { return nil })
	resetPortCacheForTests()
	t.Cleanup(func() { restore(); resetPortCacheForTests() })
	// Seeded DB: force the listing below to read it rather than an
	// earlier test's snapshot.
	InvalidateSessionsCache()

	a := New(database, nil)
	sessions, err := a.Sessions(context.Background(), "", 0)
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].ID != sid {
		t.Errorf("session id = %q, want %q", sessions[0].ID, sid)
	}
	if sessions[0].Platform != string(PlatformID) {
		t.Errorf("platform = %q, want %q", sessions[0].Platform, PlatformID)
	}
}

func TestAdapter_Owns(t *testing.T) {
	const sid = "sess-1"
	database := newTestDBWithSession(t, sid, "/tmp/proj")
	a := New(database, nil)

	if !a.Owns(context.Background(), sid) {
		t.Errorf("Owns(%q) = false, want true", sid)
	}
	if a.Owns(context.Background(), "nonexistent") {
		t.Errorf("Owns(nonexistent) = true, want false")
	}
	if a.Owns(context.Background(), "") {
		t.Errorf("Owns(empty) = true, want false")
	}
}

func TestAdapter_Session_FallsBackToDBWhenNoLivePort(t *testing.T) {
	const sid = "sess-1"
	database := newTestDBWithSession(t, sid, "/tmp/proj")
	restore := setDiscoverPortsImplForTests(func() map[string]string { return nil })
	resetPortCacheForTests()
	t.Cleanup(func() { restore(); resetPortCacheForTests() })

	a := New(database, nil)
	detail, err := a.Session(context.Background(), sid, 30, 0)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	if detail == nil || detail.Session == nil {
		t.Fatalf("nil detail")
	}
	if detail.Session.ID != sid {
		t.Errorf("session id = %q, want %q", detail.Session.ID, sid)
	}
	// No messages were inserted by the helper.
	if len(detail.Messages) != 0 {
		t.Errorf("got %d messages, want 0", len(detail.Messages))
	}
}

func TestAdapter_SessionTreeCombinesNativeAndMCPDescendants(t *testing.T) {
	root := "root"
	mcpChild := "mcp-child"
	database := newTestDBWithSessions(t, []testSession{
		{id: root, directory: "/tmp/proj"},
		{id: "native-child", directory: "/tmp/proj", parentID: &root},
		{id: mcpChild, directory: "/tmp/worktree", messageData: `{"role":"assistant","providerID":"test","modelID":"model","tokens":{"input":2,"output":3}}`},
		{id: "native-grandchild", directory: "/tmp/worktree", parentID: &mcpChild},
		{id: "unrelated", directory: "/tmp/proj"},
	})
	restore := setDiscoverPortsImplForTests(func() map[string]string { return nil })
	resetPortCacheForTests()
	t.Cleanup(func() { restore(); resetPortCacheForTests() })

	a := NewWithPricing(database, nil, fakePricing{in: 0.01, out: 0.02})
	a.childLinks = stubMCPParentLookup{parents: map[string]string{mcpChild: root}}
	for _, openedID := range []string{root, "native-grandchild"} {
		detail, err := a.Session(t.Context(), openedID, 30, 0)
		if err != nil {
			t.Fatalf("Session(%s): %v", openedID, err)
		}

		byID := make(map[string]db.Session, len(detail.SessionTree))
		for _, session := range detail.SessionTree {
			byID[session.ID] = session
		}
		if len(byID) != 4 {
			t.Fatalf("Session(%s) tree = %v, want four related sessions", openedID, byID)
		}
		if _, ok := byID["unrelated"]; ok {
			t.Fatalf("Session(%s) included unrelated session", openedID)
		}
		if byID[mcpChild].ParentID != root || byID["native-grandchild"].ParentID != mcpChild {
			t.Fatalf("Session(%s) mixed parent links not preserved: %v", openedID, byID)
		}
		if !approxEqual(byID[mcpChild].TotalEstCost, 0.08) {
			t.Fatalf("Session(%s) child estimated cost = %v, want 0.08", openedID, byID[mcpChild].TotalEstCost)
		}
		if !approxEqual(byID[mcpChild].TotalEffectiveCost, 0.08) {
			t.Fatalf("Session(%s) child effective cost = %v, want 0.08", openedID, byID[mcpChild].TotalEffectiveCost)
		}
	}
}

func TestApplySessionDetailMetadataFromMessages_CarriesErrorNoticeFields(t *testing.T) {
	session := &db.Session{ID: "sess-1"}
	messages := []db.Message{
		{
			ID:          "m1",
			SessionID:   "sess-1",
			TimeCreated: 1100,
			Data:        []byte(`{"role":"assistant","finish":"error","error":{"name":"ProviderOverloadedError","data":{"message":"provider is overloaded"}}}`),
		},
	}

	applySessionDetailMetadataFromMessages(session, messages)

	if session.Status != "error" {
		t.Errorf("Status = %q, want error", session.Status)
	}
	if session.LastErrorName != "ProviderOverloadedError" {
		t.Errorf("LastErrorName = %q, want ProviderOverloadedError", session.LastErrorName)
	}
	if session.LastErrorMessage != "provider is overloaded" {
		t.Errorf("LastErrorMessage = %q, want provider is overloaded", session.LastErrorMessage)
	}
	if session.LastErrorAt != 1100 {
		t.Errorf("LastErrorAt = %d, want 1100", session.LastErrorAt)
	}
}

func TestApplySessionDetailMetadataFromMessages_CarriesTopLevelErrorMessage(t *testing.T) {
	session := &db.Session{ID: "sess-1"}
	messages := []db.Message{
		{
			ID:          "m1",
			SessionID:   "sess-1",
			TimeCreated: 1100,
			Data:        []byte(`{"role":"assistant","finish":"error","error":{"name":"RateLimitError","message":"This request would exceed your account's rate limit. Please try again later. [retrying in 58m attempt #1]"}}`),
		},
	}

	applySessionDetailMetadataFromMessages(session, messages)

	if session.LastErrorMessage != "This request would exceed your account's rate limit. Please try again later. [retrying in 58m attempt #1]" {
		t.Errorf("LastErrorMessage = %q", session.LastErrorMessage)
	}
}

func TestAdapter_Session_NotFound(t *testing.T) {
	database := newTestDBWithSession(t, "exists", "/tmp/proj")
	restore := setDiscoverPortsImplForTests(func() map[string]string { return nil })
	resetPortCacheForTests()
	t.Cleanup(func() { restore(); resetPortCacheForTests() })

	a := New(database, nil)
	_, err := a.Session(context.Background(), "missing", 30, 0)
	if !errors.Is(err, platforms.ErrNotFound) {
		t.Errorf("Session(missing) error = %v, want ErrNotFound", err)
	}
}

func TestAdapter_Session_NilDBReturnsNotFound(t *testing.T) {
	a := New(nil, nil)
	_, err := a.Session(context.Background(), "x", 30, 0)
	if !errors.Is(err, platforms.ErrNotFound) {
		t.Errorf("Session on nil DB = %v, want ErrNotFound", err)
	}
}

func TestAdapter_SessionsInactiveBefore(t *testing.T) {
	const sid = "sess-1"
	database := newTestDBWithSession(t, sid, "/tmp/proj")
	a := New(database, nil)

	// Cutoff well in the future: every session looks inactive.
	got, err := a.SessionsInactiveBefore(context.Background(), 1<<62)
	if err != nil {
		t.Fatalf("SessionsInactiveBefore: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d candidates, want 1", len(got))
	}
}

func TestAdapter_SessionsInactiveBefore_NilDB(t *testing.T) {
	a := New(nil, nil)
	got, err := a.SessionsInactiveBefore(context.Background(), 0)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestAdapter_SessionChanges_NilDBReturnsNotFound(t *testing.T) {
	a := New(nil, nil)
	_, err := a.SessionChanges(context.Background(), "x")
	if !errors.Is(err, platforms.ErrNotFound) {
		t.Errorf("SessionChanges on nil DB = %v, want ErrNotFound", err)
	}
}

func TestAdapter_SessionChanges_NoParts(t *testing.T) {
	const sid = "sess-1"
	database := newTestDBWithSession(t, sid, "/tmp/proj")

	a := New(database, nil)
	got, err := a.SessionChanges(context.Background(), sid)
	if err != nil {
		t.Fatalf("SessionChanges: %v", err)
	}
	if got == nil {
		t.Fatalf("nil changes (expected empty struct)")
	}
}

func TestAdapter_LiveStatus_AlwaysNil(t *testing.T) {
	a := New(nil, nil)
	if got := a.LiveStatus("anything"); got != nil {
		t.Errorf("LiveStatus = %v, want nil", got)
	}
}
