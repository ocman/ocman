package factorymod

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

func (m *Module) handleFactoryEpic(w http.ResponseWriter, r *http.Request) {
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
		epic, err := m.svc.GetWorkEpic(r.Context(), parts[0])
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, epic)
		return
	}
	if len(parts) == 2 && parts[1] == "usage" && r.Method == http.MethodGet {
		m.handleFactoryUsage(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "projects" {
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", http.MethodDelete)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			service, ok := m.svc.(interface {
				RemoveWorkEpicProject(context.Context, string, string) error
			})
			if !ok {
				WriteError(w, factory.ErrFactoryUnavailable)
				return
			}
			if err := service.RemoveWorkEpicProject(r.Context(), parts[0], r.URL.Query().Get("path")); err != nil {
				WriteError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "issues" && r.Method == http.MethodGet {
		issues, err := m.svc.ListIssues(r.Context(), parts[0])
		if err != nil {
			WriteError(w, err)
			return
		}
		if issues == nil {
			issues = []factory.Issue{}
		}
		httpx.WriteJSON(w, issues)
		return
	}
	if len(parts) == 4 && parts[1] == "issues" && parts[3] == "comments" {
		switch r.Method {
		case http.MethodGet:
			comments, err := m.svc.ListIssueComments(r.Context(), parts[0], parts[2])
			if err != nil {
				WriteError(w, err)
				return
			}
			if comments == nil {
				comments = []factory.IssueComment{}
			}
			httpx.WriteJSON(w, comments)
		case http.MethodPost:
			m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Body string `json:"body"`
				}
				if !decodeFactoryRequest(w, r, &request) {
					return
				}
				comment, err := m.svc.AddIssueComment(r.Context(), parts[0], parts[2], "user", request.Body)
				if err != nil {
					WriteError(w, err)
					return
				}
				httpx.WriteJSONStatus(w, http.StatusCreated, comment)
			})(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 4 && parts[1] == "issues" && parts[3] == "discuss" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			control, err := m.discussFactoryIssue(r.Context(), parts[0], parts[2])
			if err != nil {
				WriteError(w, err)
				return
			}
			httpx.WriteJSONStatus(w, http.StatusCreated, control)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "control" {
		m.handleFactoryControl(w, r, parts[0])
		return
	}
	if (len(parts) == 3 || len(parts) == 4) && parts[1] == "control-actions" {
		verb := ""
		if len(parts) == 4 {
			verb = parts[3]
		}
		m.handleFactoryControlAction(w, r, parts[0], parts[2], verb)
		return
	}
	if len(parts) == 4 && parts[1] == "issues" && parts[3] == "transfer" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) { m.handleFactoryIssueTransfer(w, r, parts[0], parts[2]) })(w, r)
		return
	}
	if len(parts) == 5 && parts[1] == "issues" && parts[3] == "transfer" && parts[4] == "cancel" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			m.handleFactoryIssueTransferCancel(w, r, parts[0], parts[2])
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "removed-issues" && r.Method == http.MethodGet {
		removed, ok := m.svc.(interface {
			ListRemovedIssues(context.Context, string) ([]factory.Issue, error)
		})
		if !ok {
			WriteError(w, factory.ErrFactoryUnavailable)
			return
		}
		issues, err := removed.ListRemovedIssues(r.Context(), parts[0])
		if err != nil {
			WriteError(w, err)
			return
		}
		if issues == nil {
			issues = []factory.Issue{}
		}
		httpx.WriteJSON(w, issues)
		return
	}
	if len(parts) == 2 && parts[1] == "mutations" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			mutator, ok := m.svc.(interface {
				MutateGraph(context.Context, factory.GraphMutation) error
			})
			if !ok {
				WriteError(w, factory.ErrFactoryUnavailable)
				return
			}
			var mutation factory.GraphMutation
			if !decodeFactoryRequest(w, r, &mutation) {
				return
			}
			mutation.EpicID = parts[0]
			mutation.Actor = "user"
			if err := mutator.MutateGraph(r.Context(), mutation); err != nil {
				WriteError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "pour" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			issues, err := m.svc.Pour(r.Context(), parts[0])
			if err != nil {
				WriteError(w, err)
				return
			}
			httpx.WriteJSONStatus(w, http.StatusCreated, issues)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "deliver-as-is" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			deliverer, ok := m.svc.(interface {
				DeliverAsIs(context.Context, string) ([]string, error)
			})
			if !ok {
				WriteError(w, factory.ErrFactoryUnavailable)
				return
			}
			leftOut, err := deliverer.DeliverAsIs(r.Context(), parts[0])
			if err != nil {
				WriteError(w, err)
				return
			}
			httpx.WriteJSON(w, map[string]any{"leftOut": leftOut})
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "close" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			closer, ok := m.svc.(interface {
				CloseEpic(context.Context, string, bool) error
			})
			if !ok {
				WriteError(w, factory.ErrFactoryUnavailable)
				return
			}
			force, err := strconv.ParseBool(r.URL.Query().Get("force"))
			if err != nil && r.URL.Query().Has("force") {
				http.Error(w, "invalid force value", http.StatusBadRequest)
				return
			}
			closeEpic := func() error { return closer.CloseEpic(r.Context(), parts[0], false) }
			if force {
				stopper, ok := m.svc.(interface {
					StopEpic(context.Context, string) error
				})
				if !ok {
					WriteError(w, factory.ErrFactoryUnavailable)
					return
				}
				closeEpic = func() error { return stopper.StopEpic(r.Context(), parts[0]) }
			}
			if err := closeEpic(); err != nil {
				WriteError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "models" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) { m.handleFactoryEpicModels(w, r, parts[0]) })(w, r)
		return
	}
	if len(parts) == 2 && (parts[1] == "pause" || parts[1] == "resume") && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			lifecycle, ok := m.svc.(interface {
				SetEpicPaused(context.Context, string, bool) error
			})
			if !ok {
				WriteError(w, factory.ErrFactoryUnavailable)
				return
			}
			if err := lifecycle.SetEpicPaused(r.Context(), parts[0], parts[1] == "pause"); err != nil {
				WriteError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 4 && parts[1] == "mols" && parts[3] == "close" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			closer, ok := m.svc.(interface {
				CloseMol(context.Context, string, string) error
			})
			if !ok {
				WriteError(w, factory.ErrFactoryUnavailable)
				return
			}
			if err := closer.CloseMol(r.Context(), parts[0], parts[2]); err != nil {
				WriteError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 4 && parts[1] == "issues" && parts[3] == "reopen" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			reopener, ok := m.svc.(interface {
				ReopenIssue(context.Context, string, string) error
			})
			if !ok {
				WriteError(w, factory.ErrFactoryUnavailable)
				return
			}
			if err := reopener.ReopenIssue(r.Context(), parts[0], parts[2]); err != nil {
				WriteError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})(w, r)
		return
	}
	if len(parts) == 3 && parts[1] == "plans" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			claimed, err := m.svc.ClaimPlan(r.Context(), parts[0], parts[2])
			if err != nil {
				WriteError(w, err)
				return
			}
			httpx.WriteJSONStatus(w, http.StatusCreated, claimed)
		})(w, r)
		return
	}
	if len(parts) == 3 && parts[1] == "materializations" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			materialization, err := m.svc.Materialize(r.Context(), parts[0], parts[2])
			if err != nil {
				WriteError(w, err)
				return
			}
			httpx.WriteJSONStatus(w, http.StatusCreated, materialization)
		})(w, r)
		return
	}
	if len(parts) == 3 && parts[1] == "plan-gate" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			if parts[2] != "approve" && parts[2] != "revise" && parts[2] != "reject" {
				http.Error(w, "invalid Plan gate action", http.StatusBadRequest)
				return
			}
			var req factory.PlanGateDecisionRequest
			if !decodeFactoryRequest(w, r, &req) {
				return
			}
			gate, err := m.svc.DecidePlanGate(r.Context(), parts[0], parts[2], req)
			if err != nil {
				WriteError(w, err)
				return
			}
			httpx.WriteJSON(w, gate)
		})(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "proposals" && r.Method == http.MethodGet {
		proposals, err := m.svc.ListProposals(r.Context(), parts[0])
		if err != nil {
			WriteError(w, err)
			return
		}
		if proposals == nil {
			proposals = []factory.ProposalRevision{}
		}
		httpx.WriteJSON(w, proposals)
		return
	}
	if len(parts) == 2 && parts[1] == "proposals" && r.Method == http.MethodPost {
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
			var req factory.SubmitProposalRequest
			if !decodeFactoryRequest(w, r, &req) {
				return
			}
			if req.AttemptID == "" || req.AttemptToken == "" {
				http.Error(w, "attemptId and attemptToken are required", http.StatusBadRequest)
				return
			}
			req.EpicID = parts[0]
			proposal, err := m.svc.SubmitProposal(r.Context(), req)
			if err != nil {
				WriteError(w, err)
				return
			}
			httpx.WriteJSONStatus(w, http.StatusCreated, proposal)
		})(w, r)
		return
	}
	if len(parts) == 3 && parts[1] == "proposals" && r.Method == http.MethodGet {
		revision, err := strconv.Atoi(parts[2])
		if err != nil || revision < 1 {
			http.Error(w, "invalid proposal revision", http.StatusBadRequest)
			return
		}
		proposal, err := m.svc.GetProposal(r.Context(), parts[0], revision)
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, proposal)
		return
	}
	w.Header().Set("Allow", "GET, POST")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}
