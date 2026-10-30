package prworkflow

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/forge/github"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

type Target struct{ Platform, SessionID, Directory, RemoteID string }
type Deps struct {
	// ProjectHandler serves only the project PR routes exposed to MCP.
	ProjectHandler http.Handler
	DB             *state.DB
	Target         func(http.ResponseWriter, *http.Request, string) (Target, bool)
	Owner          func(context.Context, Target) (hostsvc.Host, error)
	ResolveForge   func(forge.Remote) (forge.Forge, bool)
	// Enqueue is the shared session queue, never a platform-specific send.
	Enqueue func(context.Context, string, platforms.SendMessageRequest) error
	Ready   func(context.Context, Target) bool
	Now     func() time.Time
}
type Module struct {
	d      Deps
	mu     sync.Mutex
	paused map[string]time.Time
}

func New(d Deps) *Module     { return &Module{d: d, paused: make(map[string]time.Time)} }
func (*Module) Name() string { return "pr-workflow" }
func (m *Module) Routes(r module.Router) {
	r.Local("GET /api/session/{id}/pull-requests", m.list)
	r.Local("GET /api/session/{id}/pull-request-checks", m.checks)
	r.Local("POST /api/session/{id}/pull-requests", m.change)
	r.Local("POST /api/session/{id}/pull-request-feedback", m.feedback)
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	t, ok := m.d.Target(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	links, err := m.links(r.Context(), t.Platform, t.SessionID)
	if err != nil {
		http.Error(w, "Could not load linked PRs", http.StatusInternalServerError)
		return
	}
	for i := range links {
		if f, ok := m.d.ResolveForge(links[i].Remote); ok {
			ctx := github.WithCache(r.Context())
			if r.URL.Query().Get("refresh") == "true" {
				ctx = github.WithRefresh(ctx)
			}
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			pr, err := f.LookupPR(ctx, links[i].Remote.Repo, links[i].Number)
			cancel()
			if err == nil {
				links[i].PR = pr
			} else if links[i].LastError == "" {
				links[i].LastError = "Could not refresh PR metadata. Showing the last saved status."
			}
		}
	}
	httpx.WriteJSON(w, map[string]any{"pullRequests": links})
}

type changeRequest struct {
	Remote string `json:"remote"`
	Number int    `json:"number"`
	Action string `json:"action"`
	Host   string `json:"host"`
	Repo   string `json:"repo"`
}

func (m *Module) change(w http.ResponseWriter, r *http.Request) {
	t, ok := m.d.Target(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	var req changeRequest
	if !httpx.ReadJSON(w, r, 4096, &req) {
		return
	}
	if req.Number <= 0 {
		http.Error(w, "A positive PR number is required", http.StatusBadRequest)
		return
	}
	if req.Action == "link" && (req.Host != "" || req.Repo != "") {
		if _, err := explicitRemote(req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.d.DB == nil {
		http.Error(w, "PR storage unavailable", http.StatusServiceUnavailable)
		return
	}
	var l Link
	if req.Action == "link" {
		var err error
		if req.Host != "" || req.Repo != "" {
			l, err = m.externalLink(r.Context(), t, req)
		} else {
			l, err = m.newLink(r.Context(), t, req.Remote, req.Number)
		}
		if err != nil {
			http.Error(w, "Could not link PR. Check its number, repository, forge configuration and owner connection.", http.StatusBadGateway)
			return
		}
		// Linking twice must not reset an existing watch.
		links, err := m.links(r.Context(), t.Platform, t.SessionID)
		if err != nil {
			http.Error(w, "PR storage unavailable", http.StatusInternalServerError)
			return
		}
		for _, existing := range links {
			if existing.Remote.Host == l.Remote.Host && existing.Remote.Repo == l.Remote.Repo && existing.Number == l.Number {
				httpx.WriteJSON(w, existing)
				return
			}
		}
	} else {
		links, err := m.links(r.Context(), t.Platform, t.SessionID)
		if err != nil {
			http.Error(w, "PR storage unavailable", http.StatusInternalServerError)
			return
		}
		found := false
		for _, existing := range links {
			if existing.Remote.Host == req.Host && existing.Remote.Repo == req.Repo && existing.Number == req.Number {
				l = existing
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "Linked PR not found", http.StatusNotFound)
			return
		}
		switch req.Action {
		case "unlink":
			if err := m.remove(r.Context(), l); err != nil {
				http.Error(w, "Could not unlink PR", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		case "unwatch":
			l.Watching = false
			l.Pending = ""
			l.LastError = ""
		case "watch":
			if l.Watching {
				httpx.WriteJSON(w, l)
				return
			}
			if err := m.checkOwner(r.Context(), l); err != nil {
				http.Error(w, "Session owner unavailable", http.StatusServiceUnavailable)
				return
			}
			snapshot, pr, err := m.read(r.Context(), l)
			if err != nil {
				http.Error(w, "Could not read PR checks and feedback", http.StatusBadGateway)
				return
			}
			if pr.Status != "open" && pr.Status != "draft" {
				http.Error(w, "Only open PRs can be watched", http.StatusConflict)
				return
			}
			l.Snapshot, l.PR, l.Watching, l.Failures, l.CommentWakes, l.NextAt, l.LastError = snapshot, pr, true, 0, 0, 0, ""
		default:
			http.Error(w, "Unknown PR action", http.StatusBadRequest)
			return
		}
	}
	if err := m.put(r.Context(), l); err != nil {
		http.Error(w, "Could not save linked PR", http.StatusInternalServerError)
		return
	}
	httpx.WriteJSON(w, l)
}

func (m *Module) newLink(ctx context.Context, t Target, remote string, number int, known ...*forge.PR) (Link, error) {
	if m.d.Owner == nil {
		return Link{}, fmt.Errorf("owner unavailable")
	}
	h, err := m.d.Owner(ctx, t)
	if err != nil {
		return Link{}, err
	}
	upstreams, err := h.ProjectUpstreams(ctx, t.Directory)
	if err != nil {
		return Link{}, err
	}
	for _, rem := range upstreams.Remotes {
		if rem.Name != remote {
			continue
		}
		f, ok := m.d.ResolveForge(rem)
		if !ok {
			return Link{}, fmt.Errorf("forge unavailable")
		}
		var pr forge.PR
		if len(known) > 0 && known[0] != nil {
			pr = *known[0]
		} else {
			pr, err = f.LookupPR(ctx, rem.Repo, number)
			if err != nil {
				return Link{}, err
			}
		}
		return Link{Platform: t.Platform, SessionID: t.SessionID, Directory: t.Directory, RemoteID: t.RemoteID, Remote: rem, Number: number, PR: pr}, nil
	}
	return Link{}, fmt.Errorf("upstream not found")
}

// LinkCreated is called after an explicit PR launch. Errors never undo the launch.
func (m *Module) LinkCreated(ctx context.Context, t Target, remote string, number int, known ...*forge.PR) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, err := m.newLink(ctx, t, remote, number, known...)
	if err != nil {
		return err
	}
	return m.put(ctx, l)
}

func targetOf(l Link) Target { return Target{l.Platform, l.SessionID, l.Directory, l.RemoteID} }
func (m *Module) checkOwner(ctx context.Context, l Link) error {
	if m.d.Owner == nil {
		return fmt.Errorf("owner unavailable")
	}
	_, err := m.d.Owner(ctx, targetOf(l))
	return err
}

func bounded(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\x00", ""))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

func (m *Module) Run(ctx context.Context) {
	if m.d.DB == nil {
		return
	}
	module.Every(ctx, "pr-watch", 2*time.Minute, func(ctx context.Context) { m.sweep(ctx) })
}
