package state

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestCoreSessionRekeyPreservesPluginThreadAndOpaqueOutboxReceipts(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	key := PluginConversationKey{PluginID: "plugin", AccountID: "account", ThreadID: "thread"}
	if _, _, err := db.LinkPluginConversation(t.Context(), key, PluginConversationSession{PlatformID: "test", SessionID: "native"}); err != nil {
		t.Fatal(err)
	}
	id, err := db.AppendPluginConversationReply(t.Context(), key, "native:message", "already owed")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := db.SessionReferences(t.Context(), "test")
	if err != nil || !slices.Equal(refs, []string{"native"}) {
		t.Fatalf("plugin conversation omitted from reference inventory: %+v %v", refs, err)
	}
	canonical := "ocm_" + uuid.NewString()
	if err := db.RekeySessionReferences(t.Context(), "test", map[string]string{"native": canonical}); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := db.GetPluginConversation(t.Context(), key)
	if err != nil || !ok || stored.SessionID != canonical || stored.PlatformID != "test" {
		t.Fatalf("thread mapping changed: %+v %v %v", stored, ok, err)
	}
	rows, err := db.ClaimPluginConversationReplies(t.Context(), 10)
	if err != nil || len(rows) != 1 || rows[0].ID != id || rows[0].Text != "already owed" {
		t.Fatalf("owed reply changed: %+v %v", rows, err)
	}
	if exists, err := db.HasPluginConversationReply(t.Context(), key, "native:message"); err != nil || !exists {
		t.Fatalf("opaque receipt was rekeyed: %v %v", exists, err)
	}
}
