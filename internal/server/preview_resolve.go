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
	_, svc, _ := s.previewBuilt()
	return svc
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
// and resolves them with this machine's credentials.
func (s *Server) handlePreviewResolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if !readAndUnmarshal(w, r, maxPreviewResolveBody, &req) {
		return
	}
	home, ok := s.previewHome(r)
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return
	}
	// Without app access there is no machine viewer: only public forge
	// links resolve.
	ctx, viewerID := r.Context(), ""
	if s.previewAccessAllowed(r) {
		ctx, viewerID = linkpreview.WithOwnerAccess(ctx), machineViewer
	}
	svc := s.linkPreviews()
	refs := svc.Discover(req.Text, s.previewIdentifierRules(r.Context()))
	previews := svc.Resolve(ctx, viewerID, home, refs)
	if previews == nil {
		previews = []linkpreview.Preview{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"previews": previews})
}
