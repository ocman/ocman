package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestDevelopmentConversationFileAdoptsOldBuildDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	legacy, err := statev2.Open(filepath.Join(root, "tmp", "state_conv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if err := legacy.RegisterInstance(t.Context(), statev2.Instance{ID: "source", OwnerID: "owner", Platform: "test", SourceKey: "retained"}); err != nil {
		t.Fatal(err)
	}
	corePath := filepath.Join(root, "state.db")
	core, err := state.Open(corePath)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	store, err := openApplicationHistory(t.Context(), core, corePath, "", historyEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.Instances(t.Context(), "owner", "test")
	if err != nil || len(rows) != 1 || rows[0].SourceKey != "retained" {
		t.Fatalf("lost old development history: %+v %v", rows, err)
	}
	if _, err := os.Stat(legacy.FilePath()); err != nil {
		t.Fatal("original removed", err)
	}
}
