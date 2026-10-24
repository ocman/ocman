package pluginsmod

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/google/uuid"
)

func TestPluginSessionMigrationPreservesReceiptsOutboxAndUnknownOwners(t *testing.T) {
	path := statetest.Path(t, Schema, sessioncatalog.Schema, owned.ReferenceSchema)
	core, err := state.Open(path, Schema, sessioncatalog.Schema, owned.ReferenceSchema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	d := NewStore(core)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if _, err := catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native"}}); err != nil {
		t.Fatal(err)
	}
	bound, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "source", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"test", "r-other:test"} {
		if err := d.LinkPluginSession(t.Context(), scope, "native", "plugin"); err != nil {
			t.Fatal(err)
		}
	}
	params := plugins.SessionRunParams{Prompt: "original prompt"}
	for _, operation := range []string{"settled", "uncertain"} {
		if _, claimed, err := d.reserveSessionRun(t.Context(), "plugin", operation, "/repo", params); err != nil || !claimed {
			t.Fatalf("reserve: %v %v", claimed, err)
		}
	}
	if err := d.finishSessionRun(t.Context(), "plugin", "settled", "native", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`UPDATE plugin_session_run SET session_id='native' WHERE operation_id='uncertain'`); err != nil {
		t.Fatal(err)
	}
	delivery, err := d.AppendPluginSessionCompletion(t.Context(), "plugin", "test", "native", "native:message", "owed answer")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AckPluginSessionCompletion(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	references, err := owned.NewReferences(core)
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(ctx context.Context, scope, ref string) (string, error) {
		return references.PrepareReference(ctx, catalogue, "owner", scope, ref)
	}
	if err := d.MigrateSessionReferences(t.Context(), core, resolve); err != nil {
		t.Fatal(err)
	}
	if plugin, linked, err := d.GetPluginSessionPlugin(t.Context(), "test", bound.ID); err != nil || !linked || plugin != "plugin" {
		t.Fatalf("mapping: %q %v %v", plugin, linked, err)
	}
	sessions, err := d.ListPluginSessionSessions(t.Context())
	if err != nil || len(sessions) != 2 {
		t.Fatalf("mapping count: %+v %v", sessions, err)
	}
	for _, session := range sessions {
		if session.PlatformID == "r-other:test" && !identity.IsPendingReference(session.SessionID) {
			t.Fatal("plugin migration minted remote identity")
		}
	}
	// Two owner scopes named the same native ID. The old run receipt cannot
	// be attributed to either, even though the local mapping is now canonical.
	id, claimed, err := d.reserveSessionRun(t.Context(), "plugin", "settled", "/repo", params)
	if err != nil || claimed || !identity.IsPendingReference(id) {
		t.Fatalf("ambiguous run was replayed or attributed: %q %v %v", id, claimed, err)
	}
	if _, claimed, err := d.reserveSessionRun(t.Context(), "plugin", "uncertain", "/repo", params); claimed || !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("uncertain work gained replay authority: %v %v", claimed, err)
	}
	if _, claimed, err := d.reserveSessionRun(t.Context(), "plugin", "settled", "/repo", plugins.SessionRunParams{Prompt: "different"}); claimed || plugins.RequestError(err).Category != plugins.ErrorConflict {
		t.Fatalf("migration changed fingerprint: %v %v", claimed, err)
	}
	var storedID int64
	var session, operation, status, text string
	if err := d.db.QueryRow(`SELECT id,session_id,operation_id,status,text FROM plugin_session_outbox`).Scan(&storedID, &session, &operation, &status, &text); err != nil {
		t.Fatal(err)
	}
	if storedID != delivery || session != bound.ID || operation != "native:message" || status != "done" || text != "owed answer" {
		t.Fatalf("outbox correlation changed: %d %q %q %q %q", storedID, session, operation, status, text)
	}
	if err := d.MigrateSessionReferences(t.Context(), core, resolve); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(path + ".backup-feature-session-keys-*.db")
	if err != nil || len(backups) != 1 {
		t.Fatalf("migration backup/replay: %+v %v", backups, err)
	}
	row, err := catalogue.Get(t.Context(), bound.ID)
	if err != nil || !row.LegacyKeys {
		t.Fatalf("legacy wire compatibility not retained: %+v %v", row, err)
	}
}

func TestPluginSessionMigrationRollsBackConflictingClaims(t *testing.T) {
	core := statetest.Open(t, Schema)
	d := NewStore(core)
	for _, id := range []string{"one", "two"} {
		if err := d.LinkPluginSession(t.Context(), "test", id, id); err != nil {
			t.Fatal(err)
		}
	}
	id := "ocm_" + uuid.NewString()
	if err := d.MigrateSessionReferences(t.Context(), core, func(context.Context, string, string) (string, error) { return id, nil }); err == nil {
		t.Fatal("colliding claims merged")
	}
	for _, id := range []string{"one", "two"} {
		if plugin, linked, err := d.GetPluginSessionPlugin(t.Context(), "test", id); err != nil || !linked || plugin != id {
			t.Fatalf("rollback lost claim: %q %v %v", plugin, linked, err)
		}
	}
	if err := d.MigrateSessionReferences(t.Context(), core, func(context.Context, string, string) (string, error) { return "native", nil }); !errors.Is(err, state.ErrPluginInvalid) {
		t.Fatalf("native migration target accepted: %v", err)
	}
}

var _ platforms.PluginSessionCompatibility = (*legacyPluginPlatform)(nil)

func TestPluginSessionMigrationRetainsUniqueRunResultAndFingerprint(t *testing.T) {
	core := statetest.Open(t, Schema)
	d := NewStore(core)
	if err := d.LinkPluginSession(t.Context(), "test", "native", "plugin"); err != nil {
		t.Fatal(err)
	}
	params := plugins.SessionRunParams{Prompt: "once", Agent: "build", PermissionProfile: "safe"}
	if _, claimed, err := d.reserveSessionRun(t.Context(), "plugin", "operation", "/repo", params); err != nil || !claimed {
		t.Fatalf("reserve: %v %v", claimed, err)
	}
	if err := d.finishSessionRun(t.Context(), "plugin", "operation", "native", nil); err != nil {
		t.Fatal(err)
	}
	canonical := "ocm_" + uuid.NewString()
	if err := d.MigrateSessionReferences(t.Context(), core, func(_ context.Context, scope, ref string) (string, error) {
		if scope != "test" || ref != "native" {
			t.Fatalf("run source inferred incorrectly: %q %q", scope, ref)
		}
		return canonical, nil
	}); err != nil {
		t.Fatal(err)
	}
	id, claimed, err := d.reserveSessionRun(t.Context(), "plugin", "operation", "/repo", params)
	if err != nil || claimed || id != canonical {
		t.Fatalf("run receipt changed or replayed: %q %v %v", id, claimed, err)
	}
}
