package state

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSessionReferenceMigrationRetainsQueueApprovalsAndReplacementRecovery(t *testing.T) {
	db := openTestStateDB(t)
	ctx := t.Context()
	for _, owner := range []string{"test", "r-other:test"} {
		if err := db.PinSession(ctx, owner, "native"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UnarchiveSession(ctx, "test", "native"); err != nil {
		t.Fatal(err)
	}
	if err := db.EnqueueMessage(ctx, QueuedMessage{ID: "queue", Platform: "test", SessionID: "native", Text: "native is user text", CreatedAt: 123}); err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePermissionInboxItem(ctx, InboxPermission{Platform: "test", SessionID: "native", PermissionID: "prompt", Permission: "read", Metadata: map[string]any{"sessionId": "opaque-native"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateSessionInboxItem(ctx, "notice", "body", "general", &InboxSession{Platform: "test", SessionID: "native"}); err != nil {
		t.Fatal(err)
	}
	routine := testRoutine("routine", "rekey", 1)
	routine.Platform, routine.SessionID = "test", "native"
	if err := db.CreateRoutine(ctx, routine); err != nil {
		t.Fatal(err)
	}
	baseline := map[string]SessionInterruption{"native": {MessageID: "native-message", ObservedAt: 123, Message: "preserve"}}
	if err := db.PrepareSessionInterruptions(ctx, "test", "/repo", baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE session_replacement SET stop_baseline_json=baseline_json`); err != nil {
		t.Fatal(err)
	}
	refs, err := db.SessionReferences(ctx, "test")
	if err != nil || !reflect.DeepEqual(refs, []string{"native"}) {
		t.Fatalf("reference inventory: %+v %v", refs, err)
	}
	if err := db.RekeySessionReferences(ctx, "test", map[string]string{"native": "ocm_canonical"}); err != nil {
		t.Fatal(err)
	}
	refs, err = db.SessionReferences(ctx, "test")
	if err != nil || !reflect.DeepEqual(refs, []string{"ocm_canonical"}) {
		t.Fatalf("legacy core references remain: %+v %v", refs, err)
	}
	foreign, err := db.SessionReferences(ctx, "r-other:test")
	if err != nil || !reflect.DeepEqual(foreign, []string{"native"}) {
		t.Fatalf("another owner changed: %+v %v", foreign, err)
	}
	queue, err := db.ListQueuedMessages(ctx, "test", "ocm_canonical")
	if err != nil || len(queue) != 1 || queue[0].ID != "queue" || queue[0].Text != "native is user text" || queue[0].CreatedAt != 123 {
		t.Fatalf("queue changed: %+v %v", queue, err)
	}
	stored, err := db.GetRoutine(ctx, routine.ID)
	if err != nil || stored.SessionID != "ocm_canonical" || stored.Prompt != routine.Prompt {
		t.Fatalf("routine changed: %+v %v", stored, err)
	}
	items, err := db.ListInboxItems(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("notification history changed: %+v %v", items, err)
	}
	for _, item := range items {
		if item.Permission != nil {
			if item.ID != permissionInboxID("test", "native", "prompt") || item.Permission.SessionID != "ocm_canonical" || item.Permission.Metadata["sessionId"] != "opaque-native" {
				t.Fatalf("notification correlation or opaque metadata changed: %+v", item)
			}
		} else if item.Session == nil || item.Session.SessionID != "ocm_canonical" {
			t.Fatalf("session notice was not rekeyed: %+v", item)
		}
	}
	for _, column := range []string{"baseline_json", "stop_baseline_json"} {
		var raw string
		if err := db.db.QueryRow("SELECT " + column + " FROM session_replacement WHERE platform='test'").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var actual map[string]SessionInterruption
		if err := json.Unmarshal([]byte(raw), &actual); err != nil {
			t.Fatal(err)
		}
		if len(actual) != 1 || actual["ocm_canonical"] != baseline["native"] {
			t.Fatalf("replacement snapshot changed: %s", raw)
		}
	}
	backups, err := filepath.Glob(filepath.Join(db.dataDir, "*.backup-canonical-session-keys-*.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("missing pre-cutover backup: %+v %v", backups, err)
	}
	if err := db.RekeySessionReferences(ctx, "test", map[string]string{"native": "ocm_canonical"}); err != nil {
		t.Fatal(err)
	}
	after, _ := filepath.Glob(filepath.Join(db.dataDir, "*.backup-canonical-session-keys-*.db"))
	if len(after) != 1 {
		t.Fatal("replay mutated already-rekeyed state")
	}
}

func TestSessionReferenceMigrationRollsBackCollidingDurableState(t *testing.T) {
	for _, conflict := range []string{"pin", "replacement snapshot"} {
		t.Run(conflict, func(t *testing.T) {
			db := openTestStateDB(t)
			if err := db.PinSession(t.Context(), "test", "native"); err != nil {
				t.Fatal(err)
			}
			if conflict == "pin" {
				if err := db.PinSession(t.Context(), "test", "ocm_canonical"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := db.PrepareSessionInterruptions(t.Context(), "test", "/repo", map[string]SessionInterruption{"native": {MessageID: "one"}, "ocm_canonical": {MessageID: "two"}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.EnqueueMessage(t.Context(), QueuedMessage{ID: "queue", Platform: "test", SessionID: "native", Text: "retained"}); err != nil {
				t.Fatal(err)
			}
			if err := db.RekeySessionReferences(t.Context(), "test", map[string]string{"native": "ocm_canonical"}); err == nil {
				t.Fatal("migration combined colliding durable state")
			}
			queue, err := db.ListQueuedMessages(t.Context(), "test", "native")
			if err != nil || len(queue) != 1 || queue[0].Text != "retained" {
				t.Fatalf("failed migration partially changed queue: %+v %v", queue, err)
			}
		})
	}
}

func TestSessionReferenceMigrationRejectsAmbiguousMapping(t *testing.T) {
	db := openTestStateDB(t)
	for _, mapping := range []map[string]string{{"": "id"}, {"old": ""}, {"one": "same", "two": "same"}, {"one": "two", "two": "three"}} {
		if err := db.RekeySessionReferences(t.Context(), "test", mapping); err == nil {
			t.Fatalf("invalid mapping accepted: %+v", mapping)
		}
	}
	if _, err := db.SessionReferences(t.Context(), ""); err == nil {
		t.Fatal("missing routing owner accepted")
	}
}
