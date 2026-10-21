package statev2

import (
	"testing"
)

func TestSearchTextUsesOwnerLocalRetainedProse(t *testing.T) {
	d := openTestDB(t)
	if err := d.RegisterInstance(t.Context(), Instance{ID: "foreign", OwnerID: "other", Platform: "test", SourceKey: "foreign"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		instance, id, role, kind string
		deleted                  bool
	}{
		{"first", "kept", "user", "text", false},
		{"first", "assistant", "assistant", "text", false},
		{"first", "tool", "assistant", "tool", false},
		{"first", "system", "system", "text", false},
		{"first", "deleted", "user", "text", true},
		{"foreign", "foreign", "user", "text", false},
	} {
		commit(t, d, Batch{Session: Session{Key: Key{InstanceID: row.instance, SessionID: row.id}, Directory: "/repo", UpdatedAt: 100, SourceDeleted: row.deleted}, Messages: []Message{{ID: "m", Role: row.role}}, Parts: []Part{{ID: "p", MessageID: "m", Kind: row.kind, Text: "UTF-8 café SEARCH prose"}}})
	}
	for _, tc := range []struct {
		query, dir  string
		since       int64
		limit, want int
	}{
		{" search ", "/repo", 100, 500, 2}, {"search", "/other", 100, 500, 0}, {"search", "", 101, 500, 0}, {"", "", 0, 500, 0}, {"search", "", 0, 1, 1},
	} {
		matches, err := d.SearchText(t.Context(), "owner", tc.query, tc.dir, tc.since, tc.limit)
		if err != nil || len(matches) != tc.want {
			t.Fatalf("%+v: %+v %v", tc, matches, err)
		}
		for _, m := range matches {
			if m.InstanceID != "first" || m.Platform != "opencode" || m.PartID != "p" || m.MessageID != "m" || m.Snippet != "UTF-8 café SEARCH prose" {
				t.Fatal(m)
			}
		}
	}
	for _, tc := range []struct {
		owner string
		limit int
	}{{"", 1}, {"owner", 0}, {"owner", 501}} {
		if _, err := d.SearchText(t.Context(), tc.owner, "search", "", 0, tc.limit); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
	_ = d.Close()
	if _, err := d.SearchText(t.Context(), "owner", "search", "", 0, 1); err == nil {
		t.Fatal("closed DB accepted")
	}
}
