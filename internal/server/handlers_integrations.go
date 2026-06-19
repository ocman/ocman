package server

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Integration status endpoint
// ---------------------------------------------------------------------------

// handleIntegrationsStatus returns which integrations are available and
// whether they are authenticated. For Forgejo it also reports the list of
// configured hosts so the frontend knows which hostnames to scan for
// previewable links (GitHub's host is fixed; Forgejo's are dynamic).
//
// GET /api/integrations/status
func (s *Server) handleIntegrationsStatus(w http.ResponseWriter, r *http.Request) {
	var forgejoHosts []string
	if s.integrations != nil && s.integrations.Forgejo != nil {
		forgejoHosts = s.integrations.Forgejo.Hosts()
	}
	if forgejoHosts == nil {
		forgejoHosts = []string{}
	}
	writeJSON(w, map[string]interface{}{
		"github": map[string]interface{}{
			"available":     true,
			"authenticated": s.integrations.GitHub.Authenticated(),
		},
		"forgejo": map[string]interface{}{
			"available": len(forgejoHosts) > 0,
			"hosts":     forgejoHosts,
		},
	})
}

// ---------------------------------------------------------------------------
// GitHub preview endpoint
// ---------------------------------------------------------------------------

// Patterns for recognising GitHub URL types.
var (
	ghPRRE     = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/pull/(\d+)`)
	ghIssueRE  = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/issues/(\d+)`)
	ghCommitRE = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+)/commit/([0-9a-f]{5,40})`)
)

// handleGitHubPreview proxies a GitHub API request for PR / issue / commit
// metadata, injecting the server-side token (if any).
//
// GET /api/integrations/github/preview?url=<github-url>
func (s *Server) handleGitHubPreview(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}
	// Strip trailing punctuation that might have leaked from markdown.
	rawURL = strings.TrimRight(rawURL, ".,;:!?")

	gh := s.integrations.GitHub

	if m := ghPRRE.FindStringSubmatch(rawURL); m != nil {
		owner, repo := m[1], m[2]
		number, _ := strconv.Atoi(m[3])
		data, err := gh.GetPR(owner, repo, number)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, data)
		return
	}

	if m := ghIssueRE.FindStringSubmatch(rawURL); m != nil {
		owner, repo := m[1], m[2]
		number, _ := strconv.Atoi(m[3])
		data, err := gh.GetIssue(owner, repo, number)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, data)
		return
	}

	if m := ghCommitRE.FindStringSubmatch(rawURL); m != nil {
		owner, repo, sha := m[1], m[2], m[3]
		data, err := gh.GetCommit(owner, repo, sha)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, data)
		return
	}

	http.Error(w, "url does not match a supported GitHub resource", http.StatusUnprocessableEntity)
}

// ---------------------------------------------------------------------------
// Forgejo preview endpoint
// ---------------------------------------------------------------------------

// Forgejo / Gitea web-UI path patterns. The host is dynamic (self-hosted or
// codeberg.org etc.), so it is validated against the configured registry
// rather than matched here. The leading path is owner/repo, then a kind
// segment and a number / sha.
var (
	fjPRPathRE     = regexp.MustCompile(`^/([^/]+)/([^/]+)/pulls/(\d+)`)
	fjIssuePathRE  = regexp.MustCompile(`^/([^/]+)/([^/]+)/issues/(\d+)`)
	fjCommitPathRE = regexp.MustCompile(`^/([^/]+)/([^/]+)/commit/([0-9a-f]{5,40})`)
)

// handleForgejoPreview proxies a Forgejo/Gitea API request for PR / issue /
// commit metadata, injecting the per-host token (if any). The URL's host must
// be a configured Forgejo host; otherwise the request is rejected so this
// endpoint can't be used as an open proxy.
//
// GET /api/integrations/forgejo/preview?url=<forgejo-url>
func (s *Server) handleForgejoPreview(w http.ResponseWriter, r *http.Request) {
	rawURL := r.URL.Query().Get("url")
	if rawURL == "" {
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}
	// Strip trailing punctuation that might have leaked from markdown.
	rawURL = strings.TrimRight(rawURL, ".,;:!?")

	if s.integrations == nil || s.integrations.Forgejo == nil {
		http.Error(w, "forgejo integration not configured", http.StatusServiceUnavailable)
		return
	}

	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}
	client := s.integrations.Forgejo.ForHost(u.Hostname())
	if client == nil {
		http.Error(w, "url host is not a configured Forgejo host", http.StatusUnprocessableEntity)
		return
	}

	ctx := r.Context()

	if m := fjPRPathRE.FindStringSubmatch(u.Path); m != nil {
		owner, repo := m[1], m[2]
		number, _ := strconv.Atoi(m[3])
		data, err := client.GetPR(ctx, owner, repo, number)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, data)
		return
	}

	if m := fjIssuePathRE.FindStringSubmatch(u.Path); m != nil {
		owner, repo := m[1], m[2]
		number, _ := strconv.Atoi(m[3])
		data, err := client.GetIssue(ctx, owner, repo, number)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, data)
		return
	}

	if m := fjCommitPathRE.FindStringSubmatch(u.Path); m != nil {
		owner, repo, sha := m[1], m[2], m[3]
		data, err := client.GetCommit(ctx, owner, repo, sha)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, data)
		return
	}

	http.Error(w, "url does not match a supported Forgejo resource", http.StatusUnprocessableEntity)
}
