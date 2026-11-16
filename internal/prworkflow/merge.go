package prworkflow

import (
	"context"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/forgehttp"
	"github.com/ocman/ocman/internal/forge/github"
	"github.com/ocman/ocman/internal/httpx"
)

func (m *Module) merge(w http.ResponseWriter, r *http.Request, l Link, expectedHead string) {
	if err := m.checkOwner(r.Context(), l); err != nil {
		http.Error(w, "Session owner unavailable", http.StatusServiceUnavailable)
		return
	}
	f, ok := m.d.ResolveForge(l.Remote)
	if !ok {
		http.Error(w, "Forge unavailable", http.StatusServiceUnavailable)
		return
	}
	merger, ok := f.(forge.PRMerger)
	if !ok {
		http.Error(w, "Forge does not support merging", http.StatusNotImplemented)
		return
	}
	ctx, cancel := context.WithTimeout(forgehttp.WithFreshReads(github.WithRefresh(r.Context())), 30*time.Second)
	defer cancel()
	pr, err := f.LookupPR(ctx, l.Remote.Repo, l.Number)
	if err != nil {
		http.Error(w, "Could not refresh PR before merging", http.StatusBadGateway)
		return
	}
	if expectedHead == "" || expectedHead != pr.HeadSHA || pr.Status != "open" || pr.Mergeable == nil || !*pr.Mergeable {
		http.Error(w, "PR changed or is not mergeable. Refresh and try again.", http.StatusConflict)
		return
	}
	ci, rl, err := f.Checks(ctx, l.Remote.Repo, pr.HeadSHA)
	if err != nil || rl.Limited || rl.Stale {
		http.Error(w, "Could not verify current PR checks", http.StatusBadGateway)
		return
	}
	if ci.State != forge.CIStateSuccess || len(ci.Checks) == 0 || forge.RollUp(ci.Checks) != forge.CIStateSuccess {
		http.Error(w, "PR checks must be green before merging", http.StatusConflict)
		return
	}
	if err := merger.MergePR(ctx, l.Remote.Repo, l.Number, pr.HeadSHA); err != nil {
		http.Error(w, "Forge refused the merge. Check permissions, branch rules and the default merge method on the PR.", http.StatusBadGateway)
		return
	}
	l.PR = pr
	l.PR.Status = "merged"
	l.Watching, l.BackgroundTask, l.Pending, l.LastError = false, false, "", ""
	if err := m.put(ctx, l); err != nil {
		http.Error(w, "PR merged, but its linked status could not be saved. Refresh the linked PRs.", http.StatusInternalServerError)
		return
	}
	httpx.WriteJSON(w, l)
}
