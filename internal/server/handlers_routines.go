package server

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/state"
)

type routineRequest struct {
	Name               string                 `json:"name"`
	Prompt             string                 `json:"prompt"`
	Directory          string                 `json:"directory"`
	RemoteID           string                 `json:"remoteId"`
	Agent              string                 `json:"agent"`
	Model              string                 `json:"model"`
	SessionMode        string                 `json:"sessionMode"`
	SessionID          string                 `json:"sessionId"`
	Schedule           routineScheduleRequest `json:"schedule"`
	Enabled            bool                   `json:"enabled"`
	DeleteAfterSuccess bool                   `json:"deleteAfterSuccess"`
}

type routineScheduleRequest struct {
	Kind      string `json:"kind"`
	TimeoutMS int64  `json:"timeoutMs"`
	At        int64  `json:"at"`
	Cron      string `json:"cron"`
	Timezone  string `json:"timezone"`
}

func (req routineRequest) input() (routines.Input, error) {
	if req.Schedule.TimeoutMS > math.MaxInt64/int64(time.Millisecond) || req.Schedule.TimeoutMS < math.MinInt64/int64(time.Millisecond) {
		return routines.Input{}, routines.ErrValidation
	}
	return routines.Input{
		Name: req.Name, Prompt: req.Prompt, Directory: req.Directory, RemoteID: req.RemoteID, Agent: req.Agent, Model: req.Model, SessionMode: req.SessionMode, SessionID: req.SessionID,
		Schedule: routines.Schedule{
			Kind: req.Schedule.Kind, Timeout: time.Duration(req.Schedule.TimeoutMS) * time.Millisecond,
			At: time.UnixMilli(req.Schedule.At), Cron: req.Schedule.Cron, Timezone: req.Schedule.Timezone,
		},
		Enabled: req.Enabled, DeleteAfterSuccess: req.DeleteAfterSuccess,
	}, nil
}

func (s *Server) handleRoutines(w http.ResponseWriter, r *http.Request) {
	if s.routineSvc == nil {
		http.Error(w, "state database unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/routines"), "/")
	if rest == "" {
		s.handleRoutineCollection(w, r)
		return
	}
	id, action, extra := cutRoutinePath(rest)
	if id == "" || extra != "" {
		http.NotFound(w, r)
		return
	}

	switch action {
	case "":
		s.handleRoutineItem(w, r, id)
	case "run":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		run, err := s.routineSvc.RunNow(r.Context(), id)
		if err != nil {
			s.writeRoutineError(w, "running routine", err)
			return
		}
		writeJSON(w, run)
	case "history":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		runs, err := s.routineSvc.History(r.Context(), id)
		if err != nil {
			s.writeRoutineError(w, "listing routine history", err)
			return
		}
		if runs == nil {
			runs = []state.RoutineRun{}
		}
		writeJSON(w, runs)
	default:
		http.NotFound(w, r)
	}
}

func cutRoutinePath(path string) (first, second, rest string) {
	parts := strings.SplitN(path, "/", 3)
	first = parts[0]
	if len(parts) > 1 {
		second = parts[1]
	}
	if len(parts) > 2 {
		rest = parts[2]
	}
	return
}

func (s *Server) handleRoutineCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.routineSvc.List(r.Context(), false)
		if err != nil {
			s.writeRoutineError(w, "listing routines", err)
			return
		}
		if items == nil {
			items = []state.Routine{}
		}
		writeJSON(w, items)
	case http.MethodPost:
		input, ok := readRoutineInput(w, r)
		if !ok {
			return
		}
		item, err := s.routineSvc.Create(r.Context(), input)
		if err != nil {
			s.writeRoutineError(w, "creating routine", err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, item)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRoutineItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		item, err := s.routineSvc.Get(r.Context(), id)
		if err != nil {
			s.writeRoutineError(w, "getting routine", err)
			return
		}
		writeJSON(w, item)
	case http.MethodPut:
		input, ok := readRoutineInput(w, r)
		if !ok {
			return
		}
		item, err := s.routineSvc.Update(r.Context(), id, input)
		if err != nil {
			s.writeRoutineError(w, "updating routine", err)
			return
		}
		writeJSON(w, item)
	case http.MethodDelete:
		if err := s.routineSvc.Delete(r.Context(), id); err != nil {
			s.writeRoutineError(w, "deleting routine", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func readRoutineInput(w http.ResponseWriter, r *http.Request) (routines.Input, bool) {
	var req routineRequest
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return routines.Input{}, false
	}
	input, err := req.input()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return routines.Input{}, false
	}
	return input, true
}

func (s *Server) writeRoutineError(w http.ResponseWriter, operation string, err error) {
	switch {
	case errors.Is(err, routines.ErrValidation):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, state.ErrRoutineNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, routines.ErrNameConflict):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, state.ErrRoutineRunActive):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		serverError(w, operation, err)
	}
}
