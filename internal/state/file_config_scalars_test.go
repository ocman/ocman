package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileConfigFalseSpellingsDisableSecuritySettings(t *testing.T) {
	for _, spelling := range []string{"false", "False", "FALSE"} {
		t.Run(spelling, func(t *testing.T) {
			db := openTestStateDB(t)
			path := filepath.Join(t.TempDir(), "config.yaml")
			data := "version: 1\nsettings:\n  worktree.inherit_permissions: " + spelling + "\n  sharing_enabled: " + spelling + "\n"
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := db.EnableFileConfig(t.Context(), path); err != nil {
				t.Fatal(err)
			}
			if enabled, err := db.GetWorktreeInheritPermissions(t.Context()); err != nil || enabled {
				t.Fatalf("permission inheritance was enabled: %v", err)
			}
			if value, ok, err := db.GetSetting(t.Context(), "sharing_enabled"); err != nil || !ok || value != "false" {
				t.Fatalf("sharing was enabled: %q, %v", value, err)
			}
		})
	}
}
