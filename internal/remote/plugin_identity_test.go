package remote

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/google/uuid"
)

func TestRestoredRemotePluginCorrelationsUseOnlyTheirScopedProof(t *testing.T) {
	core, err := state.Open(statetest.Path(t, owned.ReferenceSchema), owned.ReferenceSchema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	p := canonicalRemotePlatform().withPluginReferences(core)
	id := "ocm_" + uuid.NewString()
	if got, err := p.PluginSessionReference(t.Context(), id); err != nil || got != id {
		t.Fatalf("new session changed wire correlation: %q %v", got, err)
	}
	if _, err := core.SQL().Exec(`INSERT INTO platform_session_reference(ref_id,scope,native_id,canonical_id,created_at) VALUES(?,?,?,?,1)`, identity.PendingReference(), string(p.ID()), "legacy-native", id); err != nil {
		t.Fatal(err)
	}
	if got, err := p.PluginSessionReference(t.Context(), id); err != nil || got != "legacy-native" {
		t.Fatalf("restored correlation lost: %q %v", got, err)
	}
	other := newRemotePlatform(&RemoteConn{protocolVersion: canonicalSessionProtocol, remoteID: "other-owner"}, "opencode", nil).withPluginReferences(core)
	if got, err := other.PluginSessionReference(t.Context(), id); err != nil || got != id {
		t.Fatalf("another owner inherited private compatibility reference: %q %v", got, err)
	}
	if _, err := p.PluginSessionReference(t.Context(), "legacy-native"); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("compatibility accepted native caller ID: %v", err)
	}
	if _, err := canonicalRemotePlatform().PluginSessionReference(t.Context(), id); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("missing proof store invented a correlation: %v", err)
	}
	if _, err := core.SQL().Exec(`INSERT INTO platform_session_reference(ref_id,scope,native_id,canonical_id,created_at) VALUES(?,?,?,?,1)`, identity.PendingReference(), string(p.ID()), "conflicting-native", id); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PluginSessionReference(t.Context(), id); !errors.Is(err, sessioncatalog.ErrBindingConflict) {
		t.Fatalf("ambiguous correlation guessed: %v", err)
	}
}
