package factorymod

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

// handleFactoryEpicModels handles POST /api/factory/epics/{epic}/models.
func (m *Module) handleFactoryEpicModels(w http.ResponseWriter, r *http.Request) {
	var req model.EpicModels
	if !decodeFactoryRequest(w, r, &req) {
		return
	}
	models, err := m.svc.SetEpicModels(r.Context(), r.PathValue("epic"), req)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, models)
}
