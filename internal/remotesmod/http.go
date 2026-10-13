package remotesmod

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/httpx"
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

// handleRemotes serves GET (list) and POST (add) on /api/remotes.
func (m *Module) handleRemotes(w http.ResponseWriter, r *http.Request) {
	mgr := m.manager()
	if mgr == nil {
		// Single-host: an empty list keeps the settings page clean.
		if r.Method == http.MethodGet {
			httpx.WriteJSON(w, []any{})
			return
		}
		http.Error(w, "remote management not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
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
	case http.MethodPost:
		handleAdd(w, r, mgr)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleAdd(w http.ResponseWriter, r *http.Request, mgr Manager) {
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

// handleRemoteByID serves /api/remotes/{localId}: PUT edits, DELETE
// removes, POST .../reconnect reconnects.
func (m *Module) handleRemoteByID(w http.ResponseWriter, r *http.Request) {
	mgr := m.manager()
	if mgr == nil {
		http.Error(w, "remote management not available", http.StatusServiceUnavailable)
		return
	}
	idStr, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/remotes/"), "/")
	localID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid remote id", http.StatusBadRequest)
		return
	}
	switch action {
	case "reconnect":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := mgr.Reconnect(r.Context(), localID); err != nil {
			httpx.ServerError(w, "reconnecting remote", err)
			return
		}
		httpx.WriteJSON(w, map[string]bool{"ok": true})
	case "":
		switch r.Method {
		case http.MethodPut:
			handleUpdate(w, r, mgr, localID)
		case http.MethodDelete:
			if err := mgr.Remove(r.Context(), localID); err != nil {
				httpx.ServerError(w, "removing remote", err)
				return
			}
			httpx.WriteJSON(w, map[string]bool{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func handleUpdate(w http.ResponseWriter, r *http.Request, mgr Manager, localID int64) {
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
