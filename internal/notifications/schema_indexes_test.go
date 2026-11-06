package notifications

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/state"
)

func TestDeliveryIndexUpgradePreservesReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	oldSchema := Schema
	oldSchema.Migrations = oldSchema.Migrations[:1]
	db, err := state.Open(path, oldSchema)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO push_subscription VALUES('endpoint','{}',1);
		INSERT INTO push_delivery VALUES('endpoint','done','{}',2,1,1,1),('endpoint','pending','{}',1,2,2,0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = state.Open(path, Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count, done, attempts int
	if err := db.SQL().QueryRow(`SELECT count(*),sum(done),sum(attempts) FROM push_delivery`).Scan(&count, &done, &attempts); err != nil {
		t.Fatal(err)
	}
	if count != 2 || done != 1 || attempts != 3 {
		t.Fatalf("upgrade changed receipts: %d, %d, %d", count, done, attempts)
	}
	if _, err := db.SQL().Exec(`INSERT OR IGNORE INTO push_delivery VALUES('endpoint','done','{}',0,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL().QueryRow(`SELECT count(*) FROM push_delivery`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("delivered receipt replayed: %d, %v", count, err)
	}
}
