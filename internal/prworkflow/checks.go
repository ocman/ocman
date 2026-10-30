package prworkflow

import (
	"context"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/github"
	"github.com/ocman/ocman/internal/httpx"
)

// checks uses the saved repository identity, not the session checkout remote.
func (m *Module) checks(w http.ResponseWriter, r *http.Request) {
	t, ok := m.d.Target(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	q := r.URL.Query()
	number, err := strconv.Atoi(q.Get("number"))
	sha := q.Get("sha")
	_, shaErr := hex.DecodeString(sha)
	if err != nil || number <= 0 || sha == "" || len(sha) > 64 || shaErr != nil {
		http.Error(w, "A positive PR number and hexadecimal commit ID are required", http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	links, err := m.links(r.Context(), t.Platform, t.SessionID)
	m.mu.Unlock()
	if err != nil {
		http.Error(w, "Could not load linked PRs", http.StatusInternalServerError)
		return
	}
	for _, l := range links {
		if l.Number != number || l.Remote.Host != q.Get("host") || l.Remote.Repo != q.Get("repo") {
			continue
		}
		if m.checkOwner(r.Context(), l) != nil {
			http.Error(w, "Session owner unavailable", http.StatusServiceUnavailable)
			return
		}
		f, ok := m.d.ResolveForge(l.Remote)
		if !ok {
			http.Error(w, "Forge unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx := github.WithCache(r.Context())
		if q.Get("refresh") == "true" {
			ctx = github.WithRefresh(ctx)
		}
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		ci, rl, err := f.Checks(ctx, l.Remote.Repo, sha)
		if err != nil {
			http.Error(w, "Could not read PR checks", http.StatusBadGateway)
			return
		}
		if ci.Checks == nil {
			ci.Checks = []forge.Check{}
		}
		httpx.WriteJSON(w, map[string]any{"state": ci.State, "checks": ci.Checks, "rateLimit": rl})
		return
	}
	http.Error(w, "Linked PR not found", http.StatusNotFound)
}
