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

// stubParentLookup is a parentLookup implementation backed by a static
// map. Used to exercise bubbleUpPromptsToParent in isolation.
type stubParentLookup struct {
	parents map[string]string
	err     error
}

func (s stubParentLookup) GetSessionParentIDs(ids []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if p, ok := s.parents[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

// TestBubbleUpPromptsToParent verifies the per-session bubble-up: a
// subagent's prompted ID gets remapped to the parent's ID so the
// listing UI can flag the parent session as having a pending prompt
// (subagent sessions are filtered out of the listing entirely).
func TestBubbleUpPromptsToParent(t *testing.T) {
	tests := []struct {
		name     string
		prompted map[string]bool
		parents  map[string]string
		want     map[string]bool
	}{
		{
			name:     "empty input passes through",
			prompted: nil,
			want:     nil,
		},
		{
			name:     "top-level session unchanged",
			prompted: map[string]bool{"s1": true},
			parents:  map[string]string{},
			want:     map[string]bool{"s1": true},
		},
		{
			name:     "subagent maps to parent",
			prompted: map[string]bool{"child": true},
			parents:  map[string]string{"child": "parent"},
			want:     map[string]bool{"parent": true},
		},
		{
			name:     "mixed: parent + subagent collapse onto same key",
			prompted: map[string]bool{"parent": true, "child": true},
			parents:  map[string]string{"child": "parent"},
			want:     map[string]bool{"parent": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := stubParentLookup{parents: tt.parents}
			got := bubbleUpPromptsToParent(tt.prompted, lookup)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("got[%q] = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

// TestBubbleUpPromptsToParent_NilLookup tolerates a nil DB (e.g. an
// adapter constructed without one in tests) by returning the original
// map untouched.
func TestBubbleUpPromptsToParent_NilLookup(t *testing.T) {
	prompted := map[string]bool{"s1": true}
	got := bubbleUpPromptsToParent(prompted, nil)
	if len(got) != 1 || !got["s1"] {
		t.Errorf("nil lookup should pass through unchanged, got %v", got)
	}
}
