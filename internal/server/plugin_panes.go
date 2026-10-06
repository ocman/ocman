package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"time"

	"net/http"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
)

type listedPluginPane struct {
	PluginID string                 `json:"pluginId"`
	OwnerID  string                 `json:"ownerId"`
	Pane     plugins.PaneDescriptor `json:"pane"`
}

func (s *Server) handlePluginPanes(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("ownerId")
	if owner == "" {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrInvalidMessage))
		return
	}
	writePluginResponse(w, s.routePluginOperation(r.Context(), owner, remote.PluginRequest{Operation: "panes", Pane: plugins.PaneRequest{OwnerID: owner}}))
}

func (s *Server) handlePluginPaneRead(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	request := plugins.PaneRequest{PluginID: q.Get("pluginId"), PaneID: q.Get("paneId"), OwnerID: q.Get("ownerId"), Directory: q.Get("directory")}
	if request.Validate() != nil {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrInvalidMessage))
		return
	}
	writePluginResponse(w, s.routePluginOperation(r.Context(), request.OwnerID, remote.PluginRequest{Operation: "pane-read", Pane: request}))
}

// Listing declarations never invokes plugins or probes a project's tools.
func (s *Server) localPluginPanes(ctx context.Context) ([]listedPluginPane, error) {
	panes := []listedPluginPane{}
	if s.stateDB == nil {
		return panes, nil
	}
	registrations, err := s.stateDB.ListPlugins(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range registrations {
		if !p.Enabled || p.Removed || p.Health.Status == "conflict" {
			continue
		}
		for _, pane := range p.Description.Panes {
			if p.Description.PaneAllowed(pane.ID, p.Grants) {
				panes = append(panes, listedPluginPane{PluginID: p.Description.ID, OwnerID: "local", Pane: pane})
			}
		}
	}
	return panes, nil
}

func (s *Server) readLocalPluginPane(ctx context.Context, request plugins.PaneRequest) (plugins.PaneTree, error) {
	if request.Validate() != nil {
		return plugins.PaneTree{}, plugins.ErrInvalidMessage
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var replies <-chan plugins.Reply
	// Match lifecycle lock ordering. Authorization and call admission are atomic
	// with disable/revocation; reads have no durable side-effect receipt.
	s.pluginMu.Lock()
	var err error
	if s.stateDB == nil {
		err = plugins.ErrUnavailable
	} else {
		err = s.stateDB.WithPluginAuthorization(ctx, request.PluginID, func(d plugins.Description, grants []string) error {
			if !d.PaneAllowed(request.PaneID, grants) {
				return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
			}
			p := s.pluginProcesses[request.PluginID]
			if p == nil {
				return plugins.ErrUnavailable
			}
			params, _ := json.Marshal(plugins.PaneRead{PaneID: request.PaneID, Directory: request.Directory})
			replies, err = p.Call(ctx, plugins.Call{OperationID: "pane:" + rand.Text(), Capability: "pane", Version: plugins.PaneCapability.Version, Method: "read", DeadlineUnixMS: time.Now().Add(30 * time.Second).UnixMilli(), Params: params})
			return err
		})
	}
	s.pluginMu.Unlock()
	if err != nil {
		return plugins.PaneTree{}, err
	}
	select {
	case <-ctx.Done():
		return plugins.PaneTree{}, ctx.Err()
	case reply := <-replies:
		if reply.Err != nil {
			return plugins.PaneTree{}, reply.Err
		}
		result := reply.Message.Result
		if reply.Message.Type != plugins.TypeResult || result == nil {
			return plugins.PaneTree{}, plugins.ErrInvalidMessage
		}
		if result.Error != nil {
			return plugins.PaneTree{}, result.Error
		}
		// A disable or grant revocation also denies a racing completed read.
		err = s.stateDB.WithPluginAuthorization(ctx, request.PluginID, func(d plugins.Description, grants []string) error {
			if !d.PaneAllowed(request.PaneID, grants) {
				return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
			}
			return nil
		})
		if err != nil {
			return plugins.PaneTree{}, err
		}
		return plugins.DecodePaneTree(result.Value)
	}
}
