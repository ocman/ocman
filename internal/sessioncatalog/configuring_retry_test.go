package sessioncatalog

import (
	"testing"
	"time"
)

func TestCatalogueRetriesConfiguringMetadataAfterBindWithoutHistoryWrite(t *testing.T) {
	s, _ := openStore(t)
	h := metadataHistory(t)
	reserved, _, err := s.Reserve(t.Context(), reservation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Attach(t.Context(), reserved.ID, binding()); err != nil {
		t.Fatal(err)
	}
	writeMetadata(t, h, binding().NativeID, "", "/observed")
	m := NewModule(Deps{Store: s, History: h, OwnerID: reserved.OwnerID})
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(m.pending) != 1 {
		t.Fatalf("configuring metadata lost from retry tracking: %d", len(m.pending))
	}
	if _, err := s.Bind(t.Context(), reserved.ID, binding()); err != nil {
		t.Fatal(err)
	}
	m.pendingRetryAt = time.Time{}
	if _, err := m.step(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := s.Get(t.Context(), reserved.ID)
	if err != nil || row.Title != binding().NativeID || row.Directory != "/observed" || len(m.pending) != 0 {
		t.Fatalf("metadata did not catch up after binding: %+v pending=%d %v", row, len(m.pending), err)
	}
}
