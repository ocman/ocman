package sessionsvc

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestReferenceRecoveryDelegatesProofsWithoutInventingMissingDependencies(t *testing.T) {
	s := &Service{}
	if _, err := s.RecoverSessionReferences(t.Context(), nil, "r-owner:test"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("missing identity boundary accepted: %v", err)
	}
	core := statetest.Open(t, owned.ReferenceSchema)
	references, err := owned.NewReferences(core)
	if err != nil {
		t.Fatal(err)
	}
	s.identities = owned.NewIdentityGateway(nil, "owner", nil, nil, nil)
	if _, err := s.RecoverSessionReferences(t.Context(), nil, "r-owner:test"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("missing reference store accepted: %v", err)
	}
	if count, err := s.RecoverSessionReferences(t.Context(), references, "r-owner:test"); err != nil || count != 0 {
		t.Fatalf("empty proof inventory should not contact an owner: %d %v", count, err)
	}
}
