package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type familyPlatform struct {
	*nativePlatform
	requested string
	err       error
}

func (p *familyPlatform) HistoryFamilyInventory(_ context.Context, id string) ([]db.HistoryEntry, error) {
	p.requested = id
	return []db.HistoryEntry{{ID: id, Version: "v1"}, {ID: "child", Version: "v2"}}, p.err
}

func TestHistoryFamilyRoutesCanonicalIdentitiesAndKeepsSourceFence(t *testing.T) {
	a, p, _ := fixture(t)
	if _, err := a.HistoryFamilyInventory(t.Context(), "missing"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("unsupported discovery: %v", err)
	}
	row, err := a.historyIdentity(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	family := &familyPlatform{nativePlatform: p}
	a.native = family
	rows, err := a.HistoryFamilyInventory(t.Context(), row.ID)
	if err != nil || len(rows) != 2 || family.requested != "native" {
		t.Fatalf("family routing: %+v %v request=%q", rows, err, family.requested)
	}
	if rows[0].ID != row.ID || !sessioncatalog.IsID(rows[1].ID) {
		t.Fatalf("native references escaped scoped discovery: %+v", rows)
	}
	if _, err := a.HistoryFamilyInventory(t.Context(), "missing"); err == nil {
		t.Fatal("unknown canonical reference routed")
	}
	family.err = errors.New("discovery failed")
	if _, err := a.HistoryFamilyInventory(t.Context(), row.ID); !errors.Is(err, family.err) {
		t.Fatalf("discovery failure hidden: %v", err)
	}
	p.source = "another-source"
	if _, err := a.HistoryFamilyInventory(t.Context(), row.ID); err == nil {
		t.Fatal("changed source accepted")
	}
}
