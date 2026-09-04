package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory"
)

func (s *Server) handleFactoryStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.factory.Status(r.Context()))
}

func (s *Server) handleFactoryQueue(w http.ResponseWriter, r *http.Request) {
	queueService, ok := s.factory.(interface {
		Queue(context.Context) ([]factory.DispatchItem, error)
	})
	if !ok {
		writeFactoryError(w, factory.ErrFactoryUnavailable)
		return
	}
	queue, err := queueService.Queue(r.Context())
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	if queue == nil {
		queue = []factory.DispatchItem{}
	}
	writeJSON(w, queue)
}

func (s *Server) handleFactoryRecoveryGate(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), "/api/factory/recovery-gates/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "resume" && parts[1] != "retry" && parts[1] != "cancel") {
		http.Error(w, "invalid recovery gate action", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Response string `json:"response"`
		}
		if !decodeFactoryRequest(w, r, &request) {
			return
		}
		gateID, err := url.PathUnescape(parts[0])
		if err != nil {
			http.Error(w, "invalid recovery gate ID", http.StatusBadRequest)
			return
		}
		gate, err := s.factory.ResolveRecoveryGate(r.Context(), gateID, parts[1], request.Response)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, gate)
	})(w, r)
}

func (s *Server) handleFactoryAuthorityGate(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), "/api/factory/authority-gates/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "approve" && parts[1] != "reject") {
		http.Error(w, "invalid authority gate action", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
		gateID, err := url.PathUnescape(parts[0])
		if err != nil {
			http.Error(w, "invalid authority gate ID", http.StatusBadRequest)
			return
		}
		gate, err := s.factory.ResolveAuthorityEscalationGate(r.Context(), gateID, parts[1])
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, gate)
	})(w, r)
}

func (s *Server) handleFactoryConfiguration(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		policy, err := s.factory.GetCapacityPolicy(r.Context())
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, policy)
	case http.MethodPost:
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			var policy factory.CapacityPolicy
			if !decodeFactoryRequest(w, r, &policy) {
				return
			}
			policy, err := s.factory.SetCapacityPolicy(r.Context(), policy)
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSON(w, policy)
		})(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFactoryEpics(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		epics, err := s.factory.ListWorkEpics(r.Context())
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		if epics == nil {
			epics = []factory.WorkEpic{}
		}
		writeJSON(w, epics)
	case http.MethodPost:
		s.requireLocalhost(s.handleFactoryEpicCreate)(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleFactoryEpic(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), "/api/factory/epics/"), "/"), "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			http.Error(w, "invalid Factory path", http.StatusBadRequest)
			return
		}
		parts[i] = decoded
	}
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "invalid epic ID", http.StatusBadRequest)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		epic, err := s.factory.GetWorkEpic(r.Context(), parts[0])
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, epic)
		return
	}
	if len(parts) == 2 && parts[1] == "issues" && r.Method == http.MethodGet {
		issues, err := s.factory.ListIssues(r.Context(), parts[0])
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		if issues == nil {
			issues = []factory.Issue{}
		}
		writeJSON(w, issues)
		return
	}
	if len(parts) == 4 && parts[1] == "issues" && parts[3] == "comments" {
		switch r.Method {
		case http.MethodGet:
			comments, err := s.factory.ListIssueComments(r.Context(), parts[0], parts[2])
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			if comments == nil {
				comments = []factory.IssueComment{}
			}
			writeJSON(w, comments)
		case http.MethodPost:
			s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Body string `json:"body"`
				}
				if !decodeFactoryRequest(w, r, &request) {
					return
				}
				comment, err := s.factory.AddIssueComment(r.Context(), parts[0], parts[2], "user", request.Body)
				if err != nil {
					writeFactoryError(w, err)
					return
				}
				writeJSONStatus(w, http.StatusCreated, comment)
			})(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "removed-issues" && r.Method == http.MethodGet {
		removed, ok := s.factory.(interface {
			ListRemovedIssues(context.Context, string) ([]factory.Issue, error)
		})
		if !ok {
			writeFactoryError(w, factory.ErrFactoryUnavailable)
			return
		}
		issues, err := removed.ListRemovedIssues(r.Context(), parts[0])
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		if issues == nil {
			issues = []factory.Issue{}
		}
		writeJSON(w, issues)
		return
	}
	if len(parts) == 2 && parts[1] == "mutations" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			mutator, ok := s.factory.(interface {
				MutateGraph(context.Context, factory.GraphMutation) error
			})
			if !ok {
				writeFactoryError(w, factory.ErrFactoryUnavailable)
				return
			}
			var mutation factory.GraphMutation
			if !decodeFactoryRequest(w, r, &mutation) {
				return
			}
			mutation.EpicID = parts[0]
			mutation.Actor = "user"
			if err := mutator.MutateGraph(r.Context(), mutation); err != nil {
				writeFactoryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "pour" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			issues, err := s.factory.Pour(r.Context(), parts[0])
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSONStatus(w, http.StatusCreated, issues)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "close" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			closer, ok := s.factory.(interface {
				CloseEpic(context.Context, string) error
			})
			if !ok {
				writeFactoryError(w, factory.ErrFactoryUnavailable)
				return
			}
			if err := closer.CloseEpic(r.Context(), parts[0]); err != nil {
				writeFactoryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 4 && parts[1] == "mols" && parts[3] == "close" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			closer, ok := s.factory.(interface {
				CloseMol(context.Context, string, string) error
			})
			if !ok {
				writeFactoryError(w, factory.ErrFactoryUnavailable)
				return
			}
			if err := closer.CloseMol(r.Context(), parts[0], parts[2]); err != nil {
				writeFactoryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 4 && parts[1] == "issues" && parts[3] == "reopen" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			reopener, ok := s.factory.(interface {
				ReopenIssue(context.Context, string, string) error
			})
			if !ok {
				writeFactoryError(w, factory.ErrFactoryUnavailable)
				return
			}
			if err := reopener.ReopenIssue(r.Context(), parts[0], parts[2]); err != nil {
				writeFactoryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 3 && parts[1] == "plans" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			claimed, err := s.factory.ClaimPlan(r.Context(), parts[0], parts[2])
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSONStatus(w, http.StatusCreated, claimed)
		})(w, r)
		return
	}
	if len(parts) == 3 && parts[1] == "materializations" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			materialization, err := s.factory.Materialize(r.Context(), parts[0], parts[2])
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSONStatus(w, http.StatusCreated, materialization)
		})(w, r)
		return
	}
	if len(parts) == 3 && parts[1] == "plan-gate" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			if parts[2] != "approve" && parts[2] != "revise" && parts[2] != "reject" {
				http.Error(w, "invalid Plan gate action", http.StatusBadRequest)
				return
			}
			var req factory.PlanGateDecisionRequest
			if !decodeFactoryRequest(w, r, &req) {
				return
			}
			gate, err := s.factory.DecidePlanGate(r.Context(), parts[0], parts[2], req)
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSON(w, gate)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "proposals" && r.Method == http.MethodGet {
		proposals, err := s.factory.ListProposals(r.Context(), parts[0])
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		if proposals == nil {
			proposals = []factory.ProposalRevision{}
		}
		writeJSON(w, proposals)
		return
	}
	if len(parts) == 2 && parts[1] == "proposals" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			var req factory.SubmitProposalRequest
			if !decodeFactoryRequest(w, r, &req) {
				return
			}
			if req.AttemptID == "" || req.AttemptToken == "" {
				http.Error(w, "attemptId and attemptToken are required", http.StatusBadRequest)
				return
			}
			req.EpicID = parts[0]
			proposal, err := s.factory.SubmitProposal(r.Context(), req)
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSONStatus(w, http.StatusCreated, proposal)
		})(w, r)
		return
	}
	if len(parts) == 3 && parts[1] == "proposals" && r.Method == http.MethodGet {
		revision, err := strconv.Atoi(parts[2])
		if err != nil || revision < 1 {
			http.Error(w, "invalid proposal revision", http.StatusBadRequest)
			return
		}
		proposal, err := s.factory.GetProposal(r.Context(), parts[0], revision)
		if err != nil {
			writeFactoryError(w, err)
			return
		}
		writeJSON(w, proposal)
		return
	}
	w.Header().Set("Allow", "GET, POST")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleFactoryEpicCreate(w http.ResponseWriter, r *http.Request) {
	var req factory.CreateWorkEpicRequest
	if !decodeFactoryRequest(w, r, &req) {
		return
	}
	epic, err := s.factory.CreateWorkEpic(r.Context(), req)
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusCreated, epic)
}

