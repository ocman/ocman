package projectsmod

import (
	"testing"

	"github.com/ocman/ocman/internal/db"
)

func TestFinalCatalog(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		checkouts := []db.ProjectStats{
			{Directory: "/remote/copy", AssociatedDirectory: "/remote/repo", RemoteID: "box", ProjectKey: "shared", SessionCount: 2, Archived: true},
			{Directory: "/local/repo", ProjectKey: "shared", SessionCount: 3, Archived: true},
			{Directory: "/elsewhere", ProjectKey: "other", LastUsed: 100},
		}
		if reversed {
			checkouts[0], checkouts[2] = checkouts[2], checkouts[0]
		}
		catalog := finalCatalog(checkouts)
		if len(catalog.projects) != 2 || catalog.projects[0].Directory != "/elsewhere" || catalog.projects[1].Directory != "/local/repo" || catalog.projects[1].SessionCount != 5 || !catalog.projects[1].Archived {
			t.Fatalf("final projects: %+v", catalog.projects)
		}
		if identity := catalog.resolve(projectCheckout{Directory: "/remote/copy", RemoteID: "box"}); identity.Directory != "/local/repo" || identity.RemoteID != "" {
			t.Fatalf("local representative: %+v", identity)
		}
	}
	associated := finalCatalog([]db.ProjectStats{{Directory: "/copy", AssociatedDirectory: "/repo", ProjectKey: "shared"}})
	if associated.projects[0].Directory != "/repo" || associated.resolve(projectCheckout{Directory: "/repo"}).Key != "shared" {
		t.Fatalf("source association lost: %+v", associated)
	}
	if got := finalCatalog(nil).projects; got == nil || len(got) != 0 {
		t.Fatalf("empty API list: %+v", got)
	}
	tied := finalCatalog([]db.ProjectStats{{Directory: "/b", ProjectKey: "b"}, {Directory: "/a", ProjectKey: "a"}})
	if tied.projects[0].Directory != "/a" {
		t.Fatalf("unstable order for equal activity: %+v", tied.projects)
	}
}
