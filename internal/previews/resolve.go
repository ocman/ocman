package previews

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/linkpreview"
)

// maxPreviewResolveBody bounds the transcript text one request may scan.
const maxPreviewResolveBody = 1 << 20

var previewChecksSHA = regexp.MustCompile(`^[0-9a-fA-F]{5,40}$`)

type previewRequest struct {
	Text          string `json:"text"`
	ChecksSHA     string `json:"checksSha"`
	RefreshChecks bool   `json:"refreshChecks"`
	Refresh       bool   `json:"refresh"`
	PRStatus      bool   `json:"prStatus"`
	OpenGraph     bool   `json:"openGraph"`
	Render        bool   `json:"render"`
}

type previewResponse struct {
	Previews []linkpreview.Preview `json:"previews"`
	Error    string                `json:"error,omitempty"`
}

func (m *Module) linkPreviews() *linkpreview.Service {
	_, svc, _ := m.previewBuilt()
	return svc
}

// previewIdentifierRules are the custom link rules routed to a provider.
func (m *Module) previewIdentifierRules(ctx context.Context) []linkpreview.IdentifierRule {
	value, ok, err := m.Store.GetSetting(ctx, linkPreviewRulesKey)
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
func (m *Module) handlePreviewResolve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		previewRequest
		Requests []previewRequest `json:"requests"`
	}
	if !httpx.ReadJSON(w, r, maxPreviewResolveBody, &req) {
		return
	}
	if req.Requests != nil && (len(req.Requests) == 0 || len(req.Requests) > 32) {
		http.Error(w, "invalid preview batch size", http.StatusBadRequest)
		return
	}
	home, ok := m.previewHome(r)
	if !ok {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return
	}
	// Without app access there is no machine viewer: only public forge
	// links resolve.
	ctx, viewerID := r.Context(), ""
	access := m.previewAccessAllowed(r)
	if access {
		ctx, viewerID = linkpreview.WithOwnerAccess(ctx), machineViewer
	}
	w.Header().Set("Cache-Control", "no-store")
	if req.Requests != nil {
		results := make([]previewResponse, len(req.Requests))
		var wg sync.WaitGroup
		for i, one := range req.Requests {
			wg.Go(func() { results[i] = m.resolvePreview(ctx, viewerID, home, one, access) })
		}
		wg.Wait()
		httpx.WriteJSON(w, map[string]any{"results": results})
		return
	}
	result := m.resolvePreview(ctx, viewerID, home, req.previewRequest, access)
	if result.Error != "" {
		http.Error(w, result.Error, http.StatusBadRequest)
		return
	}
	httpx.WriteJSON(w, result)
}

func (m *Module) resolvePreview(ctx context.Context, viewerID, home string, req previewRequest, access bool) previewResponse {
	svc := m.linkPreviews()
	refs := svc.Discover(req.Text, m.previewIdentifierRules(ctx))
	if req.PRStatus {
		if req.ChecksSHA != "" || len(refs) != 1 || refs[0].Kind != "pr" ||
			(refs[0].Provider != "github" && !strings.HasPrefix(refs[0].Provider, "forgejo:")) {
			return previewResponse{Error: "invalid PR status target"}
		}
		refs[0].Kind = "pr-status"
	}
	if req.ChecksSHA != "" {
		if !previewChecksSHA.MatchString(req.ChecksSHA) || len(refs) != 1 || refs[0].Kind != "pr" ||
			(refs[0].Provider != "github" && !strings.HasPrefix(refs[0].Provider, "forgejo:")) {
			return previewResponse{Error: "invalid checks target"}
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
	if req.Render {
		ctx = linkpreview.WithPRRenderRefresh(ctx)
	}
	previews := svc.Resolve(ctx, viewerID, home, refs)
	if req.OpenGraph && !req.PRStatus && req.ChecksSHA == "" && access {
		rich := false
		for _, p := range previews {
			rich = rich || (p.Title != "" && (p.State == linkpreview.StateOK || p.Stale))
		}
		if !rich {
			if p := svc.ResolveOpenGraph(ctx, viewerID, home, req.Text); p.State == linkpreview.StateOK && p.Title != "" {
				previews = []linkpreview.Preview{p}
			}
		}
	}
	if previews == nil {
		previews = []linkpreview.Preview{}
	}
	return previewResponse{Previews: previews}
}
