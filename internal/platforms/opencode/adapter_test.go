package opencode

import (
	"context"
	"testing"
)

func TestAdapter_ID(t *testing.T) {
	a := New(nil, nil)
	if a.ID() != "opencode" {
		t.Errorf("expected ID=opencode, got %q", a.ID())
	}
}

func TestAdapter_DisplayName(t *testing.T) {
	a := New(nil, nil)
	if a.DisplayName() != "OpenCode" {
		t.Errorf("expected DisplayName=OpenCode, got %q", a.DisplayName())
	}
}

func TestAdapter_Capabilities_AllTrue(t *testing.T) {
	a := New(nil, nil)
	c := a.Capabilities()
	cases := map[string]bool{
		"Composer":          c.Composer,
		"RespondPermission": c.RespondPermission,
		"RespondQuestion":   c.RespondQuestion,
		"Abort":             c.Abort,
		"Compact":           c.Compact,
		"Events":            c.Events,
		"AgentCatalog":      c.AgentCatalog,
		"ModelCatalog":      c.ModelCatalog,
		"SlashCommands":     c.SlashCommands,
	}
	for name, got := range cases {
		if !got {
			t.Errorf("capability %s: expected true for OpenCode in v1, got false", name)
		}
	}
}

func TestAdapter_Available_NilDB(t *testing.T) {
	a := New(nil, nil)
	if a.Available(context.Background()) {
		t.Error("Available should return false when DB is nil")
	}
}

func TestAdapter_LiveStatus_ReturnsNil(t *testing.T) {
	a := New(nil, nil)
	// OpenCode does not track in-memory live state via hooks; it uses
	// port discovery on demand. LiveStatus is always nil.
	if ls := a.LiveStatus("any-session"); ls != nil {
		t.Errorf("LiveStatus should return nil for OpenCode, got %+v", ls)
	}
}
