package opencode

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
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

// stubMCPParentLookup is an mcpParentLookup backed by a static
// childID->parentID map. It exercises the MCP-child fallback path in
// bubbleUpPromptsToParent (children ocman spawned via MCP/worktree that
// OpenCode's own session.parent_id never records).
type stubMCPParentLookup struct {
	parents map[string]string // childID -> parentID
	err     error
}

func (s stubMCPParentLookup) ChildSessionParents() (map[state.Key]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[state.Key]string, len(s.parents))
	for child, parent := range s.parents {
		out[state.Key{Platform: string(PlatformID), SessionID: child}] = parent
	}
	return out, nil
}

// TestBubbleUpPromptsToParent verifies the per-session bubble-up: a
// subagent's prompted ID is retained while the parent's ID is added so
// both visible session rows can show the pending prompt.
func TestBubbleUpPromptsToParent(t *testing.T) {
	tests := []struct {
		name       string
		prompted   map[string]bool
		parents    map[string]string // OpenCode session.parent_id
		mcpParents map[string]string // ocman state.db child_sessions links
		want       map[string]bool
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
			want:     map[string]bool{"child": true, "parent": true},
		},
		{
			name:     "mixed parent and subagent retain both keys",
			prompted: map[string]bool{"parent": true, "child": true},
			parents:  map[string]string{"child": "parent"},
			want:     map[string]bool{"parent": true, "child": true},
		},
		{
			// Regression (#268): an MCP/worktree child has NO
			// OpenCode parent_id, only an ocman child_sessions link.
			// Its pending-prompt flag must still bubble to the parent.
			name:       "MCP child with no OpenCode parent maps via state.db link",
			prompted:   map[string]bool{"mcpchild": true},
			parents:    map[string]string{},
			mcpParents: map[string]string{"mcpchild": "parent"},
			want:       map[string]bool{"mcpchild": true, "parent": true},
		},
		{
			// OpenCode's own parent_id wins when both are present, so
			// a Task subagent that also happens to have a state.db row
			// resolves consistently (they point at the same parent).
			name:       "OpenCode parent_id takes precedence over MCP link",
			prompted:   map[string]bool{"child": true},
			parents:    map[string]string{"child": "parent"},
			mcpParents: map[string]string{"child": "parent"},
			want:       map[string]bool{"child": true, "parent": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := stubParentLookup{parents: tt.parents}
			mcpLookup := stubMCPParentLookup{parents: tt.mcpParents}
			got := bubbleUpPromptsToParent(tt.prompted, lookup, mcpLookup)
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
	got := bubbleUpPromptsToParent(prompted, nil, nil)
	if len(got) != 1 || !got["s1"] {
		t.Errorf("nil lookup should pass through unchanged, got %v", got)
	}
}

// TestBubbleUpPromptsToParent_MCPOnly verifies the MCP fallback works
// even when the OpenCode parent lookup is nil (e.g. an adapter with no
// read-only OpenCode DB but a live state.db): the child_sessions link
// alone must bubble the flag.
func TestBubbleUpPromptsToParent_MCPOnly(t *testing.T) {
	prompted := map[string]bool{"mcpchild": true}
	mcpLookup := stubMCPParentLookup{parents: map[string]string{"mcpchild": "parent"}}
	got := bubbleUpPromptsToParent(prompted, nil, mcpLookup)
	if len(got) != 2 || !got["mcpchild"] || !got["parent"] {
		t.Errorf("MCP-only lookup should bubble to parent, got %v", got)
	}
}

// TestMCPChildSessionIDs verifies the reverse lookup: given a parent
// session ID, return the IDs of every ocman MCP/worktree child linked
// to it in state.db. This feeds listPrompts's subagentIDs so a child's
// permission/question prompt is recognised as relevant on the parent
// page (the child has no OpenCode parent_id, so /children and
// GetSubagentSessionIDs both miss it — see #268 regression).
func TestMCPChildSessionIDs(t *testing.T) {
	mcp := stubMCPParentLookup{parents: map[string]string{
		"childA": "parent1",
		"childB": "parent1",
		"childC": "parent2",
	}}
	got := mcpChildSessionIDs(mcp, "parent1")
	want := map[string]bool{"childA": true, "childB": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want children of parent1 only", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected child %q for parent1", id)
		}
	}
}

// TestMCPChildSessionIDs_NilAndEmpty degrades cleanly: a nil reader or
// an empty parent ID yields no children rather than panicking.
func TestMCPChildSessionIDs_NilAndEmpty(t *testing.T) {
	if got := mcpChildSessionIDs(nil, "parent1"); got != nil {
		t.Errorf("nil reader should yield nil, got %v", got)
	}
	mcp := stubMCPParentLookup{parents: map[string]string{"childA": "parent1"}}
	if got := mcpChildSessionIDs(mcp, ""); got != nil {
		t.Errorf("empty parent should yield nil, got %v", got)
	}
}

// favReaderOnly implements FavoritesReader but NOT mcpParentLookup, so
// childLinksFrom must return nil for it (bare stub, e.g. tests) and the
// stub state.DB otherwise.
type favReaderOnly struct{}

func (favReaderOnly) ModelFavorites(string) ([]state.ModelFavorite, error) { return nil, nil }

// favWithLinks implements both FavoritesReader and mcpParentLookup, like
// the production *state.DB.
type favWithLinks struct{ favReaderOnly }

func (favWithLinks) ChildSessionParents() (map[state.Key]string, error) { return nil, nil }

func TestChildLinksFrom(t *testing.T) {
	if got := childLinksFrom(nil); got != nil {
		t.Errorf("nil favorites should yield nil childLinks, got %v", got)
	}
	if got := childLinksFrom(favReaderOnly{}); got != nil {
		t.Errorf("favorites without ChildSessionParents should yield nil, got %v", got)
	}
	if got := childLinksFrom(favWithLinks{}); got == nil {
		t.Errorf("favorites implementing mcpParentLookup should be returned")
	}
}
