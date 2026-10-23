package sessioncatalog

import (
	"testing"
)

func TestCatalogueBootstrapPreservesConfiguringIdentityUntilNextInventory(t *testing.T) {
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
	if row, err := s.Get(t.Context(), reserved.ID); err != nil || row.CreationState != Configuring {
		t.Fatalf("bootstrap changed configuring identity: %+v %v", row, err)
	}
	if _, err := s.Bind(t.Context(), reserved.ID, binding()); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	row, err := s.Get(t.Context(), reserved.ID)
	if err != nil || row.Title != binding().NativeID || row.Directory != "/observed" {
		t.Fatalf("metadata did not catch up after binding: %+v %v", row, err)
	}
}
