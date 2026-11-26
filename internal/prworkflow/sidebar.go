package prworkflow

import (
	"context"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/github"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

func remoteSidebarTarget(r *http.Request) (Target, bool) {
	q := r.URL.Query()
	platform := q.Get("platform")
	owner, _ := platforms.SplitPlatformID(platform)
	return Target{Platform: platform, RemoteID: owner, SessionID: r.PathValue("id")}, owner != "" && q.Get("sidebar") == "1"
}

func (m *Module) remoteSidebarLinks(w http.ResponseWriter, t Target) ([]Link, bool) {
	if m.d.SidebarLinks != nil {
		if snapshot, ready := m.d.SidebarLinks(t.Platform); ready {
			links := []Link{}
			for _, row := range snapshot {
				if row.SessionID == t.SessionID && row.Number > 0 && forge.ValidRepo(row.Remote.Repo) {
					links = append(links, Link{Platform: t.Platform, RemoteID: t.RemoteID, SessionID: t.SessionID, Directory: row.Directory,
						Remote: row.Remote, Number: row.Number, PR: row.PR, Watching: row.Watching, LastError: row.LastError})
				}
			}
			return links, true
		}
	}
	http.Error(w, "PR sidebar links unavailable", http.StatusServiceUnavailable)
	return nil, false
}

func (m *Module) listRemoteSidebar(w http.ResponseWriter, r *http.Request, t Target) {
	links, ok := m.remoteSidebarLinks(w, t)
	if !ok {
		return
	}
	q := r.URL.Query()
	links = filterLinks(links, q)
	ctx := github.WithCache(r.Context())
	if refreshRequested(q) {
		ctx = github.WithRefresh(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for i := range links {
		l := &links[i]
		if f, ok := m.d.ResolveForge(l.Remote); ok {
			if pr, err := f.LookupPR(ctx, l.Remote.Repo, l.Number); err == nil {
				l.PR = pr
				continue
			}
		}
		if l.LastError == "" {
			l.LastError = "Could not refresh PR metadata. Showing the last saved status."
		}
	}
	httpx.WriteJSON(w, map[string]any{"pullRequests": links})
}

func (m *Module) sidebarLinksOperation(w http.ResponseWriter, r *http.Request, platform string) {
	links, err := m.platformLinks(r.Context(), platform)
	if err != nil {
		http.Error(w, "Could not read PR sidebar links", http.StatusInternalServerError)
		return
	}
	result := make([]platforms.PullRequestSidebarLink, 0, len(links))
	for _, l := range links {
		l.PR.Body = "" // the linked card doesn't render the description
		l.Remote.URL = ""
		result = append(result, platforms.PullRequestSidebarLink{SessionID: l.SessionID, Directory: l.Directory,
			Remote: l.Remote, Number: l.Number, PR: l.PR, Watching: l.Watching, LastError: l.LastError})
	}
	httpx.WriteJSON(w, result)
}
