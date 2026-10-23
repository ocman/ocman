package state

import (
	"path/filepath"
	"testing"
)

func TestSessionCutoverSharesBackupAcrossBatchesAndFeatures(t *testing.T) {
	d := openTestStateDB(t)
	ctx := WithSessionMigrationBackup(t.Context())
	for _, id := range []string{"one", "two"} {
		if err := d.PinSession(ctx, "test", id); err != nil {
			t.Fatal(err)
		}
		if err := d.RekeySessionReferences(ctx, "test", map[string]string{id: "ocm_" + id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.BackupSessionReferences(ctx); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(d.dataDir, "*.backup-*-session-keys-*.db"))
	if err != nil || len(files) != 1 {
		t.Fatalf("cutover repeated full database backups: %v %v", files, err)
	}
	if err := d.BackupSessionReferences(WithSessionMigrationBackup(t.Context())); err != nil {
		t.Fatal(err)
	}
	files, _ = filepath.Glob(filepath.Join(d.dataDir, "*.backup-*-session-keys-*.db"))
	if len(files) != 2 {
		t.Fatal("independent cutover did not take its own backup")
	}
}
