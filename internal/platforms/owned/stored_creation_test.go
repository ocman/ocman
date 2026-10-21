package owned

import (
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
)

func TestCreationMetadataIsStoredBeforePublicationAndNeverOverwritesIngestion(t *testing.T) {
	_, p, store := fixture(t)
	// A fresh read model: creation registers the source instance itself.
	history := statev2test.Open(t)
	a, err := New(t.Context(), p, store, history, "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/new", Title: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CommitCreation(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	row, err := a.SessionMetadata(t.Context(), created.ID)
	if err != nil || row.Directory != "/new" || row.Title != "new" {
		t.Fatalf("creation metadata not readable from statev2: %+v %v", row, err)
	}
	instance, err := history.FindInstance(t.Context(), "owner", "test", "source")
	if err != nil || instance.ID != statev2.SourceInstanceID("owner", "test", "source") {
		t.Fatalf("source identity drift: %+v %v", instance, err)
	}
	key := statev2.Key{InstanceID: instance.ID, SessionID: created.ID}
	s, _, err := history.SessionState(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	s.Title = "ingested"
	s.Extensions = json.RawMessage(`{"SourceChild":false}`)
	if _, err := history.Commit(t.Context(), statev2.Batch{Session: s, ExpectedRevision: s.Revision}); err != nil {
		t.Fatal(err)
	}
	if err := a.CommitCreation(t.Context(), created.ID); err != nil {
		t.Fatal(err)
	}
	if row, err := a.SessionMetadata(t.Context(), created.ID); err != nil || row.Title != "ingested" {
		t.Fatalf("creation retry overwrote imported metadata: %+v %v", row, err)
	}
	if err := history.Purge(t.Context(), key); err != nil {
		t.Fatal(err)
	}
	if err := a.CommitCreation(t.Context(), created.ID); err == nil {
		t.Fatal("purged bootstrap republished")
	}
}

func TestUserLinkedWorktreeChildrenRemainVisibleAndDoNotBubbleAsNativeDescendants(t *testing.T) {
	a, _, parent := storedFixture(t)
	child, err := a.historyIdentity(t.Context(), "worktree-child")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "source", SessionID: child.ID}, Directory: "/worktree", ParentID: parent, Title: "user child", Extensions: json.RawMessage(`{"SourceChild":false}`)}}); err != nil {
		t.Fatal(err)
	}
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ordinary worktree child was filtered as an inactive native helper: %+v %v", rows, err)
	}
	descendants, err := a.SessionDescendants(t.Context(), parent)
	if err != nil || len(descendants) != 1 || descendants[0] != parent {
		t.Fatalf("user link became native descendant authority: %v %v", descendants, err)
	}
	parents, err := a.history.SessionParents(t.Context(), "source")
	if err != nil || parents[child.ID] != "" {
		t.Fatalf("worktree permission would bubble to another session: %v %v", parents, err)
	}
}
