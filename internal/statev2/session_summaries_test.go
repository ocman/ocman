package statev2

import (
	"encoding/json"
	"reflect"
	"testing"
)

func summaryBatch(instance, id, parent string) Batch {
	return Batch{Session: Session{Key: Key{InstanceID: instance, SessionID: id}, Directory: "/repo", Title: id, ParentID: parent, CreatedAt: 1, UpdatedAt: 30, ReadFormat: ConversationReadFormat}, Messages: []Message{
		{ID: "u", Role: "user", ConversationVisible: true, CreatedAt: 10, Extensions: json.RawMessage(`{"role":"user"}`)},
		{ID: "a", Role: "assistant", ConversationVisible: true, Finish: "stop", CompletedAt: 25, Cost: 0.5, TokensInput: 7, TokensOutput: 3, TokensCacheRead: 2, TokensCacheWrite: 1, CreatedAt: 20, Position: 1, Extensions: json.RawMessage(`{"role":"assistant","providerID":"p","modelID":"model","finish":"stop","cost":0.5,"tokens":{"input":7,"output":3,"cache":{"read":2,"write":1}},"time":{"created":20,"completed":25}}`)},
	}, Parts: []Part{{ID: "p", MessageID: "u", Kind: "text", Text: "prose"}}}
}

func TestSessionSummariesAndRelationsAreInstanceScoped(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "second")
	for _, b := range []Batch{summaryBatch("first", "root", ""), summaryBatch("first", "child", "root"), summaryBatch("first", "other", ""), summaryBatch("second", "root", "child")} {
		commit(t, d, b)
	}
	deleted := summaryBatch("first", "deleted", "")
	deleted.Session.SourceDeleted = true
	commit(t, d, deleted)
	rows, err := d.SessionSummaries(t.Context(), "first", "/repo", "root")
	if err != nil || len(rows) != 1 {
		t.Fatalf("%+v %v", rows, err)
	}
	s := rows[0]
	if s.SessionID != "root" || s.LatestMessageID != "a" || s.LatestMessageCreated != 20 || s.Totals.Messages != 1 || s.Totals.Input != 7 || s.Totals.Output != 3 || s.Totals.Cost != 0.5 || s.Totals.CompletedAt != 25 || s.Totals.PromptAt != 10 || s.Totals.ActiveDuration != 5 || len(s.Totals.Models) != 1 || s.Totals.Models[0].CacheRead != 2 {
		t.Fatalf("summary lost persisted facts: %+v", s)
	}
	if rows, err := d.SessionSummaries(t.Context(), "first", "", ""); err != nil || len(rows) != 3 {
		t.Fatalf("inventory: %v %v", rows, err)
	}
	if rows, err := d.SessionSummaries(t.Context(), "first", "/elsewhere", ""); err != nil || len(rows) != 0 {
		t.Fatalf("directory: %v %v", rows, err)
	}
	if rows, err := d.SessionSummaries(t.Context(), "first", "", "deleted"); err != nil || len(rows) != 1 || !rows[0].SourceDeleted {
		t.Fatalf("retained summary: %v %v", rows, err)
	}
	parents, err := d.SessionParents(t.Context(), "first")
	if err != nil || parents["child"] != "root" || len(parents) != 3 {
		t.Fatalf("parents: %v %v", parents, err)
	}
	children, err := d.Descendants(t.Context(), Key{"first", "root"})
	if err != nil || !reflect.DeepEqual(children, []string{"root", "child"}) {
		t.Fatalf("children: %v %v", children, err)
	}
	usage, err := d.DescendantUsage(t.Context(), Key{"first", "root"})
	if err != nil || len(usage) != 2 || usage["child"][0].Paid != 0.5 {
		t.Fatalf("usage: %v %v", usage, err)
	}
	unread, err := d.UnreadCounts(t.Context(), "first", map[string]int64{"root": 10, "child": 20, "deleted": 0, "absent": 0})
	if err != nil || !reflect.DeepEqual(unread, map[string]int{"root": 1}) {
		t.Fatalf("unread: %v %v", unread, err)
	}
	if unread, err := d.UnreadCounts(t.Context(), "first", nil); err != nil || len(unread) != 0 {
		t.Fatalf("empty unread: %v %v", unread, err)
	}
}

func TestSummaryQueriesRejectMissingScopeAndClosedStores(t *testing.T) {
	d := openTestDB(t)
	queries := map[string]func() error{
		"summaries":   func() error { _, err := d.SessionSummaries(t.Context(), "", "", ""); return err },
		"parents":     func() error { _, err := d.SessionParents(t.Context(), ""); return err },
		"descendants": func() error { _, err := d.Descendants(t.Context(), Key{}); return err },
		"usage":       func() error { _, err := d.DescendantUsage(t.Context(), Key{}); return err },
		"unread":      func() error { _, err := d.UnreadCounts(t.Context(), "", nil); return err },
	}
	for name, query := range queries {
		if query() == nil {
			t.Fatalf("%s accepted missing scope", name)
		}
	}
	_ = d.Close()
	queries = map[string]func() error{
		"summaries":   func() error { _, err := d.SessionSummaries(t.Context(), "first", "", ""); return err },
		"parents":     func() error { _, err := d.SessionParents(t.Context(), "first"); return err },
		"descendants": func() error { _, err := d.Descendants(t.Context(), Key{"first", "s"}); return err },
		"usage":       func() error { _, err := d.DescendantUsage(t.Context(), Key{"first", "s"}); return err },
		"unread":      func() error { _, err := d.UnreadCounts(t.Context(), "first", map[string]int64{"s": 0}); return err },
	}
	for name, query := range queries {
		if query() == nil {
			t.Fatalf("%s accepted closed store", name)
		}
	}
}
