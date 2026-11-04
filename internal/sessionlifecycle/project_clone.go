package sessionlifecycle

import (
	"net/http"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/httpx"
)

// HandlePlanProjectClone validates and suggests a destination without writing anything.
func (m *Module) HandlePlanProjectClone(w http.ResponseWriter, r *http.Request) {
	var req struct {
		hostsvc.ProjectClone
		RemoteID string `json:"remoteId"`
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	if req.RemoteID == "" {
		http.Error(w, "remoteId is required", http.StatusBadRequest)
		return
	}
	host, ok := m.d.Owner(w, req.Directory, req.RemoteID)
	if !ok {
		return
	}
	plan, err := host.PlanProjectClone(r.Context(), req.ProjectClone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	httpx.WriteJSON(w, plan)
}

func (m *Module) cloneForStart(w http.ResponseWriter, r *http.Request, target Target) bool {
	if target.CloneURL == "" {
		return true
	}
	host, ok := m.d.Owner(w, target.Directory, target.RemoteID)
	if !ok {
		return false
	}
	hostsvc.ReportProgress(r.Context(), "clone", hostsvc.StepActive)
	err := host.CloneProject(r.Context(), hostsvc.ProjectClone{URL: target.CloneURL, Directory: target.Directory})
	hostsvc.FinishStep(r.Context(), "clone", err)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return false
	}
	return true
}
