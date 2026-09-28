package server

import (
	"context"
	"net/url"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/state"
)

// artifactMCPService adapts the artifact service to the MCP artifacts tool,
// making every item URL absolute via publicURL.
type artifactMCPService struct{ server *Server }

func (s artifactMCPService) CreateArtifact(ctx context.Context, in internalmcp.ArtifactCreateRequest) (state.Artifact, error) {
	req := ArtifactInput{Title: in.Title, Description: in.Description, Directory: in.Directory, Platform: in.Platform, SessionID: in.SessionID}
	for _, f := range in.Files {
		req.Files = append(req.Files, ArtifactFileInput(f))
	}
	for _, l := range in.Links {
		req.Links = append(req.Links, ArtifactLinkInput(l))
	}
	a, err := s.server.CreateArtifact(ctx, req)
	if err != nil {
		return state.Artifact{}, err
	}
	return s.view(a), nil
}

func (s artifactMCPService) ListArtifacts(ctx context.Context, f state.ArtifactFilter) ([]state.Artifact, string, error) {
	list, next, err := s.server.stateDB.ListArtifacts(ctx, f)
	for i := range list {
		list[i] = s.view(list[i])
	}
	return list, next, err
}

func (s artifactMCPService) GetArtifact(ctx context.Context, id string) (state.Artifact, error) {
	a, err := s.server.stateDB.GetArtifact(ctx, id)
	if err != nil {
		return state.Artifact{}, err
	}
	return s.view(a), nil
}

func (s artifactMCPService) ArtifactPageURL(id string) string {
	return s.server.publicURL("/artifacts/" + url.PathEscape(id))
}

func (s artifactMCPService) view(a state.Artifact) state.Artifact {
	a = artifactView(a)
	for i := range a.Items {
		if a.Items[i].Kind == state.ArtifactItemFile {
			a.Items[i].URL = s.server.publicURL(a.Items[i].URL)
		}
	}
	return a
}
