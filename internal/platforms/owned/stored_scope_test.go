package owned

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestOldPhysicalSourcesStayReadableWithoutBorrowingCurrentNativeIDs(t *testing.T) {
	a, p, current := storedFixture(t)
	if _, err := a.store.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "old"}, []sessioncatalog.NativeSession{{ID: "native", Directory: "/old"}}); err != nil {
		t.Fatal(err)
	}
	old, err := a.store.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "old", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.history.RegisterInstance(t.Context(), statev2.Instance{ID: "old", OwnerID: "owner", Platform: "test", SourceKey: "old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "old", SessionID: old.ID}, Directory: "/old", Title: "old retained", ReadFormat: 1}, Messages: []statev2.Message{
		{ID: "u", Role: "user", ConversationVisible: true, CreatedAt: 30, Extensions: json.RawMessage(`{"role":"user"}`)},
		{ID: "a", Role: "assistant", ConversationVisible: true, Position: 1, CreatedAt: 40, Finish: "stop", Extensions: json.RawMessage(`{"role":"assistant","providerID":"old-provider","modelID":"old-model","finish":"stop","tokens":{"input":4,"output":2},"cost":1}`)},
	}}); err != nil {
		t.Fatal(err)
	}
	if !a.Owns(t.Context(), old.ID) || !a.Available(t.Context()) {
		t.Fatal("retained source lost read routing")
	}
	row, err := a.SessionSummary(t.Context(), old.ID)
	if err != nil || row.Title != "old retained" || row.Status == db.StatusBusy || row.LiveConnection {
		t.Fatalf("current source observation leaked into old identity: %+v %v", row, err)
	}
	if live := a.LiveStatus(old.ID); live != nil {
		t.Fatalf("native ID collision borrowed a live turn: %+v", live)
	}
	if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: old.ID}); !errors.Is(err, platforms.ErrHistoryUnavailable) || len(p.sent) != 0 {
		t.Fatalf("historical routing authorized native execution: %v", err)
	}
	usage, err := a.SessionUsage(t.Context(), old.ID)
	if err != nil || usage[old.ID].Cost != 1 {
		t.Fatalf("usage crossed source: %v %v", usage, err)
	}
	counts, err := a.UnreadCounts(t.Context(), map[string]int64{current: 10, old.ID: 0})
	if err != nil || counts[current] != 1 || counts[old.ID] != 2 {
		t.Fatalf("unread crossed source: %v %v", counts, err)
	}
	models, err := a.SessionModels(t.Context(), old.ID)
	if err != nil || len(models.Models) != 1 || models.Models[0].Provider != "old-provider" || models.HasProviders {
		t.Fatalf("old catalogue borrowed current source: %+v %v", models, err)
	}
	detail, err := a.Session(t.Context(), old.ID, 10, 0)
	if err != nil || len(detail.Messages) != 2 || detail.Session.Title != "old retained" {
		t.Fatalf("older retained format lost: %+v %v", detail, err)
	}
	a.unavailable = errors.New("native source unavailable")
	if !a.Available(t.Context()) {
		t.Fatal("native outage hid retained platform history")
	}
	if rows, err := a.Sessions(t.Context(), "", 0); err != nil || len(rows) != 2 {
		t.Fatalf("retained inventory unavailable: %+v %v", rows, err)
	}
}
