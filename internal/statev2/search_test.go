package statev2

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSearchSessionTextLimitsSessionsAndExcludesHiddenContent(t *testing.T) {
	d := openTestDB(t)
	if err := d.RegisterInstance(t.Context(), Instance{ID: "foreign", OwnerID: "other", Platform: "test", SourceKey: "foreign"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		instance, id, role, kind                     string
		updated                                      int64
		hidden, deleted, deletedMessage, deletedPart bool
	}{
		{instance: "first", id: "recent", role: "assistant", kind: "text", updated: 2},
		{instance: "first", id: "old", role: "user", kind: "text", updated: 1},
		{instance: "first", id: "tool", role: "assistant", kind: "tool", updated: 3},
		{instance: "first", id: "system", role: "system", kind: "text", updated: 3},
		{instance: "first", id: "hidden", role: "assistant", kind: "text", updated: 3, hidden: true},
		{instance: "first", id: "deleted-session", role: "user", kind: "text", updated: 3, deleted: true},
		{instance: "first", id: "deleted-message", role: "user", kind: "text", updated: 3, deletedMessage: true},
		{instance: "first", id: "deleted-part", role: "user", kind: "text", updated: 3, deletedPart: true},
		{instance: "foreign", id: "foreign", role: "user", kind: "text", updated: 3},
	} {
		batch := Batch{
			Session:  Session{Key: Key{row.instance, row.id}, Title: "Unrelated title", Directory: "/repo", UpdatedAt: row.updated, ReadFormat: ConversationReadFormat, SourceDeleted: row.deleted},
			Messages: []Message{{ID: "m", Role: row.role, ConversationVisible: !row.hidden, SourceDeleted: row.deletedMessage}},
			Parts:    []Part{{ID: "p", MessageID: "m", Kind: row.kind, Text: "text with NEEDLE", SourceDeleted: row.deletedPart}},
		}
		if row.id == "recent" {
			batch.Messages = append(batch.Messages, Message{ID: "hidden", Role: "assistant"})
			batch.Parts = append(batch.Parts, Part{ID: "p24", MessageID: "hidden", Kind: "text", Text: "hidden needle"})
			for i := range 25 {
				batch.Parts = append(batch.Parts, Part{ID: fmt.Sprintf("p%d", i), MessageID: "m", Kind: "text", CreatedAt: int64(i + 1), Text: "latest needle"})
			}
		}
		commit(t, d, batch)
	}
	matches, err := d.SearchSessionText(t.Context(), "owner", " Needle ")
	if err != nil || len(matches) != 2 || matches[0].ID != "recent" || matches[0].Snippet != "latest needle" || matches[1].ID != "old" || matches[1].Snippet != "text with NEEDLE" {
		t.Fatalf("unexpected matches: %+v, %v", matches, err)
	}
	for _, query := range []string{"", "Unrelated title", "missing"} {
		if matches, err := d.SearchSessionText(t.Context(), "owner", query); err != nil || matches == nil || len(matches) != 0 {
			t.Fatalf("%q: %+v, %v", query, matches, err)
		}
	}
}

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

func TestSnippetAndASCIIOnlyCaseFolding(t *testing.T) {
	if got := asciiLower("CAFÉ Test"); got != "cafÉ test" {
		t.Fatal(got)
	}
	for _, tc := range []struct{ text, needle, want string }{
		{"a\n b needle c", "needle", "a b needle c"},
		{"abc", "missing", "abc"},
		{strings.Repeat("é", 90) + "NEEDLE" + strings.Repeat("é", 90), "needle", "…" + strings.Repeat("é", 40) + "NEEDLE" + strings.Repeat("é", 40) + "…"},
	} {
		got := searchSnippet(tc.text, tc.needle)
		if got != tc.want || !utf8.ValidString(got) {
			t.Fatalf("%q want %q", got, tc.want)
		}
	}
}
