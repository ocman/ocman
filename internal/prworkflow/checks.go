package prworkflow

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/github"
	"github.com/ocman/ocman/internal/httpx"
)

// checks uses the saved repository identity, not the session checkout remote.
// Without sha it reads the PR's current head commit from the forge.
func (m *Module) checks(w http.ResponseWriter, r *http.Request) {
	t, remote := remoteSidebarTarget(r)
	if !remote {
		var ok bool
		t, ok = m.d.Target(w, r, r.PathValue("id"))
		if !ok || m.forward(w, r, t, "checks") {
			return
		}
	}
	q := r.URL.Query()
	number, err := strconv.Atoi(q.Get("number"))
	sha := q.Get("sha")
	_, shaErr := hex.DecodeString(sha)
	if err != nil || number <= 0 || len(sha) > 64 || shaErr != nil {
		http.Error(w, "A positive PR number and an optional hexadecimal commit ID are required", http.StatusBadRequest)
		return
	}
	var links []Link
	if remote {
		var ok bool
		links, ok = m.remoteSidebarLinks(w, t)
		if !ok {
			return
		}
	} else {
		m.mu.Lock()
		links, err = m.links(r.Context(), t.Platform, t.SessionID)
		m.mu.Unlock()
		if err != nil {
			http.Error(w, "Could not load linked PRs", http.StatusInternalServerError)
			return
		}
	}
	for _, l := range links {
		if l.Number != number || l.Remote.Host != q.Get("host") || l.Remote.Repo != q.Get("repo") {
			continue
		}
		if !remote && m.checkOwner(r.Context(), l) != nil {
			http.Error(w, "Session owner unavailable", http.StatusServiceUnavailable)
			return
		}
		f, ok := m.d.ResolveForge(l.Remote)
		if !ok {
			http.Error(w, "Forge unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx := github.WithCache(r.Context())
		if refreshRequested(q) {
			ctx = github.WithRefresh(ctx)
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if sha == "" {
			pr, err := f.LookupPR(ctx, l.Remote.Repo, number)
			if err != nil || pr.HeadSHA == "" {
				http.Error(w, "Could not read the PR head commit", http.StatusBadGateway)
				return
			}
			sha = pr.HeadSHA
		}
		ci, rl, err := f.Checks(ctx, l.Remote.Repo, sha)
		if err != nil {
			http.Error(w, "Could not read PR checks", http.StatusBadGateway)
			return
		}
		if ci.Checks == nil {
			ci.Checks = []forge.Check{}
		}
		httpx.WriteJSON(w, map[string]any{"sha": sha, "state": ci.State, "checks": ci.Checks, "rateLimit": rl})
		return
	}
	http.Error(w, "Linked PR not found", http.StatusNotFound)
}

// refreshRequested accepts the dashboard's "true" and the MCP tool's "1".
func refreshRequested(q url.Values) bool {
	return q.Get("refresh") == "true" || q.Get("refresh") == "1"
}
