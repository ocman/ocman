package sessionlifecycle

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (m *Module) HandleSessionRestartOpencode(w http.ResponseWriter, r *http.Request) {
	if !m.d.Privileged(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	all := r.URL.Query().Get("all") == "true"
	force := r.URL.Query().Get("force") == "true"
	confirmed := r.URL.Query().Get("confirmed") == "true"
	if r.URL.Query().Get("all") != "" && !all || r.URL.Query().Get("force") != "" && !force || r.URL.Query().Get("confirmed") != "" && !confirmed {
		http.Error(w, "invalid restart options", http.StatusBadRequest)
		return
	}
	m.d.WithSessionAdapter(w, r, func(w http.ResponseWriter, r *http.Request, sessionID string, adapter platforms.Platform) {
		detail, err := adapter.Session(r.Context(), sessionID, 0, 0)
		if err != nil {
			httpx.WritePlatformError(w, "loading session", err)
			return
		}
		if detail == nil || detail.Session == nil || detail.Session.Directory == "" {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		targets, err := m.RestartTargets(r.Context(), detail.Session.Directory, detail.Session.RemoteID, all)
		if err != nil {
			httpx.ServerError(w, "listing managed opencode", err)
			return
		}
		if len(targets) == 0 {
			http.Error(w, "no managed OpenCode instance found", http.StatusNotFound)
			return
		}
		busy, err := m.waitForRestartIdle(r.Context(), targets, force)
		if err != nil {
			httpx.ServerError(w, "checking OpenCode sessions", err)
			return
		}
		if force && !confirmed && len(busy) > 0 {
			httpx.WriteJSON(w, map[string]any{"confirmationRequired": true, "busySessions": busy})
			return
		}
		for _, target := range targets {
			if _, err := target.Host.RestartProjectOpencode(r.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: target.Root}); err != nil {
				httpx.ServerError(w, "restarting opencode", err)
				return
			}
		}
		httpx.WriteJSON(w, map[string]any{"restarted": len(targets)})
	})
}

type RestartTarget struct {
	Host hostsvc.Host
	Root string
	// Machine means the instance serves every project on the host (OpenCode v2).
	Machine bool
}

func (m *Module) RestartTargets(ctx context.Context, dir, remoteID string, all bool) ([]RestartTarget, error) {
	var hosts []hostsvc.Host
	if all {
		hosts = append([]hostsvc.Host{m.d.Router().Local()}, mapsValues(m.d.Router().Remotes())...)
	} else if remoteID != "" && remoteID != "local" {
		host, ok := m.d.Router().LookupRemote(remoteID)
		if !ok {
			return nil, fmt.Errorf("remote host %q is unavailable", remoteID)
		}
		hosts = []hostsvc.Host{host}
	} else {
		hosts = []hostsvc.Host{m.d.Router().ForDir(dir)}
	}
	seen := make(map[string]bool)
	var targets []RestartTarget
	for _, host := range hosts {
		if seen[host.RemoteID()] {
			continue
		}
		seen[host.RemoteID()] = true
		instances, err := host.ManagedOpencodes(ctx)
		if err != nil {
			return nil, err
		}
		for _, instance := range instances {
			if instance.Machine {
				// Restart the shared server once, whatever else is listed.
				targets = slices.DeleteFunc(targets, func(t RestartTarget) bool { return t.Host == host })
				targets = append(targets, RestartTarget{Host: host, Root: instance.RepoRoot, Machine: true})
				break
			}
			if all || SameProject(instance.RepoRoot, dir) {
				targets = append(targets, RestartTarget{Host: host, Root: instance.RepoRoot})
			}
		}
	}
	return targets, nil
}

func (m *Module) waitForRestartIdle(ctx context.Context, targets []RestartTarget, force bool) ([]string, error) {
	for {
		busy, err := m.restartBusySessions(ctx, targets)
		if err != nil || force || len(busy) == 0 {
			return busy, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (m *Module) restartBusySessions(ctx context.Context, targets []RestartTarget) ([]string, error) {
	var busy []string
	for _, adapter := range m.d.Platforms() {
		sessions, err := adapter.Sessions(ctx, "", 0)
		if err != nil {
			return nil, err
		}
		for _, session := range sessions {
			if session.Status != db.StatusBusy {
				continue
			}
			owner := session.RemoteID
			if owner == "" {
				owner = "local"
			}
			for _, target := range targets {
				if target.Host.RemoteID() != owner || (!target.Machine && !SameProject(target.Root, session.Directory)) {
					continue
				}
				busy = append(busy, session.ID)
				break
			}
		}
	}
	return busy, nil
}

func SameProject(root, dir string) bool {
	return pathContains(root, dir) || pathContains(filepath.Join(filepath.Dir(root), ".worktrees", filepath.Base(root)), dir)
}

func pathContains(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func mapsValues(m map[string]hostsvc.Host) []hostsvc.Host {
	out := make([]hostsvc.Host, 0, len(m))
	for _, host := range m {
		out = append(out, host)
	}
	return out
}
