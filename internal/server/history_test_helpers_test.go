package server

import (
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

func withReadyTestHistory(t *testing.T, srv *Server) *Server {
	t.Helper()
	store, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "fixture", OwnerID: "owner", Platform: "test", SourceKey: "fixture"}); err != nil {
		t.Fatal(err)
	}
	srv.WithConversationHistory(store, "owner")
	srv.historyAnalytics.Instances = func() ([]string, bool) { return []string{"fixture"}, true }
	return srv
}
