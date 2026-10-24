package owned

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestRetainedReferencesValidatePhysicalSource(t *testing.T) {
	_, _, catalogue := fixture(t)
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	instance := statev2.Instance{ID: "retained", OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if err := history.RegisterInstance(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	if _, err := HistoryReference(t.Context(), catalogue, nil, instance.ID, "native"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatal(err)
	}
	if _, err := HistoryReference(t.Context(), catalogue, history, instance.ID, "missing"); err == nil {
		t.Fatal("missing retained evidence acquired identity")
	}
	for _, native := range []string{"retained-native"} {
		key := statev2.Key{InstanceID: instance.ID, SessionID: native}
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: key, Directory: "/repo"}}); err != nil {
			t.Fatal(err)
		}
		id, err := HistoryReference(t.Context(), catalogue, history, instance.ID, native)
		if err != nil || !sessioncatalog.IsID(id) {
			t.Fatalf("retained reference: %q %v", id, err)
		}
		row, err := catalogue.Get(t.Context(), id)
		if err != nil || row.Purged {
			t.Fatalf("retained reference was purged: %+v %v", row, err)
		}
		canonical := statev2.Key{InstanceID: instance.ID, SessionID: id}
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: canonical, Directory: "/repo"}}); err != nil {
			t.Fatal(err)
		}
		if got, err := HistoryReference(t.Context(), catalogue, history, instance.ID, id); err != nil || got != id {
			t.Fatalf("canonical retained reference was re-adopted: %q %v", got, err)
		}
		other := instance
		other.ID = "foreign"
		other.OwnerID = "other"
		if err := history.RegisterInstance(t.Context(), other); err != nil {
			t.Fatal(err)
		}
		canonical.InstanceID = other.ID
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: canonical, Directory: "/repo"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := HistoryReference(t.Context(), catalogue, history, other.ID, id); !errors.Is(err, sessioncatalog.ErrBindingConflict) {
			t.Fatalf("canonical reference crossed source: %v", err)
		}
	}
}
