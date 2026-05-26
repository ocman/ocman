package server

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Integration status endpoint
// ---------------------------------------------------------------------------

// handleIntegrationsStatus returns which integrations are available and
// whether they are authenticated.
//
// GET /api/integrations/status
func (s *Server) handleIntegrationsStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"github": map[string]interface{}{
			"available":     true,
			"authenticated": s.integrations.GitHub.Authenticated(),
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
