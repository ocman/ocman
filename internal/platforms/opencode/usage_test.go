package opencode

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSessionUsageIncludesOnlyDescendants(t *testing.T) {
	parent, root, child := "parent", "root", "child"
	message := `{"role":"assistant","modelID":"m","cost":0.25,"tokens":{"input":10,"output":20,"cache":{"read":30,"write":40}}}`
	database := newTestDBWithSessions(t, []testSession{
		{id: parent, messageData: message},
		{id: root, parentID: &parent, messages: []string{message, `{"role":"user","cost":99,"tokens":{"input":99}}`, `not json`}},
		{id: child, parentID: &root, messageData: message},
		{id: "grandchild", parentID: &child, messageData: message},
		{id: "sibling", parentID: &parent, messageData: message},
		{id: "unrelated", messageData: message},
		{id: "empty"},
	})
	adapter := NewWithPricing(database, nil, fakePricing{in: 0.05})
	usage, err := adapter.SessionUsage(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 3 {
		t.Fatalf("usage = %+v", usage)
	}
	want := platforms.Usage{Tokens: platforms.TokenTotals{Input: 10, Output: 20, CacheRead: 30, CacheWrite: 40}, Cost: 0.25, EstCost: 0.5}
	for _, id := range []string{root, child, "grandchild"} {
		if usage[id] != want {
			t.Fatalf("%s = %+v, want %+v", id, usage[id], want)
		}
	}
	empty, err := adapter.SessionUsage(t.Context(), "empty")
	if err != nil || len(empty) != 1 || empty["empty"] != (platforms.Usage{}) {
		t.Fatalf("empty = %+v, %v", empty, err)
	}
	if _, err := adapter.SessionUsage(t.Context(), "missing"); err == nil {
		t.Fatal("missing session accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := adapter.SessionUsage(ctx, root); err == nil {
		t.Fatal("canceled query accepted")
	}
	if _, err := New(nil, nil).SessionUsage(t.Context(), root); err == nil {
		t.Fatal("nil database accepted")
	}
}
