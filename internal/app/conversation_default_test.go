package app

import (
	"path/filepath"
	"testing"
)

func TestConversationHistoryStartsWithoutFeatureFlags(t *testing.T) {
	store, err := openConversationHistory(filepath.Join(t.TempDir(), "state.db"), "", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("conversation storage still requires a feature flag")
	}
	store.Close()
}
