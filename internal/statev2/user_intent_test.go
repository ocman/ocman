package statev2

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
	if got, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "session", 17); err != nil || got.Limited || !reflect.DeepEqual(got.Messages, []string{"first", "second piece"}) {
		t.Fatalf("complete prose: %v %v", got, err)
	}
	if got, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "session", 16); err != nil || !got.Limited || !reflect.DeepEqual(got.Messages, []string{"irst", "second piece"}) {
		t.Fatalf("windowed prose: %v %v", got, err)
	}
	if _, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "session", 0); err == nil {
		t.Fatal("invalid byte budget accepted")
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

func TestReviewUserMessagesBoundsBytesNotCount(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Checkpoint = nil
	b.Messages, b.Parts = nil, nil
	for i := range 150 {
		id := fmt.Sprintf("u%03d", i)
		b.Messages = append(b.Messages, Message{ID: id, Role: "user", Position: int64(i)})
		b.Parts = append(b.Parts, Part{ID: "p" + id, MessageID: id, Kind: "text", Text: "é"})
	}
	b.Parts[0].Text = "Do not push."
	b.Parts[len(b.Parts)-1].Text = "Still do not push."
	commit(t, d, b)
	bytes := len(b.Parts[0].Text) + 148*len("é") + len(b.Parts[149].Text)
	userContext, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "session", bytes)
	if err != nil || userContext.Limited || len(userContext.Messages) != 150 || userContext.Messages[0] != "Do not push." || userContext.Messages[149] != "Still do not push." {
		t.Fatalf("lost restrictions: %v %v", userContext, err)
	}
	if got, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "session", bytes-1); err != nil || !got.Limited || got.Digest != userContext.Digest || len(strings.Join(got.Messages, "")) != bytes-1 {
		t.Fatalf("limited UTF-8 history: %v %v", got, err)
	}
	b.Parts[0].Text = strings.Repeat("x", 32_001)
	b.ExpectedRevision = 1
	commit(t, d, b)
	if got, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "session", 32_000); err != nil || !got.Limited || got.Digest == userContext.Digest || len(strings.Join(got.Messages, "")) != 32_000 {
		t.Fatalf("oversized earlier restriction: %v %v", got, err)
	}
}
