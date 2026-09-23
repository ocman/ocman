package server

import (
	"context"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// handleFactoryEpicModels handles POST /api/factory/epics/<id>/models.
func (s *Server) handleFactoryEpicModels(w http.ResponseWriter, r *http.Request, epicID string) {
	setter, ok := s.factory.(interface {
		SetEpicModels(context.Context, string, model.EpicModels) (model.EpicModels, error)
	})
	if !ok {
		writeFactoryError(w, factory.ErrFactoryUnavailable)
		return
	}
	var req model.EpicModels
	if !decodeFactoryRequest(w, r, &req) {
		return
	}
	models, err := setter.SetEpicModels(r.Context(), epicID, req)
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	writeJSON(w, models)
}
