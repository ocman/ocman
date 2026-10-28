package pluginsmod

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

// In-app plugin UI runs with the user's ocman session, so v1 only loads it
// for plugins executing on this machine. Remote owners keep declarative views.
const uiOwner = "local"

type listedPluginUI struct {
	PluginID    string                     `json:"pluginId"`
	PluginLabel string                     `json:"pluginLabel"`
	OwnerID     string                     `json:"ownerId"`
	SDK         string                     `json:"sdk"`
	HostSDK     string                     `json:"hostSdk"`
	Compatible  bool                       `json:"compatible"`
	Index       *listedPluginUIExpose      `json:"index,omitempty"`
	Actions     []plugins.ActionDescriptor `json:"actions"`
}

type listedPluginUIExpose struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type cachedUIBundle struct {
	sha  string
	data []byte
}

func uiBundleURL(id, sha string) string {
	return "/api/plugins/ui/bundle?pluginId=" + id + "&expose=" + plugins.UIIndexExpose + "&sha256=" + sha
}

// pluginUIDeclarations lists enabled, granted UI plugins without invoking
// them. Incompatible SDK ranges are listed so the browser can explain why
// the UI is not loaded instead of failing on import.
func pluginUIDeclarations(registrations []state.PluginRegistration) []listedPluginUI {
	listed := []listedPluginUI{}
	for _, p := range registrations {
		d := p.Description
		if !p.Enabled || p.Removed || p.Health.Status == "conflict" || d.UI == nil || !slices.Contains(p.Grants, plugins.UIAppGrant) {
			continue
		}
		entry := listedPluginUI{PluginID: d.ID, PluginLabel: d.Name, OwnerID: uiOwner, SDK: d.UI.SDK, HostSDK: plugins.UISDKVersion,
			Compatible: d.UIAllowed(p.Grants), Actions: []plugins.ActionDescriptor{}}
		// The UI runs actions through the palette dialog, so it may only see
		// granted command-palette actions.
		for _, a := range d.Actions {
			if slices.Contains(a.Surfaces, "command-palette") && a.Granted(p.Grants) {
				entry.Actions = append(entry.Actions, a)
			}
		}
		if expose, ok := d.UI.Exposes[plugins.UIIndexExpose]; ok && entry.Compatible {
			entry.Index = &listedPluginUIExpose{URL: uiBundleURL(d.ID, expose.SHA256), SHA256: expose.SHA256}
		}
		listed = append(listed, entry)
	}
	return listed
}

func (m *Module) handlePluginUIs(w http.ResponseWriter, r *http.Request) {
	response := m.RouteOperation(r.Context(), uiOwner, remote.PluginRequest{Operation: "catalog"})
	if response.Error != nil {
		writePluginResponse(w, response)
		return
	}
	var registrations []state.PluginRegistration
	if json.Unmarshal(response.Value, &registrations) != nil {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrUnavailable))
		return
	}
	writePluginResponse(w, pluginResponse(pluginUIDeclarations(registrations), nil))
}

func (m *Module) handlePluginUIBundle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	input := map[string]json.RawMessage{}
	for _, key := range []string{"expose", "sha256"} {
		input[key], _ = json.Marshal(q.Get(key))
	}
	response := m.RouteOperation(r.Context(), uiOwner, remote.PluginRequest{Operation: "ui-bundle", PluginID: q.Get("pluginId"), Input: remote.PluginInput{Values: input}})
	var bundle []byte
	if response.Error != nil || json.Unmarshal(response.Value, &bundle) != nil {
		if response.Error == nil {
			response = pluginResponse(nil, plugins.ErrUnavailable)
		}
		writePluginResponse(w, response)
		return
	}
	etag := `"` + q.Get("sha256") + `"`
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", etag)
	h.Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(bundle)
}

func (m *Module) handlePluginUICall(w http.ResponseWriter, r *http.Request) {
	var input struct {
		PluginID string          `json:"pluginId"`
		Method   string          `json:"method"`
		Params   json.RawMessage `json:"params,omitempty"`
	}
	if !httpx.ReadJSON(w, r, plugins.MaxMessageBytes, &input) {
		return
	}
	raw, _ := json.Marshal(plugins.UICall{Method: input.Method, Params: input.Params})
	writePluginResponse(w, m.RouteOperation(r.Context(), uiOwner, remote.PluginRequest{Operation: "ui-call", PluginID: input.PluginID, Input: remote.PluginInput{Values: map[string]json.RawMessage{"call": raw}}}))
}

// localPluginUI serves the two owner-local ui operations. Remote callers are
// refused: a hub never loads UI code from another machine in v1.
func (m *Module) localPluginUI(ctx context.Context, req remote.PluginRequest, fromRemote bool) remote.PluginResponse {
	if fromRemote {
		return pluginResponse(nil, &plugins.WireError{Category: plugins.ErrorPermissionDenied})
	}
	if req.Operation == "ui-call" {
		return pluginResponse(m.callLocalPluginUI(ctx, req.PluginID, req.Input.Values["call"]))
	}
	var expose, sha string
	if json.Unmarshal(req.Input.Values["expose"], &expose) != nil || json.Unmarshal(req.Input.Values["sha256"], &sha) != nil {
		return pluginResponse(nil, plugins.ErrInvalidMessage)
	}
	return pluginResponse(m.localPluginUIBundle(ctx, req.PluginID, expose, sha))
}

