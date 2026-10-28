package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
)

const defaultAgentKey = "session.default_agent"

var defaultAgentOptions = knownAgentOptions

func knownAgentOptions(ctx context.Context, adapters []platforms.Platform, directories []string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	known := map[string]bool{"build": true, "plan": true}
	for _, adapter := range adapters {
		if owner, _ := platforms.SplitPlatformID(string(adapter.ID())); owner != "" {
			continue
		}
		if reader, ok := adapter.(platforms.AgentOptionsReader); ok {
			for _, agent := range reader.AgentOptions(ctx, directories) {
				known[agent] = true
			}
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

type defaultAgentBody struct {
	DefaultAgent string `json:"defaultAgent"`
}

func (s *Server) defaultAgentSetting() httpx.SettingRoute[defaultAgentBody] {
	return httpx.SettingRoute[defaultAgentBody]{
		Name:      "default agent",
		Available: s.hasStateDB,
		Get: func(ctx context.Context) (any, error) {
			agent, err := s.defaultAgent(ctx)
			if err != nil {
				return nil, err
			}
			directories := s.storedDirectories(ctx)
			var adapters []platforms.Platform
			if s.registry != nil {
				adapters = s.registry.Platforms()
			}
			return map[string]any{"defaultAgent": agent, "agents": defaultAgentOptions(ctx, adapters, directories)}, nil
		},
		Validate: func(body *defaultAgentBody) error {
			body.DefaultAgent = strings.TrimSpace(body.DefaultAgent)
			if body.DefaultAgent == "" || len(body.DefaultAgent) > 128 || strings.ContainsFunc(body.DefaultAgent, unicode.IsControl) {
				return errors.New("defaultAgent must be a non-empty agent name of at most 128 bytes")
			}
			return nil
		},
		Set: func(ctx context.Context, body defaultAgentBody) error {
			return s.stateDB.SetSetting(ctx, defaultAgentKey, body.DefaultAgent)
		},
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
