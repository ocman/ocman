package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
)

func TestMigratedPermissionInboxReplayAndReplyKeepOriginalNotification(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "resolved tombstone"}[resolved], func(t *testing.T) {
			s, _, id := identityHTTPFixture(t)
			permission := state.InboxPermission{Platform: "test", SessionID: "native", PermissionID: "prompt", Permission: "bash"}
			if resolved {
				if err := s.stateDB.ResolvePermissionInboxItem(t.Context(), "test", "native", "prompt"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.stateDB.EnsurePermissionInboxItem(t.Context(), permission); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.stateDB.RekeySessionReferences(t.Context(), "test", map[string]string{"native": id}); err != nil {
				t.Fatal(err)
			}
			catalogue, err := sessioncatalog.New(s.stateDB)
			if err != nil {
				t.Fatal(err)
			}
			if err := catalogue.RetainLegacyKeys(t.Context(), "owner", id); err != nil {
				t.Fatal(err)
			}
			native, _ := s.registry.Get("test")
			a, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
			if err != nil {
				t.Fatal(err)
			}
			s.registry.Register(a)
			if err := s.registry.EnableCanonicalSessions(); err != nil {
				t.Fatal(err)
			}
			permission.SessionID = id
			s.notifyPermissionInbox(permission)
			active, err := s.stateDB.ListInboxItems(t.Context())
			want := 1
			if resolved {
				want = 0
			}
			if err != nil || len(active) != want {
				t.Fatalf("replay duplicated/reopened notification: %+v %v", active, err)
			}
			s.resolvePermissionInbox(t.Context(), "test", id, "prompt")
			s.notifyPermissionInbox(permission)
			active, err = s.stateDB.ListInboxItems(t.Context())
			if err != nil || len(active) != 0 {
				t.Fatalf("reply left notification active: %+v %v", active, err)
			}
			archived, err := s.stateDB.ListArchivedInboxItems(t.Context())
			if err != nil || len(archived) != 1 {
				t.Fatalf("reply created a different tombstone: %+v %v", archived, err)
			}
		})
	}
}
