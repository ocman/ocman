package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestSessionModelsRetainCanonicalHistoryWhenCatalogFails(t *testing.T) {
	a, p, id := storedFixture(t)
	a.native = &liveStoredCatalog{nativePlatform: p, err: errors.New("location no longer exists")}
	models, err := a.SessionModels(t.Context(), id)
	if err != nil || models == nil || models.HasProviders || len(models.Models) != 1 || models.Models[0].Provider != "p" || models.Models[0].Model != "model" {
		t.Fatalf("canonical model history lost: %+v %v", models, err)
	}
	if _, err := a.DirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/stored"}); err == nil {
		t.Fatal("new-session preparation concealed the live catalog failure")
	}
	if _, err := a.AgentCatalog(t.Context(), id); err == nil {
		t.Fatal("failed agent catalog became an empty successful list")
	}
	if _, err := a.Session(t.Context(), id, 10, 0); err != nil {
		t.Fatalf("catalog failure made retained conversation unreadable: %v", err)
	}
	if _, err := a.SessionModels(t.Context(), "unrelated-native-id"); err == nil {
		t.Fatal("unknown identity obtained model history")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.SessionModels(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	_ = a.history.Close()
	if _, err := a.SessionModels(t.Context(), id); err == nil {
		t.Fatal("unavailable canonical history became an empty successful list")
	}
}
