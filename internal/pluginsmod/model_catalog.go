package pluginsmod

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/remote"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func (m *Module) handlePluginModelCatalog(w http.ResponseWriter, r *http.Request) {
	owner, platform := r.URL.Query().Get("ownerId"), r.URL.Query().Get("platform")
	if owner == "" || platform == "" {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrInvalidMessage))
		return
	}
	raw, _ := json.Marshal(platform)
	writePluginResponse(w, m.RouteOperation(r.Context(), owner, remote.PluginRequest{
		Operation: "model-catalog", Input: remote.PluginInput{Values: map[string]json.RawMessage{"platform": raw}},
	}))
}

func (m *Module) pluginModelCatalog(ctx context.Context, input remote.PluginInput) (*platforms.SessionModelsResponse, error) {
	var requested string
	if json.Unmarshal(input.Values["platform"], &requested) != nil || requested == "" {
		return nil, plugins.ErrInvalidMessage
	}
	if m.deps.CreatePlatform == nil || m.deps.Local == nil || m.deps.Platform == nil {
		return nil, plugins.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	id, _, err := m.deps.CreatePlatform(ctx, m.deps.Local(), requested, "")
	if err != nil {
		return nil, plugins.ErrUnavailable
	}
	adapter, ok := m.deps.Platform(id)
	if !ok {
		return nil, plugins.ErrUnavailable
	}
	// Empty directory asks the adapter for its platform-wide catalog. Never
	// resolve a project, ensure a runtime, or create a session for a picker.
	catalog, err := adapter.DirectoryCatalog(ctx, platforms.DirectoryCatalogRequest{})
	if err != nil {
		return nil, plugins.ErrUnavailable
	}
	if catalog == nil || catalog.Models == nil {
		return &platforms.SessionModelsResponse{Models: []platforms.SessionModel{}}, nil
	}
	return catalog.Models, nil
}
