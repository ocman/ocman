package sessioncatalog

import (
	"errors"
	"testing"
)

func TestReservePluginOriginRequiresPluginID(t *testing.T) {
	s, _ := openStore(t)
	req := reservation()
	req.Origin, req.OriginID = Plugin, "org.example.background"
	row, created, err := s.Reserve(t.Context(), req)
	if err != nil || !created || row.Origin != Plugin || row.OriginID != req.OriginID {
		t.Fatalf("reserve plugin origin: %+v %v %v", row, created, err)
	}
	req.RequestKey, req.OriginID = "start-2", " "
	if _, _, err := s.Reserve(t.Context(), req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank plugin id accepted: %v", err)
	}
}