func (s *Server) handleFactoryFormulas(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/factory/formulas")
	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodGet:
			formulas, err := s.factory.ListFormulas(r.Context())
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			if formulas == nil {
				formulas = []factory.NativeFormulaView{}
			}
			writeJSON(w, formulas)
		case http.MethodPost:
			s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
				var req factory.FormulaSaveRequest
				if !decodeFactoryRequest(w, r, &req) {
					return
				}
				formula, err := s.factory.SaveFormula(r.Context(), req)
				if err != nil {
					writeFactoryError(w, err)
					return
				}
				writeJSONStatus(w, http.StatusCreated, formula)
			})(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if path == "/validate" || path == "/preview" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Source string `json:"source"`
				ID     string `json:"id"`
			}
			if !decodeFactoryRequest(w, r, &req) {
				return
			}
			var formula factory.NativeFormulaView
			var err error
			if path == "/validate" {
				formula, err = s.factory.ValidateFormula(r.Context(), req.Source, req.ID)
			} else {
				formula, err = s.factory.PreviewFormula(r.Context(), req.Source, req.ID)
			}
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSON(w, formula)
		})(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/api/factory/formulas/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.Error(w, "invalid Formula reference", http.StatusBadRequest)
		return
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil {
		http.Error(w, "invalid Formula reference", http.StatusBadRequest)
		return
	}
	version, err := strconv.Atoi(parts[1])
	if err != nil || version < 1 {
		http.Error(w, "invalid Formula revision", http.StatusBadRequest)
		return
	}
	formula, err := s.factory.GetFormula(r.Context(), id, version)
	if err != nil {
		writeFactoryError(w, err)
		return
	}
	writeJSON(w, formula)
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

func writeFactoryError(w http.ResponseWriter, err error) {
	if errors.Is(err, factory.ErrFormulaCorrupt) {
		serverError(w, "reading Factory Formula", err)
		return
	}
	if errors.Is(err, factory.ErrFactoryUnavailable) {
		http.Error(w, "factory is unavailable", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, factory.ErrFormulaNotFound) {
		http.Error(w, "factory Formula not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, factory.ErrWorkEpicNotFound) {
		http.Error(w, "factory epic not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, factory.ErrInstantiationConflict) {
		http.Error(w, "factory instantiation conflict", http.StatusConflict)
		return
	}
	if errors.Is(err, factory.ErrActionNotPermitted) {
		http.Error(w, "factory action is not permitted", http.StatusForbidden)
		return
	}
	if errors.Is(err, factory.ErrProjectNotLocalGit) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, factory.ErrInvalidRequest) || errors.Is(err, factory.ErrAcknowledgementRequired) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, factory.ErrInvalidFormula) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	serverError(w, "handling Factory request", err)
}
