package remotesmod

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/ocman/ocman/internal/httpx"
)

// Tokens are accepted on write but never returned; the list view shows
// health/counts only (FR-14).

// accessStatus is the GET /api/settings/remote-access response. The token
// itself is never included; the explicit reveal action handles that (AD-5).
type accessStatus struct {
	InstanceID string `json:"instanceId"`
	Listening  bool   `json:"listening"`
	ListenAddr string `json:"listenAddr"`
	TLS        bool   `json:"tls"`
	Transport  string `json:"transport"`
	TokenSet   bool   `json:"tokenSet"`
}

func (m *Module) handleAccess(w http.ResponseWriter, r *http.Request) {
	var a Access
	if m.d.Access != nil {
		a = m.d.Access()
	}
	status := accessStatus{InstanceID: a.InstanceID, Listening: a.Listening, ListenAddr: a.ListenAddr, TLS: a.TLS}
	if status.Listening {
		status.Transport = "trusted-overlay"
		if status.TLS {
			status.Transport = "tls"
		}
	}
	if m.d.Identity != nil {
		if ident, err := m.d.Identity.InstanceIdentity(r.Context()); err == nil {
			if status.InstanceID == "" {
				status.InstanceID = ident.InstanceID
			}
			status.TokenSet = ident.RemoteToken != ""
		}
	}
	httpx.WriteJSON(w, status)
}

// handleRevealToken returns this instance's own plaintext token for
// copy-to-clipboard. It never returns tokens stored for attached remotes
// (AD-5, NFR-4).
func (m *Module) handleRevealToken(w http.ResponseWriter, r *http.Request) {
	if m.d.Identity == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	ident, err := m.d.Identity.InstanceIdentity(r.Context())
	if err != nil {
		httpx.ServerError(w, "reading instance identity", err)
		return
	}
	httpx.WriteJSON(w, map[string]string{"token": ident.RemoteToken})
}

func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
	mgr := m.manager()
	if mgr == nil {
		// Single-host: an empty list keeps the settings page clean.
		httpx.WriteJSON(w, []any{})
		return
	}
	list, err := mgr.List(r.Context())
	if err != nil {
		httpx.ServerError(w, "listing remotes", err)
		return
	}
	if list == nil {
		httpx.WriteJSON(w, []any{})
		return
	}
	httpx.WriteJSON(w, list)
}

// available answers 503 and returns nil without remote management.
func (m *Module) available(w http.ResponseWriter) Manager {
	mgr := m.manager()
	if mgr == nil {
		http.Error(w, "remote management not available", http.StatusServiceUnavailable)
	}
	return mgr
}

// remote resolves the manager and the {id} path value of /api/remotes/{id}.
func (m *Module) remote(w http.ResponseWriter, r *http.Request) (Manager, int64, bool) {
	mgr := m.available(w)
	if mgr == nil {
		return nil, 0, false
	}
	localID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid remote id", http.StatusBadRequest)
		return nil, 0, false
	}
	return mgr, localID, true
}

func (m *Module) handleAdd(w http.ResponseWriter, r *http.Request) {
	mgr := m.available(w)
	if mgr == nil {
		return
	}
	var req struct {
		Address     string `json:"address"`
		Token       string `json:"token"`
		DisplayName string `json:"displayName"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	req.Token = strings.TrimSpace(req.Token)
	if req.Address == "" || req.Token == "" {
		http.Error(w, "address and token are required", http.StatusBadRequest)
		return
	}
	id, err := mgr.Add(r.Context(), req.Address, req.Token, strings.TrimSpace(req.DisplayName))
	if err != nil {
		httpx.ServerError(w, "adding remote", err)
		return
	}
	// The dial runs in the background; the new row starts "connecting".
	list, _ := mgr.List(r.Context())
	for _, st := range list {
		if st.LocalID == id {
			httpx.WriteJSON(w, st)
			return
		}
	}
	httpx.WriteJSON(w, map[string]int64{"localId": id})
}

func (m *Module) handleReconnect(w http.ResponseWriter, r *http.Request) {
	mgr, localID, ok := m.remote(w, r)
	if !ok {
		return
	}
	if err := mgr.Reconnect(r.Context(), localID); err != nil {
		httpx.ServerError(w, "reconnecting remote", err)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"ok": true})
}

func (m *Module) handleRemove(w http.ResponseWriter, r *http.Request) {
	mgr, localID, ok := m.remote(w, r)
	if !ok {
		return
	}
	if err := mgr.Remove(r.Context(), localID); err != nil {
		httpx.ServerError(w, "removing remote", err)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"ok": true})
}

func (m *Module) handleUpdate(w http.ResponseWriter, r *http.Request) {
	mgr, localID, ok := m.remote(w, r)
	if !ok {
		return
	}
	var req struct {
		DisplayName string  `json:"displayName"`
		Address     string  `json:"address"`
		Enabled     bool    `json:"enabled"`
		Token       *string `json:"token"` // nil = leave unchanged
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	if req.Address == "" {
		http.Error(w, "address is required", http.StatusBadRequest)
		return
	}
	var token *string
	if req.Token != nil {
		if t := strings.TrimSpace(*req.Token); t != "" {
			token = &t
		}
	}
	if err := mgr.Update(r.Context(), localID, strings.TrimSpace(req.DisplayName), req.Address, req.Enabled, token); err != nil {
		httpx.ServerError(w, "updating remote", err)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"ok": true})
}
