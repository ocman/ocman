package pluginsmod

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/remote"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

// The normal authenticated module route projects only the named owner's exact
// declared icon. It never reads a host filesystem path or serves plugin markup.
func (m *Module) handlePluginIcon(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("ownerId") == "" || !plugins.ValidIconPath(q.Get("path")) {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrInvalidMessage))
		return
	}
	raw, _ := json.Marshal(plugins.AssetRequest{Path: q.Get("path")})
	response := m.RouteOperation(r.Context(), q.Get("ownerId"), remote.PluginRequest{Operation: "asset-read", PluginID: q.Get("pluginId"), Input: remote.PluginInput{Values: map[string]json.RawMessage{"asset": raw}}})
	var data []byte
	if response.Error == nil && (json.Unmarshal(response.Value, &data) != nil || plugins.ValidateIconSVG(data) != nil) {
		response = pluginResponse(nil, plugins.ErrInvalidMessage)
	}
	if response.Error != nil {
		writePluginResponse(w, response)
		return
	}
	h := w.Header()
	httpx.SetInertFileHeaders(w)
	h.Set("Content-Type", "image/svg+xml")
	h.Set("Cache-Control", "private, no-store")
	_, _ = w.Write(data)
}

func iconAllowed(path string) func(plugins.Description, []string) error {
	return func(d plugins.Description, _ []string) error {
		if d.Icon == nil || d.Icon.Path != path {
			return &plugins.WireError{Category: plugins.ErrorNotFound}
		}
		return nil
	}
}

func (m *Module) localPluginIcon(ctx context.Context, id string, raw json.RawMessage) ([]byte, error) {
	req, err := plugins.DecodeAssetRequest(raw)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	allowed := iconAllowed(req.Path)
	var replies <-chan plugins.Reply
	m.mu.Lock()
	if m.store == nil {
		err = plugins.ErrUnavailable
	} else {
		err = m.store.WithPluginAuthorization(ctx, id, func(d plugins.Description, grants []string) error {
			if err := allowed(d, grants); err != nil {
				return err
			}
			if p := m.processes[id]; p != nil {
				var e error
				replies, e = p.Call(ctx, plugins.Call{OperationID: "asset:" + rand.Text(), Capability: plugins.AssetCapability.Name, Version: plugins.AssetCapability.Version, Method: "read", Params: raw, DeadlineUnixMS: time.Now().Add(5 * time.Second).UnixMilli()})
				return e
			}
			return plugins.ErrUnavailable
		})
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case reply, ok := <-replies:
		if !ok {
			return nil, plugins.ErrUnavailable
		}
		if reply.Err != nil {
			return nil, reply.Err
		}
		result := reply.Message.Result
		if reply.Message.Type != plugins.TypeResult || result == nil {
			return nil, plugins.ErrInvalidMessage
		}
		if result.Error != nil {
			return nil, result.Error
		}
		data, err := plugins.DecodeAssetResult(result.Value)
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if err := m.store.WithPluginAuthorization(ctx, id, allowed); err != nil {
			return nil, err
		}
		return data, nil
	}
}
