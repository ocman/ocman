package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/NoUseFreak/ocman/internal/linkpreview"
	"github.com/NoUseFreak/ocman/internal/previewauth"
)

type previewProviderView struct {
	previewEntry
	Notice    string `json:"notice,omitempty"`
	TokenHelp string `json:"tokenHelp,omitempty"`
	// Accounts are the saved tokens and grants: display names only.
	Accounts []previewauth.Connection `json:"accounts"`
}

type previewHostKind struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Help string `json:"help"`
}

// previewHostKinds are providers whose hosts are added with a token.
var previewHostKinds = []previewHostKind{
	{Kind: "forgejo", Name: "Forgejo", Help: "Create an access token under Settings → Applications on the host, with read access to repositories and issues."},
	{Kind: "gitlab", Name: "GitLab", Help: "Create a personal access token with the read_api scope under Preferences → Access tokens on the host."},
}

// handlePreviewProviders lists supported providers, which are configured
// and their link hosts, and the custom-rule patterns that route to a
// configured provider, so the browser only asks to resolve text that can
// produce a preview. Tokens never leave the server.
func (s *Server) handlePreviewProviders(w http.ResponseWriter, r *http.Request) {
	home, ok := s.previewContext(w, r)
	if !ok {
		return
	}
	m, _, catalog := s.previewBuilt()
	status, err := m.Status(r.Context(), machineViewer, home)
	if err != nil {
		http.Error(w, "status unavailable", http.StatusInternalServerError)
		return
	}
	byID := map[string]previewauth.ProviderStatus{}
	for _, st := range status {
		byID[st.ID] = st
	}
	views := make([]previewProviderView, 0, len(catalog))
	configured := map[string]bool{}
	for _, e := range catalog {
		st := byID[e.ID]
		configured[e.ID] = e.Configured
		if e.Hosts == nil {
			e.Hosts = []string{}
		}
		accounts := st.Connections
		if accounts == nil {
			accounts = []previewauth.Connection{}
		}
		views = append(views, previewProviderView{previewEntry: e, Notice: st.Notice, TokenHelp: st.TokenHelp, Accounts: accounts})
	}
	rules := []string{}
	for _, rule := range s.previewIdentifierRules(r.Context()) {
		if configured[rule.Provider] {
			rules = append(rules, rule.Pattern)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"providers": views, "rules": rules, "hostKinds": previewHostKinds})
}

// tokenProvider returns the provider a token is saved for: a known one, or
// a new Forgejo/GitLab host ("forgejo:<host>").
func (s *Server) tokenProvider(id string) (previewauth.Provider, bool) {
	if p, ok := s.previewManager().Provider(id); ok {
		return p, true
	}
	kind, host, _ := strings.Cut(id, ":")
	if u, err := url.Parse("https://" + host); host == "" || err != nil || u.Host != host || u.Hostname() == "" || strings.ToLower(host) != host {
		return previewauth.Provider{}, false
	}
	switch kind {
	case "forgejo":
		return linkpreview.ForgejoOAuth(host, "", ""), true
	case "gitlab":
		return linkpreview.GitLabOAuth(host, "", "", "", ""), true
	}
	return previewauth.Provider{}, false
}

// handlePreviewToken checks a pasted personal token with the provider and
// saves it for this machine. The token is sealed at rest and never returned.
func (s *Server) handlePreviewToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Provider string `json:"provider"`
		Token    string `json:"token"`
	}
	if !readAndUnmarshal(w, r, maxPreviewAuthBody, &req) {
		return
	}
	home, ok := s.previewContext(w, r)
	if !ok {
		return
	}
	p, ok := s.tokenProvider(strings.ToLower(strings.TrimSpace(req.Provider)))
	if !ok {
		http.Error(w, "unknown provider", http.StatusNotFound)
		return
	}
	err := s.previewManager().ConnectToken(r.Context(), machineViewer, home, p, req.Token)
	switch {
	case errors.Is(err, previewauth.ErrNoTokenLogin):
		http.Error(w, "this provider does not accept personal tokens", http.StatusBadRequest)
	case errors.Is(err, previewauth.ErrExchange):
		http.Error(w, "the provider rejected this token", http.StatusBadRequest)
	case err != nil:
		http.Error(w, "saving the token failed", http.StatusInternalServerError)
	default:
		s.resetPreviews()
		w.WriteHeader(http.StatusNoContent)
	}
}
