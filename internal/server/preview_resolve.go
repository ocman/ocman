package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
)

// maxPreviewResolveBody bounds the transcript text one request may scan.
const maxPreviewResolveBody = 1 << 20

// WithPreviewResolvers registers the providers that turn discovered links
// into previews. Must be called before Start.
func (s *Server) WithPreviewResolvers(resolvers ...linkpreview.Resolver) *Server {
	s.previewAuth.resolvers = resolvers
	return s
}

func (s *Server) linkPreviews() *linkpreview.Service {
	s.previewManager()
	return s.previewAuth.previews
}

// previewIdentifierRules are the custom link rules routed to a provider.
func (s *Server) previewIdentifierRules(ctx context.Context) []linkpreview.IdentifierRule {
	value, ok, err := s.stateDB.GetSetting(ctx, linkPreviewRulesKey)
	var saved linkPreviewRules
	if err != nil || !ok || json.Unmarshal([]byte(value), &saved) != nil {
		return nil
	}
	var rules []linkpreview.IdentifierRule
	for _, r := range saved.Rules {
		if r.Provider != "" {
			rules = append(rules, linkpreview.IdentifierRule{Pattern: r.Pattern, Provider: r.Provider})
		}
	}
	return rules
}

// handlePreviewResolve discovers previewable resources in the posted text
// and resolves them for this browser's viewer.
func (s *Server) handlePreviewResolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
		// Workspaces is the viewer's workspace choice per provider for
		// resources several connected workspaces could own. An unconnected
		// choice resolves as not connected.
		Workspaces map[string]string `json:"workspaces"`
	}
	if !readAndUnmarshal(w, r, maxPreviewResolveBody, &req) {
		return
	}
	viewerID, ownerID, ok := s.previewContext(w, r, false)
	if !ok {
		return
	}
	svc := s.linkPreviews()
	refs := svc.Discover(req.Text, s.previewIdentifierRules(r.Context()))
	for i := range refs {
		if refs[i].Workspace == "" {
			refs[i].Workspace = req.Workspaces[refs[i].Provider]
		}
	}
	previews := svc.Resolve(r.Context(), viewerID, ownerID, refs)
	if previews == nil {
		previews = []linkpreview.Preview{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"previews": previews})
}
