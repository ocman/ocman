package factorymod

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

// handleFactoryEpicModels handles POST /api/factory/epics/<id>/models.
func (m *Module) handleFactoryEpicModels(w http.ResponseWriter, r *http.Request, epicID string) {
	setter, ok := m.svc.(interface {
		SetEpicModels(context.Context, string, model.EpicModels) (model.EpicModels, error)
	})
	if !ok {
		WriteError(w, factory.ErrFactoryUnavailable)
		return
	}
	var req model.EpicModels
	if !decodeFactoryRequest(w, r, &req) {
		return
	}
	models, err := setter.SetEpicModels(r.Context(), epicID, req)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, models)
}
