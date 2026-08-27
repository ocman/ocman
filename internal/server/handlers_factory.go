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
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/factory/epics/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "invalid epic ID", http.StatusBadRequest)
		return
	}
	if len(parts) == 1 {
		requireGET(func(w http.ResponseWriter, r *http.Request) { s.handleFactoryEpicGet(w, r, parts[0]) })(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "plan" {
		requireGET(func(w http.ResponseWriter, r *http.Request) { s.handleFactoryPlanGet(w, r, parts[0]) })(w, r)
		return
	}
	mutation := s.requireLocalhost(requirePOST(func(w http.ResponseWriter, r *http.Request) {
		s.handleFactoryPlanMutation(w, r, parts)
	}))
	mutation(w, r)
}

func (s *Server) handleFactoryEpicGet(w http.ResponseWriter, r *http.Request, id string) {
	epic, err := s.factory.GetWorkEpic(r.Context(), id)
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	writeJSON(w, epic)
}

func (s *Server) handleFactoryPlanGet(w http.ResponseWriter, r *http.Request, id string) {
	plan, err := s.factory.GetPlan(r.Context(), id)
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	writeJSON(w, plan)
}

func (s *Server) handleFactoryPlanMutation(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 3 && parts[1] == "plan" && parts[2] == "mutate" {
		var req factory.MutatePlanRequest
		if !decodeFactoryRequest(w, r, &req) {
			return
		}
		result, err := s.factory.MutatePlan(r.Context(), parts[0], req)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		if result.Stale {
			writeFactoryPlanConflict(w, result.Plan)
			return
		}
		writeJSON(w, result)
		return
	}
	if len(parts) == 2 && parts[1] == "planning" {
		var req factory.AddPlanningWorkRequest
		if !decodeFactoryRequest(w, r, &req) {
			return
		}
		result, err := s.factory.AddPlanningWork(r.Context(), parts[0], req)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		if result.Stale {
			writeFactoryPlanConflict(w, result.Plan)
			return
		}
		writeJSONStatus(w, http.StatusCreated, result)
		return
	}
	if len(parts) == 4 && parts[1] == "planning" && parts[3] == "complete" {
		var req factory.CompletePlanningWorkRequest
		if !decodeFactoryRequest(w, r, &req) {
			return
		}
		req.Actor = "operator"
		plan, err := s.factory.CompletePlanningWork(r.Context(), parts[0], parts[2], req)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, plan)
		return
	}
	if len(parts) == 3 && parts[1] == "plan" {
		var req factory.PlanDecisionRequest
		if !decodeFactoryRequest(w, r, &req) {
			return
		}
		req.Actor = "operator"
		var plan factory.Plan
		var err error
		switch parts[2] {
		case "approve":
			plan, err = s.factory.ApprovePlan(r.Context(), parts[0], req)
		case "revise":
			plan, err = s.factory.RevisePlan(r.Context(), parts[0], req)
		case "reject":
			plan, err = s.factory.RejectPlan(r.Context(), parts[0], req)
		case "cancel":
			plan, err = s.factory.CancelPlan(r.Context(), parts[0], req)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, plan)
		return
	}
	http.NotFound(w, r)
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
	var conflict *factory.PlanConflictError
	if errors.As(err, &conflict) {
		writeFactoryPlanConflict(w, conflict.Current)
		return
	}
	switch {
	case errors.Is(err, factory.ErrWorkEpicNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, factory.ErrInstantiationConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, factory.ErrPlanNotApprovable):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, factory.ErrFactoryUnavailable):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, factory.ErrBeadsFailure):
		http.Error(w, err.Error(), http.StatusBadGateway)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

func writeFactoryPlanConflict(w http.ResponseWriter, plan factory.Plan) {
	writeJSONStatus(w, http.StatusConflict, struct {
		Error   string       `json:"error"`
		Current factory.Plan `json:"current"`
	}{Error: (&factory.PlanConflictError{Current: plan}).Error(), Current: plan})
}
