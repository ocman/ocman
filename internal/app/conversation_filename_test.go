package app

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
)

func TestConversationFilenameAdoptsWALOnceAndResetKeepsApplicationState(t *testing.T) {
	root := t.TempDir()
	corePath := filepath.Join(root, "state.db")
	core, err := state.Open(corePath)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if err := core.SetSetting(t.Context(), "user-setting", "kept"); err != nil {
		t.Fatal(err)
	}
	legacy, err := statev2.Open(filepath.Join(root, "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if err := legacy.RegisterInstance(t.Context(), statev2.Instance{ID: "source", OwnerID: "owner", Platform: "test", SourceKey: "native"}); err != nil {
		t.Fatal(err)
	}
	// Keep the source connection open: the snapshot must include WAL content.
	store, err := openApplicationHistory(t.Context(), core, corePath, "", historyEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if store.FilePath() != filepath.Join(root, "state_conv.db") {
		t.Fatal(store.FilePath())
	}
	if rows, err := store.Instances(t.Context(), "owner", "test"); err != nil || len(rows) != 1 {
		t.Fatalf("WAL data lost in adoption: %+v %v", rows, err)
	}
	if _, err := os.Stat(legacy.FilePath()); err != nil {
		t.Fatal("original database removed", err)
	}
	path := store.FilePath()
	_ = store.Close()
	// An explicit offline reset deletes only the conversation database. No old
	// statev2 data should be silently resurrected on the next startup.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store, err = openApplicationHistory(t.Context(), core, corePath, "", historyEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if rows, err := store.Instances(t.Context(), "owner", "test"); err != nil || len(rows) != 0 {
		t.Fatalf("reset resurrected legacy history: %+v %v", rows, err)
	}
	if value, ok, err := core.GetSetting(t.Context(), "user-setting"); err != nil || !ok || value != "kept" {
		t.Fatalf("reset lost application state: %q %v %v", value, ok, err)
	}
}

func TestConversationFilenameNeverOverwritesExistingTarget(t *testing.T) {
	root := t.TempDir()
	core, err := state.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	target := filepath.Join(root, "state_conv.db")
	store, err := statev2.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "new", OwnerID: "owner", Platform: "test", SourceKey: "new"}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	if err := os.WriteFile(filepath.Join(root, "statev2.db"), []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err = openApplicationHistory(t.Context(), core, filepath.Join(root, "state.db"), "", historyEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if rows, err := store.Instances(t.Context(), "owner", "test"); err != nil || len(rows) != 1 || rows[0].ID != "new" {
		t.Fatalf("existing destination overwritten: %+v %v", rows, err)
	}
}

func TestConversationFilenameRejectsForeignAndLinkedLegacyFiles(t *testing.T) {
	for _, kind := range []string{"foreign", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "statev2.db")
			if kind == "foreign" {
				store, err := state.Open(source)
				if err != nil {
					t.Fatal(err)
				}
				_ = store.Close()
			} else if err := os.Symlink("elsewhere", source); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "state_conv.db")
			if err := adoptLegacyConversationFile(t.Context(), target); err == nil {
				t.Fatal("unsafe source adopted")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("unsafe adoption published a target", err)
			}
		})
	}
}

func TestConversationPathAliases(t *testing.T) {
	for _, name := range []string{"conversations-db", "statev2-db"} {
		flags := flag.NewFlagSet("test", flag.ContinueOnError)
		path := conversationHistoryFlag(flags)
		if err := flags.Parse([]string{"-" + name + "=/custom/db"}); err != nil || *path != "/custom/db" {
			t.Fatalf("%s: %q %v", name, *path, err)
		}
	}
	for name, tc := range map[string]struct{ flag, current, legacy, want string }{
		"new env":    {current: "/new/db", legacy: "/old/db", want: "/new/db"},
		"legacy env": {legacy: "/old/db", want: "/old/db"},
		"flag wins":  {flag: "/flag/db", current: "/new/db", legacy: "/old/db", want: "/flag/db"},
		"derived":    {want: "/isolated/state_conv.db"},
	} {
		t.Run(name, func(t *testing.T) {
			getenv := historyEnv(map[string]string{historyDBEnv: tc.current, "OCMAN_STATEV2_DB": tc.legacy})
			if got := conversationHistoryPath("/isolated/state.db", tc.flag, getenv); got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConversationAdoptionHonorsCanceledContext(t *testing.T) {
	root := t.TempDir()
	core, err := state.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if store, err := openApplicationHistory(ctx, core, filepath.Join(root, "state.db"), "", historyEnv(nil)); err == nil {
		_ = store.Close()
		t.Fatal("canceled adoption accepted")
	}
}
