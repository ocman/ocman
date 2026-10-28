package owned

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/sessioncatalog"
)

func TestBatchedStoredIdentitiesPreserveScopeAndPublication(t *testing.T) {
	for _, mode := range []string{"ready", "owner", "platform", "offline", "purged", "creating", "missing", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			a, _, id := storedFixture(t)
			switch mode {
			case "owner":
				a.owner = "another-owner"
			case "platform":
				a.source.Platform = "another-platform"
			case "offline":
				a.unavailable = errors.New("disconnected")
			case "purged":
				row, err := a.store.Get(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.store.Scrub(t.Context(), sessioncatalog.Source{OwnerID: row.OwnerID, Platform: row.Platform, SourceKey: row.SourceKey}, row.NativeID); err != nil {
					t.Fatal(err)
				}
			case "creating":
				row, _, err := a.store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: a.owner, Platform: a.source.Platform, Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "new", PayloadHash: strings.Repeat("0", 64)})
				if err != nil {
					t.Fatal(err)
				}
				id = row.ID
			case "missing":
				id = "ocm_00000000-0000-0000-0000-000000000000"
			case "invalid":
				id = "native"
			}
			single, singleErr := a.storedIdentity(t.Context(), id)
			batch, batchErr := a.storedIdentities(t.Context(), []string{id})
			if singleErr == nil {
				if batchErr != nil || !reflect.DeepEqual(batch[id], single) {
					t.Fatalf("batch changed identity: %+v %v", batch, batchErr)
				}
			} else if mode == "platform" || mode == "invalid" {
				if !errors.Is(batchErr, singleErr) {
					t.Fatalf("scope error changed: single=%v batch=%v", singleErr, batchErr)
				}
			} else if batchErr != nil || len(batch) != 0 {
				t.Fatalf("hidden identity leaked: %+v %v", batch, batchErr)
			}
		})
	}
}
