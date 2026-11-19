package prworkflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

// SessionLink is the public reverse index, without watch or prompt state.
type SessionLink struct {
	Platform  string `json:"platform"`
	SessionID string `json:"sessionId"`
	Number    int    `json:"number"`
}

// LinkedSessions reads durable links without contacting the forge. Remote state
// stays on its owner; legacy hub links are included until their next migration.
func (m *Module) LinkedSessions(ctx context.Context, platform, host, repo string) ([]SessionLink, error) {
	links, err := m.localSessionLinks(ctx, platform, host, repo)
	if err != nil {
		return nil, err
	}
	owner, err := m.remote(Target{Platform: platform})
	if err != nil || owner == nil {
		return links, err
	}
	response, err := owner.PullRequestOperation(ctx, platforms.PullRequestRequest{
		Operation: "linked-sessions", Query: url.Values{"host": {host}, "repo": {repo}},
	})
	if err = responseError(response, err); err != nil {
		return nil, err
	}
	var remote []SessionLink
	if err := json.Unmarshal(response.Body, &remote); err != nil {
		return nil, err
	}
	for _, link := range remote {
		link.Platform = platform
		links = append(links, link)
	}
	seen := make(map[SessionLink]bool)
	result := []SessionLink{}
	for _, link := range links {
		if !seen[link] {
			seen[link] = true
			result = append(result, link)
		}
	}
	return result, nil
}

func (m *Module) localSessionLinks(ctx context.Context, platform, host, repo string) ([]SessionLink, error) {
	result := []SessionLink{}
	if m.d.DB == nil {
		return result, nil
	}
	rows, err := m.d.DB.SQL().QueryContext(ctx, `SELECT session_id, number FROM session_pull_request WHERE platform=? AND host=? AND repo=? ORDER BY number, session_id`, platform, host, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		link := SessionLink{Platform: platform}
		if err := rows.Scan(&link.SessionID, &link.Number); err != nil {
			return nil, err
		}
		result = append(result, link)
	}
	return result, rows.Err()
}

func (m *Module) linkedSessionsOperation(w http.ResponseWriter, r *http.Request, platform string) {
	host, repo := r.URL.Query().Get("host"), r.URL.Query().Get("repo")
	if host == "" || repo == "" {
		http.Error(w, "host and repo are required", http.StatusBadRequest)
		return
	}
	links, err := m.localSessionLinks(r.Context(), platform, host, repo)
	if err != nil {
		http.Error(w, "Could not read linked sessions", http.StatusInternalServerError)
		return
	}
	httpx.WriteJSON(w, links)
}
