package server

import (
	"errors"
	"strings"
	"testing"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestArtifactMCPServiceRelativeFileURLs(t *testing.T) {
	srv, _ := artifactServer(t)
	srv.publicBaseURL = "https://ocman.example/"
	svc := artifactMCPService{srv}
	a, err := svc.CreateArtifact(t.Context(), internalmcp.ArtifactCreateRequest{
		Title: "Report", Directory: "/repo/sub", Platform: "opencode", SessionID: "top",
		Files: []internalmcp.ArtifactFile{{Name: "a.md", Content: "# hi"}},
		Links: []internalmcp.ArtifactLink{{URL: "https://example.com", Label: "ex"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/api/artifacts/" + a.ID + "/files/0"; a.Items[0].URL != want {
		t.Fatalf("file url = %q, want %q", a.Items[0].URL, want)
	}
	if a.Items[1].URL != "https://example.com" || svc.ArtifactPageURL(a.ID) != "https://ocman.example/artifacts/"+a.ID {
		t.Fatalf("items = %#v", a.Items)
	}
	got, err := svc.GetArtifact(t.Context(), a.ID)
	if err != nil || !strings.HasPrefix(got.Items[0].URL, "/api/artifacts/") {
		t.Fatalf("get = %#v, %v", got, err)
	}
	list, _, err := svc.ListArtifacts(t.Context(), state.ArtifactFilter{Platform: "opencode", SessionIDs: []string{"top"}})
	if err != nil || len(list) != 1 || !strings.HasPrefix(list[0].Items[0].URL, "/api/artifacts/") {
		t.Fatalf("list = %#v, %v", list, err)
	}
	if _, err := svc.CreateArtifact(t.Context(), internalmcp.ArtifactCreateRequest{Title: "x", Directory: "/repo"}); !errors.Is(err, state.ErrArtifactInvalid) {
		t.Fatalf("empty artifact err = %v", err)
	}
	if _, err := svc.GetArtifact(t.Context(), "missing"); !errors.Is(err, state.ErrArtifactNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}
