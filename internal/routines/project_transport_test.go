package routines

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
)

func TestRepositoryTransportURLsDoNotUseCanonicalKeyLookup(t *testing.T) {
	for _, project := range []string{
		"git://forge.test/org/one.git",
		"https://forge.test/org/one.git",
		"ssh://git@forge.test/org/one.git",
		"git@forge.test:org/one.git",
	} {
		t.Run(project, func(t *testing.T) {
			h, _ := projectHarness(t)
			h.svc.normalizeUpstream = remote.NormalizeUpstream
			h.svc.projectCatalog = func(context.Context) ([]db.ProjectStats, error) {
				t.Fatal("repository transport URL reached canonical-key lookup")
				return nil, nil
			}
			got, err := h.svc.launchProject(t.Context(), state.Routine{RemoteID: "local"}, project)
			if err != nil || got != "/repo" {
				t.Fatalf("launch = %q, %v", got, err)
			}
		})
	}
}
