package sessioncatalog

import (
	"errors"
	"testing"
)

func TestReservePreservesDraftIdentity(t *testing.T) {
	s, _ := openStore(t)
	request := reservation()
	request.SessionID = "ocm_01234567-89ab-4cde-8fab-0123456789ab"
	row, created, err := s.Reserve(t.Context(), request)
	if err != nil || !created || row.ID != request.SessionID {
		t.Fatalf("draft identity changed: %+v %v %v", row, created, err)
	}
	if replay, created, err := s.Reserve(t.Context(), request); err != nil || created || replay.ID != row.ID {
		t.Fatalf("draft replay changed identity: %+v %v %v", replay, created, err)
	}
	request.RequestKey = "another-request"
	request.OwnerID = "another-owner"
	if _, _, err := s.Reserve(t.Context(), request); err == nil {
		t.Fatal("another reservation stole an existing draft identity")
	}
	stored, err := s.Get(t.Context(), row.ID)
	if err != nil || stored.OwnerID != row.OwnerID {
		t.Fatal("identity collision overwrote the owner", stored, err)
	}
}

func TestReservationRejectsNativeOrMalformedDraftIDs(t *testing.T) {
	s, _ := openStore(t)
	for _, id := range []string{"native-session", "ocm_short", "ocm_------------------------------------"} {
		request := reservation()
		request.SessionID = id
		if _, _, err := s.Reserve(t.Context(), request); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q: %v", id, err)
		}
	}
}
