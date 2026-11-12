package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"sort"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/srvtiming"
)

// resolvePort looks up the running OpenCode HTTP port for the given session.
func (a *Adapter) resolvePort(sessionID string) (port string, session *db.Session, err error) {
	return a.resolvePortCtx(context.Background(), sessionID)
}

func (a *Adapter) resolvePortCtx(ctx context.Context, sessionID string) (port string, session *db.Session, err error) {
	if a.db == nil {
		return "", nil, platforms.ErrNotFound
	}
	dbPhase := srvtiming.Begin(ctx, "db_get_session")
	s, err := a.db.GetSession(ctx, sessionID)
	dbPhase.End()
	if err != nil {
		return "", nil, platforms.ErrNotFound
	}
	port, err = a.rt.resolveSessionPortCtx(ctx, sessionID, s.Directory)
	if err != nil {
		return "", s, err
	}
	if port == "" {
		return "", s, fmt.Errorf("no running OpenCode instance for session %s: %w", sessionID, platforms.ErrPlatformUnreachable)
	}
	a.rt.rememberSessionPort(sessionID, port)
	return port, s, nil
}

func (d *Discovery) resolveOpenCodePortBySession(ctx context.Context, sessionID string) (string, error) {
	for _, server := range d.discoverOpenCodeServers() {
		if server.port == "" {
			continue
		}
		found, err := d.probeSessionPort(ctx, server.port, sessionID)
		if err != nil {
			return "", err
		}
		if found {
			return server.port, nil
		}
	}
	return "", nil
}

// ProjectCatalog reads catalogs from a managed project before it has a session.
// directory scopes the read on the OpenCode server, which serves every project.
func (h *httpState) ProjectCatalog(ctx context.Context, endpoint, directory string) (agents, models []string, err error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Port() == "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		return nil, nil, errors.New("invalid OpenCode endpoint")
	}
	body, err := h.getJSONCached(ctx, u.Port(), scopedPath(ctx, u.Port(), "/agent", directory))
	if err != nil {
		return nil, nil, err
	}
	var rawAgents []map[string]interface{}
	if err := json.Unmarshal(body, &rawAgents); err != nil {
		return nil, nil, err
	}
	for _, agent := range rawAgents {
		if name := stringField(agent, "name"); name != "" {
			agents = append(agents, name)
		}
	}
	body, err = h.getJSONCached(ctx, u.Port(), scopedPath(ctx, u.Port(), "/provider", directory))
	if err != nil {
		return nil, nil, err
	}
	var providers OpenCodeProvidersResponse
	if err := json.Unmarshal(body, &providers); err != nil {
		return nil, nil, err
	}
	// Only connected providers, not the full models.dev catalog.
	for _, provider := range providers.All {
		if !slices.Contains(providers.Connected, provider.ID) {
			continue
		}
		for model := range provider.Models {
			models = append(models, provider.ID+"/"+model)
		}
	}
	sort.Strings(agents)
	sort.Strings(models)
	return agents, models, nil
}

// agentCatalogAt is a best-effort lookup for optional agent-name suggestions.
func (h *httpState) agentCatalogAt(ctx context.Context, port, sessionID, directory string) []platforms.AgentCatalogEntry {
	entries, _ := h.readAgentCatalog(ctx, port, sessionID, directory)
	return entries
}

func (h *httpState) readAgentCatalog(ctx context.Context, port, sessionID, directory string) ([]platforms.AgentCatalogEntry, error) {
	body, fetchErr := h.getJSONCached(ctx, port, scopedPath(ctx, port, "/agent", directory))
	if fetchErr != nil {
		logFetchFailure(fetchErr, log.Fields{"sessionID": sessionID, "port": port, "endpoint": "/agent", "directory": directory},
			"opencode: agent catalog fetch failed; returning empty list")
		return nil, catalogError(directory, "/agent", fetchErr)
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "port": port, "directory": directory, "error": err}).
			Warn("opencode: agent catalog decode failed; returning empty list")
		return nil, catalogError(directory, "/agent", err)
	}
	entries := make([]platforms.AgentCatalogEntry, 0, len(raw))
	for _, r := range raw {
		mode := stringField(r, "mode")
		entries = append(entries, platforms.AgentCatalogEntry{
			Name: stringField(r, "name"), Description: stringField(r, "description"),
			Model: stringField(r, "model"), Color: stringField(r, "color"),
			Kind: mode, Mode: mode, Hidden: boolField(r, "hidden"), BuiltIn: boolField(r, "native"),
		})
	}
	return entries, nil
}

func (h *httpState) slashCommandsAt(ctx context.Context, port, sessionID, directory string) []platforms.SlashCommandEntry {
	entries, _ := h.readSlashCommands(ctx, port, sessionID, directory)
	return entries
}

func (h *httpState) readSlashCommands(ctx context.Context, port, sessionID, directory string) ([]platforms.SlashCommandEntry, error) {
	body, fetchErr := h.getJSONCached(ctx, port, scopedPath(ctx, port, "/command", directory))
	if fetchErr != nil {
		logFetchFailure(fetchErr, log.Fields{"sessionID": sessionID, "port": port, "endpoint": "/command", "directory": directory},
			"opencode: slash commands fetch failed; returning empty list")
		return nil, catalogError(directory, "/command", fetchErr)
	}
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		log.WithFields(log.Fields{"sessionID": sessionID, "port": port, "directory": directory, "error": err}).
			Warn("opencode: slash commands decode failed; returning empty list")
		return nil, catalogError(directory, "/command", err)
	}
	entries := make([]platforms.SlashCommandEntry, 0, len(raw))
	for _, r := range raw {
		entries = append(entries, platforms.SlashCommandEntry{
			Name: stringField(r, "name"), Description: stringField(r, "description"),
			Template: stringField(r, "template"), Source: stringField(r, "source"),
		})
	}
	return entries, nil
}

func catalogError(directory, endpoint string, err error) error {
	return fmt.Errorf("OpenCode catalog %s for directory %q: %w", endpoint, directory, err)
}

func marshalRequest(v any) ([]byte, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	return payload, nil
}

func stringField(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func boolField(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}
