package server

import (
	"context"
	"encoding/json"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// hookPlugin is a platform whose CLI can report live status for sessions
// started in a terminal through an opt-in hook plugin (FR-6, AD-11).
type hookPlugin interface {
	Hook(ctx context.Context, event providerprotocol.HookEvent) error
	hostsvc.HookPluginManager
}

func (s *Server) hookPlugin() hookPlugin {
	for _, p := range s.registry.Platforms() {
		if provider, ok := p.(hostsvc.PlatformServiceProvider); ok {
			if hooks := provider.HostServices().Hooks; hooks != nil {
				return hooks
			}
		}
		if h, ok := p.(hookPlugin); ok {
			return h
		}
	}
	return nil
}

// handleHook receives `ocman hook` posts on the loopback MCP listener. The
// hook gives up after a second, so failures are only logged.
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	var event providerprotocol.HookEvent
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&event); err != nil {
		http.Error(w, "invalid hook event", http.StatusBadRequest)
		return
	}
	p := s.hookPlugin()
	if p == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := p.Hook(r.Context(), event); err != nil {
		log.WithError(err).WithField("event", event.Event).Debug("claude hook")
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleHookPlugin reports the hook plugin's state; POST /install and
// /uninstall change it through the CLI and report the new state.
func (s *Server) handleHookPlugin(w http.ResponseWriter, r *http.Request) {
	p, ok := s.router().Local().(hostsvc.HookPluginManager)
	if !ok {
		httpx.WriteJSON(w, map[string]bool{"available": false, "installed": false})
		return
	}
	var err error
	switch r.URL.Path {
	case "/api/hook-plugin/install":
		err = p.InstallHookPlugin(r.Context())
	case "/api/hook-plugin/uninstall":
		err = p.UninstallHookPlugin(r.Context())
	}
	if err != nil {
		log.WithError(err).Warn("changing the Claude hook plugin")
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	available, installed := p.HookPlugin()
	httpx.WriteJSON(w, map[string]bool{"available": available, "installed": installed})
}
