package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

type pluginExecution struct {
	remote bool
	owner  string
}
type pluginExecutionKey struct{}

func pluginScopeAllowed(scope plugins.Scope, execution pluginExecution) bool {
	if execution.remote {
		return scope == plugins.ScopeOwner
	}
	return scope == plugins.ScopeHub || execution.owner == "" || execution.owner == "local"
}

func validPluginInput(input remote.PluginInput) bool {
	data, err := json.Marshal(input)
	return err == nil && len(data) <= plugins.MaxMessageBytes
}

// RemoteOperation runs only owner-local operations after gRPC authentication.
// It cannot forward to another owner or invoke hub-scoped/global actions.
func (m *Module) RemoteOperation(ctx context.Context, req remote.PluginRequest) remote.PluginResponse {
	return m.localPluginOperation(ctx, req, true)
}

func pluginResponse(value any, err error) remote.PluginResponse {
	if err == nil {
		data, marshalErr := json.Marshal(value)
		if marshalErr == nil {
			if len(data) > remote.MaxPluginResponseBytes {
				return remote.PluginResponse{Error: &plugins.WireError{Category: plugins.ErrorUnavailable}}
			}
			return remote.PluginResponse{Value: data}
		}
		err = marshalErr
	}
	category := plugins.ErrorInternal
	var wire *plugins.WireError
	switch {
	case errors.As(err, &wire):
		category = wire.Category
	case errors.Is(err, state.ErrPluginNotFound):
		category = plugins.ErrorNotFound
	case errors.Is(err, state.ErrPluginInvalid), errors.Is(err, plugins.ErrInvalidMessage):
		category = plugins.ErrorInvalidArgument
	case errors.Is(err, errPluginConflict):
		category = plugins.ErrorConflict
	case errors.Is(err, plugins.ErrUnavailable), errors.Is(err, platforms.ErrRemoteOffline):
		category = plugins.ErrorUnavailable
	case errors.Is(err, context.Canceled):
		category = plugins.ErrorCancelled
	case errors.Is(err, context.DeadlineExceeded):
		category = plugins.ErrorDeadlineExceeded
	}
	return remote.PluginResponse{Error: &plugins.WireError{Category: category}}
}

