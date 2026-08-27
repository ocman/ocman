package server

import (
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
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
