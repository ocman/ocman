// Package connectionsmod owns the owner-routed, authenticated connection API.
package connectionsmod

import (
	"errors"
	"net/http"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Deps struct {
	// ResolveOwner must fail closed for a disconnected explicit remote.
	ResolveOwner func(*http.Request) (hostsvc.Host, error)
}
type Module struct{ d Deps }

func NewModule(d Deps) *Module { return &Module{d: d} }
func (m *Module) Name() string { return "platform-connections" }
func (m *Module) Routes(r module.Router) {
	r.Handle("GET /api/platform-connections", m.list)
	r.Local("POST /api/platform-connections/check", m.check)
	r.Local("POST /api/platform-connections", m.save)
	r.Local("PATCH /api/platform-connections/{id}", m.update)
	r.Local("DELETE /api/platform-connections/{id}", m.remove)
	r.Local("DELETE /api/platform-connections/setup/{id}", m.cancel)
	r.Local("POST /api/platform-connections/onboarding", m.onboarding)
}
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, platformconnections.ErrInvalid), status.Code(err) == codes.InvalidArgument:
		http.Error(w, "invalid connection request", http.StatusBadRequest)
	case errors.Is(err, platformconnections.ErrNotFound), status.Code(err) == codes.NotFound:
		http.Error(w, "connection not found", http.StatusNotFound)
	case errors.Is(err, platformconnections.ErrUnavailable), errors.Is(err, platformconnections.ErrLimit), status.Code(err) == codes.FailedPrecondition:
		http.Error(w, "connection unavailable or still in use", http.StatusConflict)
	case errors.Is(err, platforms.ErrUnsupported), status.Code(err) == codes.Unimplemented:
		http.Error(w, "connection management unsupported", http.StatusNotImplemented)
	case errors.Is(err, platforms.ErrRemoteOffline), status.Code(err) == codes.Unavailable:
		http.Error(w, "owner disconnected", http.StatusServiceUnavailable)
	default:
		httpx.ServerError(w, "platform connection operation failed", err)
	}
}
func (m *Module) owner(w http.ResponseWriter, r *http.Request) hostsvc.ConnectionHost {
	if r.URL.Query().Get("remoteId") == "" {
		http.Error(w, "remoteId is required", http.StatusBadRequest)
		return nil
	}
	if m.d.ResolveOwner == nil {
		http.Error(w, "owner unavailable", http.StatusServiceUnavailable)
		return nil
	}
	h, err := m.d.ResolveOwner(r)
	if err != nil {
		writeError(w, err)
		return nil
	}
	c, ok := h.(hostsvc.ConnectionHost)
	if !ok {
		writeError(w, platforms.ErrUnsupported)
		return nil
	}
	return c
}
func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	h := m.owner(w, r)
	if h == nil {
		return
	}
	v, err := h.InventoryPlatformConnections(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.WriteJSON(w, v)
}
func (m *Module) check(w http.ResponseWriter, r *http.Request) {
	h := m.owner(w, r)
	if h == nil {
		return
	}
	var req hostsvc.ConnectionCheckRequest
	if !httpx.ReadJSON(w, r, 4096, &req) {
		return
	}
	v, err := h.CheckPlatformConnection(r.Context(), req)
	if err != nil {
		// A receipt survives a failed probe/preparation. Return it without exposing
		// native error text, so retry cannot allocate a different configuration.
		if v.ReservationID != "" {
			httpx.WriteJSONStatus(w, http.StatusConflict, map[string]any{"reservationId": v.ReservationID, "error": "Setup check failed. Retry with this reservation."})
			return
		}
		writeError(w, err)
		return
	}
	httpx.WriteJSON(w, v)
}
func (m *Module) save(w http.ResponseWriter, r *http.Request) {
	h := m.owner(w, r)
	if h == nil {
		return
	}
	var req struct {
		ReservationID string `json:"reservationId"`
		Name          string `json:"name"`
		Enabled       bool   `json:"enabled"`
	}
	if !httpx.ReadJSON(w, r, 4096, &req) {
		return
	}
	if req.ReservationID == "" {
		writeError(w, platformconnections.ErrInvalid)
		return
	}
	_, err := h.SavePlatformConnection(r.Context(), req.ReservationID, req.Name, req.Enabled)
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"saved": true})
}
func (m *Module) update(w http.ResponseWriter, r *http.Request) {
	h := m.owner(w, r)
	if h == nil {
		return
	}
	var req hostsvc.ConnectionUpdate
	if !httpx.ReadJSON(w, r, 4096, &req) {
		return
	}
	req.ID = r.PathValue("id")
	if err := h.UpdatePlatformConnection(r.Context(), req); err != nil {
		writeError(w, err)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"saved": true})
}
func (m *Module) remove(w http.ResponseWriter, r *http.Request) {
	h := m.owner(w, r)
	if h == nil {
		return
	}
	if err := h.RemovePlatformConnection(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"removed": true})
}
func (m *Module) cancel(w http.ResponseWriter, r *http.Request) {
	h := m.owner(w, r)
	if h == nil {
		return
	}
	if err := h.CancelPlatformConnectionSetup(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"canceled": true})
}
func (m *Module) onboarding(w http.ResponseWriter, r *http.Request) {
	h := m.owner(w, r)
	if h == nil {
		return
	}
	if err := h.CompletePlatformConnectionOnboarding(r.Context()); err != nil {
		writeError(w, err)
		return
	}
	httpx.WriteJSON(w, map[string]bool{"saved": true})
}