func (m *Module) localPluginOperation(ctx context.Context, req remote.PluginRequest, fromRemote bool) remote.PluginResponse {
	ctx = context.WithValue(ctx, pluginExecutionKey{}, pluginExecution{remote: fromRemote, owner: req.Action.Context.OwnerID})
	switch req.Operation {
	case "page-read", "page-save":
		method := "read"
		if req.Operation == "page-save" {
			method = "save"
		}
		return pluginResponse(m.localPluginPage(ctx, req.PluginID, method, req.Input.Values["page"]))
	case "pane-read":
		return pluginResponse(m.readLocalPluginPane(ctx, req.Pane))
	case "project-catalog":
		if req.PluginID != "org.ocman.slack" || req.Read {
			return pluginResponse(nil, plugins.ErrInvalidMessage)
		}
		return pluginResponse(m.pluginProjectCatalog(ctx, req.Input))
	case "available":
		return pluginResponse(true, nil)
	case "discovery":
		m.mu.Lock()
		defer m.mu.Unlock()
		return pluginResponse(append([]pluginDiscoveryFailure{}, m.discovery...), nil)
	case "catalog", "rescan":
		if m.store == nil {
			return pluginResponse(nil, plugins.ErrUnavailable)
		}
		if req.Operation == "rescan" {
			if _, err := m.RescanPlugins(ctx); err != nil {
				return pluginResponse(nil, err)
			}
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		return pluginResponse(m.store.ListPlugins(ctx))
	case "actions", "invoke":
		if req.Action.Validate() != nil {
			return pluginResponse(nil, plugins.ErrInvalidMessage)
		}
		if fromRemote && req.Action.Placement == "global" {
			return pluginResponse(nil, &plugins.WireError{Category: plugins.ErrorPermissionDenied})
		}
		if req.Operation == "invoke" {
			return pluginResponse(m.Actions().Invoke(ctx, req.Action), nil)
		}
		return pluginResponse(m.localPluginActions(ctx, req.Action, fromRemote))
	case "artifact":
		name, data, err := m.Actions().Artifact(ctx, req.Handle)
		return pluginResponse(pluginArtifact{Name: name, Data: data}, err)
	case "health", "stderr", "conversations":
		if !req.Read {
			return pluginResponse(nil, plugins.ErrInvalidMessage)
		}
	case "configuration", "grants":
	case "enable", "disable", "restart", "retry", "configuration/validate", "remove-data",
		"conversations/retry", "conversations/discard":
		if req.Read {
			return pluginResponse(nil, plugins.ErrInvalidMessage)
		}
	default:
		return pluginResponse(nil, plugins.ErrInvalidMessage)
	}
	// Apply the same canonical configuration limit on both transports, allowing
	// the remote request additional room for its routing envelope.
	if !validPluginInput(req.Input) {
		return pluginResponse(nil, plugins.ErrInvalidMessage)
	}
	return pluginResponse(m.manageLocalPlugin(ctx, req.PluginID, req.Operation, req.Read, req.Input))
}

type pluginArtifact struct {
	Name string `json:"name"`
	Data []byte `json:"data"`
}

// OwnerID qualifies the registration and each capability instance. The tuple
// (ownerId, description.id, capability/instance id) is the public identity.
type ownedPlugin struct {
	state.PluginRegistration
	OwnerID string `json:"ownerId"`
}

// RouteOperation runs req on owner ("local" or empty for this machine) and
// qualifies the answer with that owner at the hub.
func (m *Module) RouteOperation(ctx context.Context, owner string, req remote.PluginRequest) remote.PluginResponse {
	if !validPluginInput(req.Input) {
		return pluginResponse(nil, plugins.ErrInvalidMessage)
	}
	if owner == "" {
		owner = "local"
	}
	var response remote.PluginResponse
	if owner == "local" {
		response = m.localPluginOperation(ctx, req, false)
	} else if remotes := m.remotes(); remotes == nil {
		return pluginResponse(nil, platforms.ErrRemoteOffline)
	} else {
		var err error
		response, err = remotes.PluginOperation(ctx, owner, req)
		if err != nil {
			// Transport errors never expose remote diagnostics or request contents.
			return pluginResponse(nil, platforms.ErrRemoteOffline)
		}
	}
	if response.Error != nil {
		return response
	}
	// Qualify at the hub, never trust an owner identity from a remote response.
	switch req.Operation {
	case "catalog", "rescan":
		var catalog []ownedPlugin
		if err := json.Unmarshal(response.Value, &catalog); err != nil {
			return pluginResponse(nil, err)
		}
		for i := range catalog {
			catalog[i].OwnerID = owner
		}
		return pluginResponse(catalog, nil)
	case "actions":
		var actions []listedPluginAction
		if err := json.Unmarshal(response.Value, &actions); err != nil {
			return pluginResponse(nil, err)
		}
		for i := range actions {
			actions[i].OwnerID = owner
		}
		return pluginResponse(actions, nil)
	case "enable", "disable", "restart", "retry", "configuration", "grants":
		if !req.Read {
			var registration ownedPlugin
			if err := json.Unmarshal(response.Value, &registration); err != nil {
				return pluginResponse(nil, err)
			}
			registration.OwnerID = owner
			return pluginResponse(registration, nil)
		}
	}
	return response
}

func (m *Module) servePluginOperation(w http.ResponseWriter, r *http.Request, req remote.PluginRequest) {
	response := m.RouteOperation(r.Context(), r.URL.Query().Get("ownerId"), req)
	writePluginResponse(w, response)
}

func writePluginResponse(w http.ResponseWriter, response remote.PluginResponse) {
	if response.Error != nil {
		writeActionResponse(w, plugins.ActionResponse{Error: response.Error})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, response.Value)
}

func (m *Module) localPluginActions(ctx context.Context, request plugins.ActionRequest, fromRemote bool) ([]listedPluginAction, error) {
	actions := []listedPluginAction{}
	if m.store == nil {
		return actions, nil
	}
	registrations, err := m.store.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range registrations {
		if !p.Enabled || p.Removed || !pluginScopeAllowed(p.Description.Scope, pluginExecution{remote: fromRemote, owner: request.Context.OwnerID}) {
			continue
		}
		for _, a := range p.Description.Actions {
			if a.Placement == request.Placement && slices.Contains(a.Surfaces, request.Surface) && a.Granted(p.Grants) {
				actions = append(actions, listedPluginAction{PluginID: p.Description.ID, OwnerID: "local", Scope: p.Description.Scope, Action: a})
			}
		}
	}
	return actions, nil
}

func (m *Module) remotes() Remotes {
	if m.deps.Remotes == nil {
		return nil
	}
	return m.deps.Remotes()
}
