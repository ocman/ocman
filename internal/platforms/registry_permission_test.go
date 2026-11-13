package platforms_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

type canonicalPermissionPlatform struct{ *platformtest.Fake }

func (*canonicalPermissionPlatform) CanonicalSessionIDs() bool { return true }

func TestPermissionPlatformUsesCanonicalOwnerForKindOnly(t *testing.T) {
	id := "ocm_" + uuid.NewString()
	for _, canonical := range []bool{false, true} {
		r := platforms.NewRegistry()
		fake := &platformtest.Fake{PlatformID: "opencode", SessionList: []db.Session{{ID: id}}}
		var p platforms.Platform = fake
		if canonical {
			p = &canonicalPermissionPlatform{fake}
		}
		if err := r.RegisterConnection("cnx_account", p); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			scope   platforms.ID
			session string
			want    bool
		}{
			{"cnx_account", id, true},
			{"opencode", id, canonical},
			{"cnx_missing", id, false},
			{"r-offline:opencode", id, false},
			{"claudecode", id, false},
			{"opencode", "native", false},
			{"opencode", "ocm_" + uuid.NewString(), false},
		} {
			got, key, ok := r.PermissionPlatform(t.Context(), tc.scope, tc.session)
			if ok != tc.want || (ok && (got != p || key != "cnx_account")) {
				t.Errorf("canonical=%t scope=%s session=%s: %v %t", canonical, tc.scope, tc.session, got, ok)
			}
		}
	}
}
