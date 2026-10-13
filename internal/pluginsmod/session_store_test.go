package pluginsmod

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestPluginSessionLifecycle(t *testing.T) {
	db := openPluginStore(t)
	ctx := context.Background()
	if err := db.LinkPluginSession(ctx, "opencode", "ses-1", "org.example.run"); err != nil {
		t.Fatal(err)
	}
	// Re-linking keeps the first claim.
	if err := db.LinkPluginSession(ctx, "opencode", "ses-1", "org.example.other"); err != nil {
		t.Fatal(err)
	}
	pluginID, linked, err := db.GetPluginSessionPlugin(ctx, "opencode", "ses-1")
	if err != nil || !linked || pluginID != "org.example.run" {
		t.Fatalf("lookup: %q %v %v", pluginID, linked, err)
	}
	if _, linked, err := db.GetPluginSessionPlugin(ctx, "opencode", "missing"); err != nil || linked {
		t.Fatalf("unlinked session reported: %v %v", linked, err)
	}
	sessions, err := db.ListPluginSessionSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].SessionID != "ses-1" || sessions[0].PluginID != "org.example.run" {
		t.Fatalf("list: %+v %v", sessions, err)
	}
	if err := db.LinkPluginSession(ctx, "", "ses-2", "org.example.run"); !errors.Is(err, state.ErrPluginInvalid) {
		t.Fatalf("accepted empty platform: %v", err)
	}
}

func TestPluginSessionOutboxDelivery(t *testing.T) {
	db := openPluginStore(t)
	ctx := context.Background()
	first, err := db.AppendPluginSessionCompletion(ctx, "org.example.run", "opencode", "ses-1", "ses-1:m2", "answer")
	if err != nil || first == 0 {
		t.Fatalf("append: %d %v", first, err)
	}
	// A repeated idle edge for the same turn appends nothing.
	if again, err := db.AppendPluginSessionCompletion(ctx, "org.example.run", "opencode", "ses-1", "ses-1:m2", "answer"); err != nil || again != 0 {
		t.Fatalf("duplicate append: %d %v", again, err)
	}
	// A different session's turn must not be held behind ses-1's head.
	if _, err := db.AppendPluginSessionCompletion(ctx, "org.example.run", "opencode", "ses-2", "ses-2:m1", "second"); err != nil {
		t.Fatal(err)
	}
	// A dead head holds only its own session: ses-1 queues turn 3 behind it.
	if _, err := db.AppendPluginSessionCompletion(ctx, "org.example.run", "opencode", "ses-1", "ses-1:m4", "third"); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimPluginSessionCompletions(ctx, 10)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if claimed[0].SessionID != "ses-1" || claimed[0].Text != "answer" {
		t.Fatalf("head: %+v", claimed[0])
	}
	if claimed[0].Group() != "org.example.run\x00opencode\x00ses-1" {
		t.Fatalf("group: %q", claimed[0].Group())
	}
	if current, err := db.PluginSessionCompletionCurrent(ctx, claimed[0].ID, claimed[0].Attempts); err != nil || !current {
		t.Fatalf("current: %v %v", current, err)
	}
	// A failed attempt keeps the row pending with a backoff until it expires.
	dead, err := db.FailPluginSessionCompletion(ctx, claimed[0].ID, "boom", time.Hour, 6)
	if err != nil || dead {
		t.Fatalf("fail: %v %v", dead, err)
	}
	due, err := db.ClaimPluginSessionCompletions(ctx, 10)
	if err != nil || len(due) != 1 || due[0].SessionID != "ses-2" {
		t.Fatalf("backoff not respected: %+v %v", due, err)
	}
	if current, err := db.PluginSessionCompletionCurrent(ctx, claimed[0].ID, claimed[0].Attempts); err != nil || current {
		t.Fatalf("stale claim trusted: %v %v", current, err)
	}
	// Exhausting attempts makes it a dead letter that holds only ses-1.
	if dead, err := db.FailPluginSessionCompletion(ctx, claimed[0].ID, "boom", 0, 1); err != nil || !dead {
		t.Fatalf("dead: %v %v", dead, err)
	}
	// Ack settles ses-2 and its receipt suppresses a re-append of the turn.
	if err := db.AckPluginSessionCompletion(ctx, due[0].ID); err != nil {
		t.Fatal(err)
	}
	if has, err := db.HasPluginSessionCompletion(ctx, "org.example.run", "ses-2:m1"); err != nil || !has {
		t.Fatalf("receipt missing: %v %v", has, err)
	}
	if again, err := db.AppendPluginSessionCompletion(ctx, "org.example.run", "opencode", "ses-2", "ses-2:m1", "second"); err != nil || again != 0 {
		t.Fatalf("re-append after ack: %d %v", again, err)
	}
	if err := db.AckPluginSessionCompletion(ctx, 99999); !errors.Is(err, state.ErrPluginNotFound) {
		t.Fatalf("ack of unknown row: %v", err)
	}
}

func TestPluginSessionBacklogFull(t *testing.T) {
	db := openPluginStore(t)
	ctx := context.Background()
	if err := db.PluginSessionBacklogFull(ctx, "org.example.run"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < state.PluginConversationOutboxMaxRows; i++ {
		if _, err := db.AppendPluginSessionCompletion(ctx, "org.example.run", "opencode", "ses-full", opid(i), "x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.PluginSessionBacklogFull(ctx, "org.example.run"); !errors.Is(err, state.ErrPluginConversationBacklogFull) {
		t.Fatalf("cap not enforced: %v", err)
	}
	// Another plugin's backlog is unaffected.
	if err := db.PluginSessionBacklogFull(ctx, "org.example.other"); err != nil {
		t.Fatal(err)
	}
}

func opid(i int) string {
	return "op-" + strconv.Itoa(i)
}
