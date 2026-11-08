package sessionlifecycle

import (
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
)

// HandleOneOffDirectory allocates a workspace without launching an agent.
// The composer then uses the normal durable draft and first-submit lifecycle.
func (m *Module) HandleOneOffDirectory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RemoteID string `json:"remoteId"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if req.RemoteID == "" {
		http.Error(w, "remoteId is required", http.StatusBadRequest)
		return
	}
	host, ok := m.d.Owner(w, "", req.RemoteID)
	if !ok {
		return
	}
	directory, err := host.CreateOneOffDirectory(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	httpx.WriteJSON(w, struct {
		Directory string `json:"directory"`
	}{directory})
}
