package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory"
)

func (s *Server) handleFactoryStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.factory.Status(r.Context()))
}

func (s *Server) handleFactoryEpics(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		requireGET(s.handleFactoryEpicList)(w, r)
	case http.MethodPost:
		requirePOST(s.requireLocalhost(s.handleFactoryEpicCreate))(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFactoryEpicList(w http.ResponseWriter, r *http.Request) {
	epics, err := s.factory.ListWorkEpics(r.Context())
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	if epics == nil {
		epics = []factory.WorkEpic{}
	}
	writeJSON(w, epics)
}

func (s *Server) handleFactoryEpic(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/factory/epics/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "invalid epic ID", http.StatusBadRequest)
		return
	}
	epic, err := s.factory.GetWorkEpic(r.Context(), id)
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	writeJSON(w, epic)
}

func (s *Server) handleFactoryEpicCreate(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	var req factory.CreateWorkEpicRequest
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid request: body must contain one JSON object", http.StatusBadRequest)
		return
	}
	epic, err := s.factory.CreateWorkEpic(r.Context(), req)
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, epic)
}

func writeFactoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, factory.ErrWorkEpicNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, factory.ErrInstantiationConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, factory.ErrFactoryUnavailable):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, factory.ErrBeadsFailure):
		http.Error(w, err.Error(), http.StatusBadGateway)
	case errors.Is(err, factory.ErrFormulaNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, factory.ErrFormulaReferenced), errors.Is(err, factory.ErrBuiltInFormulaImmutable):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

func (s *Server) handleFactoryFormulas(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		formulas, err := s.factory.ListFormulas(r.Context())
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		if formulas == nil {
			formulas = []factory.FormulaSummary{}
		}
		writeJSON(w, formulas)
	case http.MethodPost:
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			var req factory.SaveFormulaRequest
			if !decodeFactoryRequest(w, r, &req) {
				return
			}
			revision, err := s.factory.SaveFormula(r.Context(), req)
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSONStatus(w, http.StatusCreated, revision)
		})(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFactoryFormulaCopy(w http.ResponseWriter, r *http.Request) {
	requirePOST(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID       string `json:"id"`
			Revision int    `json:"revision"`
		}
		if !decodeFactoryRequest(w, r, &req) {
			return
		}
		draft, err := s.factory.CopyFormula(r.Context(), req.ID, req.Revision)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, draft)
	})(w, r)
}

func (s *Server) handleFactoryFormulaValidate(w http.ResponseWriter, r *http.Request) {
	requirePOST(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DefinitionYAML string `json:"definitionYaml"`
		}
		if !decodeFactoryRequest(w, r, &req) {
			return
		}
		writeJSON(w, s.factory.ValidateFormula(req.DefinitionYAML))
	})(w, r)
}

func (s *Server) handleFactoryFormulaPreview(w http.ResponseWriter, r *http.Request) {
	requirePOST(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DefinitionYAML string            `json:"definitionYaml"`
			Parameters     map[string]string `json:"parameters"`
		}
		if !decodeFactoryRequest(w, r, &req) {
			return
		}
		preview, err := s.factory.PreviewFormula(req.DefinitionYAML, req.Parameters)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, preview)
	})(w, r)
}

func (s *Server) handleFactoryFormulaArchive(w http.ResponseWriter, r *http.Request) {
	s.handleFactoryFormulaIDAction(w, r, s.factory.ArchiveFormula)
}

func (s *Server) handleFactoryFormulaDelete(w http.ResponseWriter, r *http.Request) {
	s.handleFactoryFormulaIDAction(w, r, s.factory.DeleteFormula)
}

func (s *Server) handleFactoryFormulaIDAction(w http.ResponseWriter, r *http.Request, action func(context.Context, string) error) {
	requirePOST(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		if !decodeFactoryRequest(w, r, &req) {
			return
		}
		if err := action(r.Context(), req.ID); err != nil {
			writeFactoryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})(w, r)
}

func decodeFactoryRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid request: body must contain one JSON object", http.StatusBadRequest)
		return false
	}
	return true
}
