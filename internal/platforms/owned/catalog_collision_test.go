package owned

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestModelCatalogSurvivesMessageIDsSharedByForks(t *testing.T) {
	a, p, id := storedFixture(t)
	fork, err := a.historyIdentity(t.Context(), "fork")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.history.Commit(t.Context(), statev2.Batch{
		Session: statev2.Session{Key: statev2.Key{InstanceID: "source", SessionID: fork.ID}, Directory: "/fork", UpdatedAt: 40},
		Messages: []statev2.Message{{ID: "a", Role: "assistant", CreatedAt: 40,
			Extensions: json.RawMessage(`{"role":"assistant","providerID":"p","modelID":"fork-model"}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	a.native = &liveStoredCatalog{nativePlatform: p, response: &platforms.DirectoryCatalog{
		Models: &platforms.SessionModelsResponse{HasProviders: true, Models: []platforms.SessionModel{
			{Provider: "anthropic", Model: "opus", IsAvailable: true},
		}},
	}}
	for _, session := range []string{"", id} {
		catalog, err := a.storedDirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/stored"}, session)
		if err != nil {
			t.Fatalf("shared message IDs hid the model catalog: %v", err)
		}
		if !catalog.Models.HasProviders || len(catalog.Models.Models) != 1 || catalog.Models.Models[0].Model != "opus" {
			t.Fatalf("live models missing: %+v", catalog.Models)
		}
		want := "p/model"
		if session != "" {
			want = "p/fork-model"
		}
		if catalog.DefaultModel != want {
			t.Fatalf("another session's message replaced the directory default: %+v", catalog)
		}
	}
}
