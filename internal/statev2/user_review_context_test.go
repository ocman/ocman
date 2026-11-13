package statev2

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReviewUserMessagesWindowAndDigest(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Checkpoint = nil
	b.Messages = []Message{{ID: "old", Role: "user"}, {ID: "new", Role: "user", Position: 1}}
	b.Parts = []Part{
		{ID: "old", MessageID: "old", Kind: "text", Text: "Do not push."},
		{ID: "start", MessageID: "new", Kind: "text", Text: strings.Repeat("é", 10)},
		{ID: "end", MessageID: "new", Kind: "text", Position: 1, Text: "Run tests."},
	}
	commit(t, d, b)
	first, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "session", 15)
	if err != nil || !first.Limited || !reflect.DeepEqual(first.Messages, []string{"ééRun tests."}) || !utf8.ValidString(first.Messages[0]) || first.Digest == "" {
		t.Fatalf("window=%+v err=%v", first, err)
	}
	// The submitted window stays identical, but an omitted restriction changed.
	b.Parts[0].Text = "Never deploy."
	b.ExpectedRevision = 1
	commit(t, d, b)
	second, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "session", 15)
	if err != nil || !reflect.DeepEqual(first.Messages, second.Messages) || first.Digest == second.Digest {
		t.Fatalf("omitted change was not tracked: first=%+v second=%+v err=%v", first, second, err)
	}
	if _, err := d.ReviewUserMessages(t.Context(), "wrong-owner", "opencode", "session", 15); err == nil {
		t.Fatal("wrong owner accepted")
	}
	if _, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "missing", 15); err == nil {
		t.Fatal("missing session accepted")
	}
	_ = d.Close()
	if _, err := d.ReviewUserMessages(t.Context(), "owner", "opencode", "session", 15); err == nil {
		t.Fatal("failed read accepted as limited context")
	}
}
