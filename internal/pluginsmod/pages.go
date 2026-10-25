package pluginsmod

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
	"net/http"
	"time"
)

type listedPluginPage struct {
	PluginID    string                 `json:"pluginId"`
	PluginLabel string                 `json:"pluginLabel"`
	OwnerID     string                 `json:"ownerId"`
	Page        plugins.PageDescriptor `json:"page"`
}

func pluginPageDeclarations(registrations []state.PluginRegistration, owner string) []listedPluginPage {
	pages := []listedPluginPage{}
	for _, p := range registrations {
		if !p.Enabled || p.Removed || p.Health.Status == "conflict" {
			continue
		}
		for _, page := range p.Description.Pages {
			if p.Description.PageAllowed(page.ID, p.Grants) {
				pages = append(pages, listedPluginPage{PluginID: p.Description.ID, PluginLabel: p.Description.Name, OwnerID: owner, Page: page})
			}
		}
	}
	return pages
}

func (m *Module) handlePluginPages(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("ownerId")
	if owner == "" {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrInvalidMessage))
		return
	}
	response := m.RouteOperation(r.Context(), owner, remote.PluginRequest{Operation: "catalog"})
	if response.Error != nil {
		writePluginResponse(w, response)
		return
	}
	var registrations []state.PluginRegistration
	if json.Unmarshal(response.Value, &registrations) != nil {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrUnavailable))
		return
	}
	writePluginResponse(w, pluginResponse(pluginPageDeclarations(registrations, owner), nil))
}

func (m *Module) handlePluginPage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OwnerID  string              `json:"ownerId"`
		PluginID string              `json:"pluginId"`
		Method   string              `json:"method"`
		Request  plugins.PageRequest `json:"request"`
	}
	if !httpx.ReadJSON(w, r, plugins.MaxMessageBytes, &input) {
		return
	}
	if input.OwnerID == "" || (input.Method != "read" && input.Method != "save") {
		writePluginResponse(w, pluginResponse(nil, plugins.ErrInvalidMessage))
		return
	}
	raw, _ := json.Marshal(input.Request)
	writePluginResponse(w, m.RouteOperation(r.Context(), input.OwnerID, remote.PluginRequest{Operation: "page-" + input.Method, PluginID: input.PluginID, Input: remote.PluginInput{Values: map[string]json.RawMessage{"page": raw}}}))
}

func (m *Module) localPluginPage(ctx context.Context, id, method string, raw json.RawMessage) (plugins.PageDocument, error) {
	if method != "read" && method != "save" {
		return plugins.PageDocument{}, plugins.ErrInvalidMessage
	}
	if method == "save" {
		if doc, handled, err := m.validatePluginPageSave(ctx, id, raw); handled || err != nil {
			return doc, err
		}
	}
	return m.dispatchPluginPage(ctx, id, method, raw)
}

func (m *Module) dispatchPluginPage(ctx context.Context, id, method string, raw json.RawMessage) (plugins.PageDocument, error) {
	req, err := plugins.DecodePageRequest(raw)
	if err != nil {
		return plugins.PageDocument{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var replies <-chan plugins.Reply
	var version plugins.Version
	m.mu.Lock()
	if m.store == nil {
		err = plugins.ErrUnavailable
	} else {
		err = m.store.WithPluginAuthorization(ctx, id, func(d plugins.Description, grants []string) error {
			if !d.PageAllowed(req.PageID, grants) || !d.PageRequestAllowed(req) {
				return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
			}
			version = d.PageVersion()
			if err := plugins.ValidatePageCall(method, req, version); err != nil {
				return err
			}
			if req.ProjectID != "" && m.deps.ProjectDirectory == nil {
				return plugins.ErrUnavailable
			}
			if req.ProjectID != "" {
				if directory, e := m.deps.ProjectDirectory(ctx, req.ProjectID); e != nil || directory == "" {
					return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
				}
			}
			p := m.processes[id]
			if p == nil {
				return plugins.ErrUnavailable
			}
			replies, err = p.Call(ctx, plugins.Call{OperationID: "page:" + rand.Text(), Capability: "page", Version: version, Method: method, DeadlineUnixMS: time.Now().Add(30 * time.Second).UnixMilli(), Params: raw})
			return err
		})
	}
	m.mu.Unlock()
	if err != nil {
		return plugins.PageDocument{}, err
	}
	select {
	case <-ctx.Done():
		return plugins.PageDocument{}, ctx.Err()
	case reply := <-replies:
		if reply.Err != nil {
			return plugins.PageDocument{}, reply.Err
		}
		result := reply.Message.Result
		if result == nil {
			return plugins.PageDocument{}, plugins.ErrInvalidMessage
		}
		if result.Error != nil {
			return plugins.PageDocument{}, result.Error
		}
		err = m.store.WithPluginAuthorization(ctx, id, func(d plugins.Description, g []string) error {
			if !d.PageAllowed(req.PageID, g) || !d.PageRequestAllowed(req) {
				return &plugins.WireError{Category: plugins.ErrorPermissionDenied}
			}
			return nil
		})
		if err != nil {
			return plugins.PageDocument{}, err
		}
		doc, err := plugins.DecodePageDocument(result.Value)
		if err == nil {
			err = plugins.ValidatePageVersion(doc, version)
		}
		return doc, err
	}
}
