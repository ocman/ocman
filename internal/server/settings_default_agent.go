package server

import (
	"context"
	"maps"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	log "github.com/sirupsen/logrus"
)

const defaultAgentKey = "session.default_agent"

var defaultAgentOptions = knownAgentOptions
var defaultAgentPorts = opencode.DiscoverOpenCodePortsContext
var defaultAgentCatalog = opencode.AgentNames
var defaultAgentPort = opencode.DiscoverOpenCodePortContext

func knownAgentOptions(ctx context.Context, directories []string) []string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	known := map[string]bool{"build": true, "plan": true}
	targets := maps.Clone(defaultAgentPorts(ctx))
	if targets == nil {
		targets = make(map[string]string)
	}
	for _, directory := range directories {
		if ctx.Err() != nil {
			break
		}
		if port := defaultAgentPort(ctx, directory); port != "" {
			targets[directory] = port
		}
	}
	for directory, port := range targets {
		if ctx.Err() != nil {
			break
		}
		for _, agent := range defaultAgentCatalog(ctx, port, directory) {
			known[agent] = true
		}
	}
	agents := make([]string, 0, len(known))
	for agent := range known {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	return agents
}

func (s *Server) defaultAgent(ctx context.Context) (string, error) {
	value, _, err := s.stateDB.GetSetting(ctx, defaultAgentKey)
	if value == "" {
		value = "build"
	}
	return value, err
}

func (s *Server) handleDefaultAgent(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		agent, err := s.defaultAgent(r.Context())
		if err != nil {
			serverError(w, "reading default agent", err)
			return
		}
		var directories []string
		if s.db != nil {
			directories, err = s.db.StatusCandidateDirectories(r.Context(), 0)
			if err != nil {
				log.WithError(err).Warn("reading local directories for default-agent options")
			}
		}
		writeJSON(w, map[string]any{"defaultAgent": agent, "agents": defaultAgentOptions(r.Context(), directories)})
	case http.MethodPost:
		var body struct {
			DefaultAgent string `json:"defaultAgent"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &body) {
			return
		}
		body.DefaultAgent = strings.TrimSpace(body.DefaultAgent)
		if body.DefaultAgent == "" || len(body.DefaultAgent) > 128 || strings.ContainsFunc(body.DefaultAgent, unicode.IsControl) {
			http.Error(w, "defaultAgent must be a non-empty agent name of at most 128 bytes", http.StatusBadRequest)
			return
		}
		if err := s.stateDB.SetSetting(r.Context(), defaultAgentKey, body.DefaultAgent); err != nil {
			serverError(w, "saving default agent", err)
			return
		}
		writeJSON(w, body)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type settingsResponse struct {
	http.ResponseWriter
	status int
}

func (w *settingsResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// settingsHandler invalidates every client's settings cache after a successful save.
func (s *Server) settingsHandler(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := &settingsResponse{ResponseWriter: w, status: http.StatusOK}
		handler(response, r)
		if r.Method == http.MethodPost && response.status < 300 {
			s.broadcastGlobalEvent("ocman.settings.changed", []byte(`{}`))
		}
	}
}