func uiAllowed(expose, sha string) func(plugins.Description, []string) error {
	return func(d plugins.Description, grants []string) error {
		if !d.UIAllowed(grants) {
			return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
		}
		if expose != "" {
			if declared, ok := d.UI.Exposes[expose]; !ok || declared.SHA256 != sha {
				return &plugins.WireError{Category: plugins.ErrorNotFound}
			}
		}
		return nil
	}
}

// startUICall admits and sends one ui call atomically with revocation, the
// same lock order as panes and pages.
func (m *Module) startUICall(ctx context.Context, id string, allowed func(plugins.Description, []string) error, call plugins.Call) (<-chan plugins.Reply, plugins.Description, error) {
	var replies <-chan plugins.Reply
	var description plugins.Description
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return nil, description, plugins.ErrUnavailable
	}
	err := m.store.WithPluginAuthorization(ctx, id, func(d plugins.Description, grants []string) error {
		if err := allowed(d, grants); err != nil {
			return err
		}
		description = d
		p := m.processes[id]
		if p == nil {
			return plugins.ErrUnavailable
		}
		call.OperationID = "ui:" + rand.Text()
		call.Capability, call.Version = plugins.UICapability.Name, plugins.UICapability.Version
		call.DeadlineUnixMS = time.Now().Add(30 * time.Second).UnixMilli()
		var err error
		replies, err = p.Call(ctx, call)
		return err
	})
	return replies, description, err
}

func (m *Module) recheckUI(ctx context.Context, id string, allowed func(plugins.Description, []string) error) error {
	return m.store.WithPluginAuthorization(ctx, id, func(d plugins.Description, grants []string) error { return allowed(d, grants) })
}

func (m *Module) callLocalPluginUI(ctx context.Context, id string, raw json.RawMessage) (json.RawMessage, error) {
	if _, err := plugins.DecodeUICall(raw); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	allowed := uiAllowed("", "")
	replies, _, err := m.startUICall(ctx, id, allowed, plugins.Call{Method: "call", Params: raw})
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case reply := <-replies:
		if reply.Err != nil {
			return nil, reply.Err
		}
		// ui calls are unary: a chunk is a protocol error, not partial data.
		result := reply.Message.Result
		if reply.Message.Type != plugins.TypeResult || result == nil {
			return nil, plugins.ErrInvalidMessage
		}
		if result.Error != nil {
			return nil, result.Error
		}
		if err := m.recheckUI(ctx, id, allowed); err != nil {
			return nil, err
		}
		return result.Value, nil
	}
}

func (m *Module) localPluginUIBundle(ctx context.Context, id, expose, sha string) ([]byte, error) {
	if expose != plugins.UIIndexExpose || len(sha) != 64 {
		return nil, plugins.ErrInvalidMessage
	}
	allowed := uiAllowed(expose, sha)
	m.mu.Lock()
	cached, hit := m.uiBundles[id]
	m.mu.Unlock()
	if hit && cached.sha == sha {
		if err := m.authorizeUIBundle(ctx, id, allowed); err != nil {
			return nil, err
		}
		return cached.data, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	params, _ := json.Marshal(plugins.UIBundleRequest{Expose: expose})
	replies, d, err := m.startUICall(ctx, id, allowed, plugins.Call{Method: "bundle", Params: params})
	if err != nil {
		return nil, err
	}
	declared := d.UI.Exposes[expose]
	var bundle []byte
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case reply := <-replies:
			if reply.Err != nil {
				return nil, reply.Err
			}
			if chunk := reply.Message.Chunk; chunk != nil {
				part, err := plugins.DecodeUIBundleChunk(chunk.Data)
				if err != nil || len(bundle)+len(part) > declared.Size {
					return nil, plugins.ErrInvalidMessage
				}
				bundle = append(bundle, part...)
				continue
			}
			result := reply.Message.Result
			if result == nil {
				return nil, plugins.ErrInvalidMessage
			}
			if result.Error != nil {
				return nil, result.Error
			}
			// The approved checksum is the only identity the browser loads.
			if err := plugins.VerifyUIBundle(declared, bundle); err != nil {
				return nil, err
			}
			if err := m.recheckUI(ctx, id, allowed); err != nil {
				return nil, err
			}
			m.mu.Lock()
			if m.uiBundles == nil {
				m.uiBundles = map[string]cachedUIBundle{}
			}
			m.uiBundles[id] = cachedUIBundle{sha: sha, data: bundle}
			m.mu.Unlock()
			return bundle, nil
		}
	}
}

func (m *Module) authorizeUIBundle(ctx context.Context, id string, allowed func(plugins.Description, []string) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil || m.processes[id] == nil {
		return plugins.ErrUnavailable
	}
	return m.recheckUI(ctx, id, allowed)
}
