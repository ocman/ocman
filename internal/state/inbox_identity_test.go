package state

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPermissionInboxOpaqueKeyKeepsResolvedPromptClosedAfterSessionRekey(t *testing.T) {
	db := openTestStateDB(t)
	key := permissionInboxID("test", "old-native", "prompt")
	if err := db.ResolvePermissionInboxItemKey(t.Context(), key, "test", "ocm_canonical", "prompt"); err != nil {
		t.Fatal(err)
	}
	permission := InboxPermission{ItemID: key, Platform: "test", SessionID: "ocm_canonical", PermissionID: "prompt", Permission: "read"}
	if err := db.EnsurePermissionInboxItem(t.Context(), permission); err != nil {
		t.Fatal(err)
	}
	if active, err := db.ListInboxItems(t.Context()); err != nil || len(active) != 0 {
		t.Fatalf("migration reopened resolved prompt: %+v %v", active, err)
	}
	archived, err := db.ListArchivedInboxItems(t.Context())
	if err != nil || len(archived) != 1 || archived[0].ID != key || archived[0].Permission == nil || archived[0].Permission.SessionID != "ocm_canonical" {
		t.Fatalf("resolved prompt history: %+v %v", archived, err)
	}
	raw, err := json.Marshal(permission)
	if err != nil || strings.Contains(string(raw), key) || strings.Contains(string(raw), "ItemID") {
		t.Fatalf("internal correlation key leaked in JSON: %s %v", raw, err)
	}
	var decoded InboxPermission
	if err := json.Unmarshal([]byte(`{"ItemID":"untrusted","sessionId":"ocm_canonical"}`), &decoded); err != nil || decoded.ItemID != "" {
		t.Fatalf("JSON supplied an internal correlation key: %+v %v", decoded, err)
	}
	if err := db.ResolvePermissionInboxItemKey(t.Context(), "", "test", "ocm_canonical", "prompt"); err == nil {
		t.Fatal("empty correlation key accepted")
	}
}
