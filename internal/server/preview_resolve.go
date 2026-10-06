package server

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
)

// maxPreviewResolveBody bounds the transcript text one request may scan.
const maxPreviewResolveBody = 1 << 20

var previewChecksSHA = regexp.MustCompile(`^[0-9a-fA-F]{5,40}$`)

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
		Text          string `json:"text"`
		ChecksSHA     string `json:"checksSha"`
		RefreshChecks bool   `json:"refreshChecks"`
		Refresh       bool   `json:"refresh"`
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
	if req.ChecksSHA != "" {
		if !previewChecksSHA.MatchString(req.ChecksSHA) || len(refs) != 1 || refs[0].Kind != "pr" ||
			(refs[0].Provider != "github" && !strings.HasPrefix(refs[0].Provider, "forgejo:")) {
			http.Error(w, "invalid checks target", http.StatusBadRequest)
			return
		}
		refs[0].Kind = "checks"
		repo, _, _ := strings.Cut(refs[0].ID, "#")
		refs[0].ID = repo + "@" + req.ChecksSHA
		if req.RefreshChecks {
			ctx = linkpreview.WithPreviewRefresh(ctx)
		}
	}
	if req.Refresh {
		ctx = linkpreview.WithPreviewRefresh(ctx)
	}
	previews := svc.Resolve(ctx, viewerID, home, refs)
	if previews == nil {
		previews = []linkpreview.Preview{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"previews": previews})
}
