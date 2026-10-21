package statev2

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestRecentUserMessagesUsesStoredScopeOrderAndProse(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Checkpoint = nil
	b.Messages = []Message{{ID: "u0", Role: "user"}, {ID: "a", Role: "assistant", Position: 1}, {ID: "u1", Role: "user", Position: 2}, {ID: "u2", Role: "user", Position: 3}}
	b.Parts = []Part{
		{ID: "p0", MessageID: "u0", Kind: "text", Text: "first"},
		{ID: "pa", MessageID: "a", Kind: "text", Text: "assistant"},
		{ID: "p1", MessageID: "u1", Kind: "text", Text: "second "},
		{ID: "p2", MessageID: "u1", Kind: "text", Text: "piece", Position: 1},
		{ID: "p3", MessageID: "u1", Kind: "text", Text: "synthetic", Position: 2, Extensions: json.RawMessage(`{"synthetic":true}`)},
		{ID: "p4", MessageID: "u2", Kind: "tool", Text: "tool"},
	}
	commit(t, d, b)
	for _, tc := range []struct {
		limit int
		want  []string
	}{{6, []string{"first", "second piece"}}, {2, []string{"first", "second piece"}}, {1, []string{"second piece"}}} {
		got, err := d.RecentUserMessages(t.Context(), "owner", "opencode", "session", tc.limit)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%v %v want %v", got, err, tc.want)
		}
	}
	for _, scope := range [][3]string{{"other", "opencode", "session"}, {"owner", "other", "session"}, {"owner", "opencode", "missing"}} {
		if _, err := d.RecentUserMessages(t.Context(), scope[0], scope[1], scope[2], 6); !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	for _, scope := range [][3]string{{"", "opencode", "session"}, {"owner", "", "session"}, {"owner", "opencode", ""}} {
		if _, err := d.RecentUserMessages(t.Context(), scope[0], scope[1], scope[2], 6); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
	for _, limit := range []int{0, 101} {
		if _, err := d.RecentUserMessages(t.Context(), "owner", "opencode", "session", limit); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
	register(t, d, "second")
	b.Session.InstanceID = "second"
	commit(t, d, b)
	if _, err := d.RecentUserMessages(t.Context(), "owner", "opencode", "session", 6); !errors.Is(err, ErrConflict) {
		t.Fatalf("mixed source intent: %v", err)
	}
	_ = d.Close()
	if _, err := d.RecentUserMessages(t.Context(), "owner", "opencode", "session", 6); err == nil {
		t.Fatal("closed DB accepted")
	}
}
