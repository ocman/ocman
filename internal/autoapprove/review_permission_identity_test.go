package autoapprove

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type stableManualPolicyPlatform struct{ manualPolicyPlatform }

func (stableManualPolicyPlatform) PermissionInboxKey(context.Context, string, string, string) (string, error) {
	return "legacy-permission-key", nil
}

func TestManualPermissionPolicyKeepsMigratedInboxKeyForAskAndReply(t *testing.T) {
	store := statetest.Open(t)
	const id = "ocm_00000000-0000-4000-8000-000000000001"
	if err := store.EnsurePermissionInboxItem(t.Context(), state.InboxPermission{ItemID: "legacy-permission-key", Platform: "fake", SessionID: id, PermissionID: "prompt", Permission: "bash"}); err != nil {
		t.Fatal(err)
	}
	recorder := &replyRecorder{}
	p := stableManualPolicyPlatform{manualPolicyPlatform{fakePlatform: &fakePlatform{respondPermissionFn: recorder.record}, pending: []platforms.LivePrompt{{"id": "prompt", "sessionID": id, "permission": "bash", "patterns": []string{"echo test"}}}}}
	m := newTestModule(store)
	saveManualRules(t, store, "fake", id, []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "ask"}})
	m.applyManualToPending(t.Context(), p, id)
	items, err := store.ListInboxItems(t.Context())
	if err != nil || len(items) != 1 || items[0].ID != "legacy-permission-key" {
		t.Fatalf("manual policy rekeyed notification: %+v %v", items, err)
	}
	saveManualRules(t, store, "fake", id, []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}})
	m.applyManualToPending(t.Context(), p, id)
	items, err = store.ListInboxItems(t.Context())
	if err != nil || len(items) != 0 || recorder.count() != 1 {
		t.Fatalf("manual reply did not resolve migrated request: %+v %v", items, err)
	}
	archived, err := store.ListArchivedInboxItems(t.Context())
	if err != nil || len(archived) != 1 || archived[0].ID != "legacy-permission-key" {
		t.Fatalf("manual reply archived a different key: %+v %v", archived, err)
	}
}
