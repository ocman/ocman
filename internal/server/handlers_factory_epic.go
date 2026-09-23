package server

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory"
)

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
	if len(parts) == 2 && parts[1] == "projects" {
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", http.MethodDelete)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			service, ok := s.factory.(interface {
				RemoveWorkEpicProject(context.Context, string, string) error
			})
			if !ok {
				writeFactoryError(w, factory.ErrFactoryUnavailable)
				return
			}
			if err := service.RemoveWorkEpicProject(r.Context(), parts[0], r.URL.Query().Get("path")); err != nil {
				writeFactoryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
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
	if len(parts) == 4 && parts[1] == "issues" && parts[3] == "unblock" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			session, err := s.launchFactoryUnblockSession(r.Context(), parts[0], parts[2])
			if err != nil {
				writeFactoryError(w, err)
				return
			}
			writeJSONStatus(w, http.StatusCreated, session)
		})(w, r)
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
				CloseEpic(context.Context, string, bool) error
			})
			if !ok {
				writeFactoryError(w, factory.ErrFactoryUnavailable)
				return
			}
			force, err := strconv.ParseBool(r.URL.Query().Get("force"))
			if err != nil && r.URL.Query().Has("force") {
				http.Error(w, "invalid force value", http.StatusBadRequest)
				return
			}
			if err := closer.CloseEpic(r.Context(), parts[0], force); err != nil {
				writeFactoryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "models" && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) { s.handleFactoryEpicModels(w, r, parts[0]) })(w, r)
		return
	}
	if len(parts) == 2 && (parts[1] == "pause" || parts[1] == "resume") && r.Method == http.MethodPost {
		s.requireLocalhost(func(w http.ResponseWriter, r *http.Request) {
			lifecycle, ok := s.factory.(interface {
				SetEpicPaused(context.Context, string, bool) error
			})
			if !ok {
				writeFactoryError(w, factory.ErrFactoryUnavailable)
				return
			}
			if err := lifecycle.SetEpicPaused(r.Context(), parts[0], parts[1] == "pause"); err != nil {
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
