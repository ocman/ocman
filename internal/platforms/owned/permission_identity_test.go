package owned

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func TestPermissionKeysPreserveLegacyDeduplicationWithoutExposingNativeSessionID(t *testing.T) {
	a, native, store := fixture(t)
	publish(t, a, native.rows...)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := rows[0].ID
	modern, err := a.PermissionInboxKey(t.Context(), "test", id, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	wantModern := "permission-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("test\x00"+id+"\x00prompt")).String()
	if modern != wantModern {
		t.Fatalf("new identity used native correlation: %s", modern)
	}
	if err := store.RetainLegacyKeys(t.Context(), "owner", id); err != nil {
		t.Fatal(err)
	}
	legacy, err := a.PermissionInboxKey(t.Context(), "test", id, "prompt")
	wantLegacy := "permission-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("test\x00native\x00prompt")).String()
	if err != nil || legacy != wantLegacy || legacy == modern {
		t.Fatalf("legacy correlation changed: %s %v", legacy, err)
	}
	remote, err := a.PermissionInboxKey(t.Context(), "r-owner:test", id, "prompt")
	wantRemote := "permission-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("r-owner:test\x00native\x00prompt")).String()
	if err != nil || remote != wantRemote {
		t.Fatalf("owner-qualified key: %s %v", remote, err)
	}
	if _, err := a.PermissionInboxKey(t.Context(), "r-stranger:test", id, "prompt"); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("unrelated owner scope accepted: %v", err)
	}
	if _, err := a.PermissionInboxKey(t.Context(), "test", "native", "prompt"); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("native execution key accepted by public integration: %v", err)
	}
	if err := store.Scrub(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}, "native"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.PermissionInboxKey(t.Context(), "test", id, "prompt"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("purged identity regained notification authority: %v", err)
	}
}
