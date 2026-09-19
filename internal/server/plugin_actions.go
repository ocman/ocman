package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
)

func (s *Server) actionBroker() *plugins.ActionBroker {
	s.pluginActionsOnce.Do(func() {
		s.pluginActions = plugins.NewActionBroker(func(ctx context.Context, id string, use func(plugins.Description, []string) error) error {
			// Lock order matches discovery: server lifecycle, then durable state.
			s.pluginMu.Lock()
			defer s.pluginMu.Unlock()
			if s.stateDB == nil {
				return plugins.ErrUnavailable
			}
			return s.stateDB.WithPluginAuthorization(ctx, id, func(d plugins.Description, grants []string) error {
				execution, _ := ctx.Value(pluginExecutionKey{}).(pluginExecution)
				if !pluginScopeAllowed(d.Scope, execution) {
					return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
				}
				return use(d, grants)
			})
		}, func(ctx context.Context, id string, call plugins.Call) (<-chan plugins.Reply, error) {
			// The authorization callback holds pluginMu through admission.
			p := s.pluginProcesses[id]
			if p == nil {
				return nil, plugins.ErrUnavailable
			}
			if err := s.stateDB.ReservePluginOperation(ctx, id, call.OperationID); err != nil {
				return nil, err
			}
			return p.Call(ctx, call)
		})
	})
	return s.pluginActions
}

type listedPluginAction struct {
	PluginID string                   `json:"pluginId"`
	OwnerID  string                   `json:"ownerId"`
	Scope    plugins.Scope            `json:"scope"`
	Action   plugins.ActionDescriptor `json:"action"`
}

func (s *Server) handlePluginActions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	context := plugins.ActionContext{OwnerID: q.Get("ownerId"), ProjectID: q.Get("projectId"), SessionID: q.Get("sessionId"), Route: q.Get("route")}
	if context.OwnerID == "" {
		context.OwnerID = "local"
	}
	request := plugins.ActionRequest{PluginID: "org.ocman.validation", ActionID: "list", OperationID: "list", Placement: q.Get("placement"), Surface: q.Get("surface"), Context: context}
	if request.Validate() != nil {
		writeActionResponse(w, plugins.ActionResponse{Error: &plugins.WireError{Category: plugins.ErrorInvalidArgument}})
		return
	}
	if request.Placement == "global" {
		context.OwnerID, request.Context.OwnerID = "local", "local"
	}
	response := s.routePluginOperation(r.Context(), context.OwnerID, remote.PluginRequest{Operation: "actions", Action: request})
	if response.Error != nil {
		writePluginResponse(w, response)
		return
	}
	var actions []listedPluginAction
	if err := json.Unmarshal(response.Value, &actions); err != nil {
		writePluginResponse(w, pluginResponse(nil, err))
		return
	}
	if context.OwnerID != "local" {
		hub, err := s.localPluginActions(r.Context(), request, false)
		if err != nil {
			writePluginResponse(w, pluginResponse(nil, err))
			return
		}
		actions = append(actions, hub...)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(actions)
}

func (s *Server) handlePluginActionInvoke(w http.ResponseWriter, r *http.Request) {
	var request plugins.ActionRequest
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF {
		writeActionResponse(w, plugins.ActionResponse{Error: &plugins.WireError{Category: plugins.ErrorInvalidArgument}})
		return
	}
	if request.Context.OwnerID == "" {
		request.Context.OwnerID = "local"
	}
	if request.Validate() != nil {
		writeActionResponse(w, plugins.ActionResponse{Error: &plugins.WireError{Category: plugins.ErrorInvalidArgument}})
		return
	}
	owner := request.OwnerID
	if owner == "" {
		owner = request.Context.OwnerID
	}
	if request.Placement == "global" {
		owner, request.Context.OwnerID = "local", "local"
	}
	if owner == "local" && request.Context.OwnerID != "local" {
		// Hub-scoped actions still require a connected explicit project owner.
		check := s.routePluginOperation(r.Context(), request.Context.OwnerID, remote.PluginRequest{Operation: "available"})
		if check.Error != nil {
			writePluginResponse(w, check)
			return
		}
	}
	request.OwnerID = owner
	response := s.routePluginOperation(r.Context(), owner, remote.PluginRequest{Operation: "invoke", Action: request})
	if response.Error != nil {
		writePluginResponse(w, response)
		return
	}
	var result plugins.ActionResponse
	if err := json.Unmarshal(response.Value, &result); err != nil {
		writePluginResponse(w, pluginResponse(nil, err))
		return
	}
	writeActionResponse(w, result)
}

func writeActionResponse(w http.ResponseWriter, response plugins.ActionResponse) {
	status := http.StatusOK
	if response.Confirmation != nil {
		status = http.StatusConflict
	}
	if response.Error != nil {
		switch response.Error.Category {
		case plugins.ErrorInvalidArgument:
			status = http.StatusBadRequest
		case plugins.ErrorPermissionDenied:
			status = http.StatusForbidden
		case plugins.ErrorNotFound:
			status = http.StatusNotFound
		case plugins.ErrorConflict:
			status = http.StatusConflict
		case plugins.ErrorUnavailable:
			status = http.StatusServiceUnavailable
		case plugins.ErrorDeadlineExceeded:
			status = http.StatusGatewayTimeout
		case plugins.ErrorCancelled:
			status = http.StatusRequestTimeout
		default:
			status = http.StatusInternalServerError
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) handlePluginActionArtifact(w http.ResponseWriter, r *http.Request) {
	response := s.routePluginOperation(r.Context(), r.URL.Query().Get("ownerId"), remote.PluginRequest{Operation: "artifact", Handle: r.URL.Query().Get("handle")})
	var artifact pluginArtifact
	var err error
	if response.Error != nil {
		err = response.Error
	} else {
		err = json.Unmarshal(response.Value, &artifact)
	}
	if err != nil {
		wire := &plugins.WireError{Category: plugins.ErrorInternal}
		var safe *plugins.WireError
		if errors.As(err, &safe) {
			wire = safe
		} else if errors.Is(err, plugins.ErrUnavailable) {
			wire.Category = plugins.ErrorUnavailable
		}
		writeActionResponse(w, plugins.ActionResponse{Error: wire})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(artifact.Data)
}
