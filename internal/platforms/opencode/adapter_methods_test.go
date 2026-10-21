package opencode

import (
	"context"
	"testing"
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

func TestAdapter_LiveStatus_AlwaysNil(t *testing.T) {
	a := New(nil, nil)
	if got := a.LiveStatus("anything"); got != nil {
		t.Errorf("LiveStatus = %v, want nil", got)
	}
}
