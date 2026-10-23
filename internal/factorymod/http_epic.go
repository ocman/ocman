package factorymod

import (
	"net/http"
	"strconv"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

// Epic subroutes; Routes applies the method and localhost guards.

func (m *Module) handleGetEpic(w http.ResponseWriter, r *http.Request) {
	epic, err := m.svc.GetWorkEpic(r.Context(), r.PathValue("epic"))
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, epic)
}

func (m *Module) handleRemoveEpicProject(w http.ResponseWriter, r *http.Request) {
	if err := m.svc.RemoveWorkEpicProject(r.Context(), r.PathValue("epic"), r.URL.Query().Get("path")); err != nil {
		WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) handleListIssues(w http.ResponseWriter, r *http.Request) {
	issues, err := m.svc.ListIssues(r.Context(), r.PathValue("epic"))
	writeIssues(w, issues, err)
}

func (m *Module) handleListRemovedIssues(w http.ResponseWriter, r *http.Request) {
	issues, err := m.svc.ListRemovedIssues(r.Context(), r.PathValue("epic"))
	writeIssues(w, issues, err)
}

func writeIssues(w http.ResponseWriter, issues []factory.Issue, err error) {
	if err != nil {
		WriteError(w, err)
		return
	}
	if issues == nil {
		issues = []factory.Issue{}
	}
	httpx.WriteJSON(w, issues)
}

func (m *Module) handleListIssueComments(w http.ResponseWriter, r *http.Request) {
	comments, err := m.svc.ListIssueComments(r.Context(), r.PathValue("epic"), r.PathValue("issue"))
	if err != nil {
		WriteError(w, err)
		return
	}
	if comments == nil {
		comments = []factory.IssueComment{}
	}
	httpx.WriteJSON(w, comments)
}

func (m *Module) handleAddIssueComment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Body string `json:"body"`
	}
	if !decodeFactoryRequest(w, r, &request) {
		return
	}
	comment, err := m.svc.AddIssueComment(r.Context(), r.PathValue("epic"), r.PathValue("issue"), "user", request.Body)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, comment)
}

func (m *Module) handleDiscussIssue(w http.ResponseWriter, r *http.Request) {
	control, err := m.discussFactoryIssue(r.Context(), r.PathValue("epic"), r.PathValue("issue"))
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, control)
}

func (m *Module) handleReopenIssue(w http.ResponseWriter, r *http.Request) {
	writeNoContent(w, m.svc.ReopenIssue(r.Context(), r.PathValue("epic"), r.PathValue("issue")))
}

func (m *Module) handleMutateGraph(w http.ResponseWriter, r *http.Request) {
	var mutation factory.GraphMutation
	if !decodeFactoryRequest(w, r, &mutation) {
		return
	}
	mutation.EpicID = r.PathValue("epic")
	mutation.Actor = "user"
	writeNoContent(w, m.svc.MutateGraph(r.Context(), mutation))
}

func (m *Module) handlePour(w http.ResponseWriter, r *http.Request) {
	issues, err := m.svc.Pour(r.Context(), r.PathValue("epic"))
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, issues)
}

func (m *Module) handleDeliverAsIs(w http.ResponseWriter, r *http.Request) {
	leftOut, err := m.svc.DeliverAsIs(r.Context(), r.PathValue("epic"))
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, map[string]any{"leftOut": leftOut})
}

// handleCloseEpic closes a finished Epic; ?force=true stops it instead.
func (m *Module) handleCloseEpic(w http.ResponseWriter, r *http.Request) {
	force, err := strconv.ParseBool(r.URL.Query().Get("force"))
	if err != nil && r.URL.Query().Has("force") {
		http.Error(w, "invalid force value", http.StatusBadRequest)
		return
	}
	epicID := r.PathValue("epic")
	if force {
		writeNoContent(w, m.svc.StopEpic(r.Context(), epicID))
		return
	}
	writeNoContent(w, m.svc.CloseEpic(r.Context(), epicID, false))
}

func (m *Module) handleSetPaused(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeNoContent(w, m.svc.SetEpicPaused(r.Context(), r.PathValue("epic"), paused))
	}
}

func (m *Module) handleCloseMol(w http.ResponseWriter, r *http.Request) {
	writeNoContent(w, m.svc.CloseMol(r.Context(), r.PathValue("epic"), r.PathValue("mol")))
}

func writeNoContent(w http.ResponseWriter, err error) {
	if err != nil {
		WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) handleClaimPlan(w http.ResponseWriter, r *http.Request) {
	claimed, err := m.svc.ClaimPlan(r.Context(), r.PathValue("epic"), r.PathValue("issue"))
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, claimed)
}

func (m *Module) handleMaterialize(w http.ResponseWriter, r *http.Request) {
	materialization, err := m.svc.Materialize(r.Context(), r.PathValue("epic"), r.PathValue("issue"))
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, materialization)
}

func (m *Module) handleDecidePlanGate(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "approve" && action != "revise" && action != "reject" {
		http.Error(w, "invalid Plan gate action", http.StatusBadRequest)
		return
	}
	var req factory.PlanGateDecisionRequest
	if !decodeFactoryRequest(w, r, &req) {
		return
	}
	gate, err := m.svc.DecidePlanGate(r.Context(), r.PathValue("epic"), action, req)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, gate)
}

func (m *Module) handleListProposals(w http.ResponseWriter, r *http.Request) {
	proposals, err := m.svc.ListProposals(r.Context(), r.PathValue("epic"))
	if err != nil {
		WriteError(w, err)
		return
	}
	if proposals == nil {
		proposals = []factory.ProposalRevision{}
	}
	httpx.WriteJSON(w, proposals)
}

func (m *Module) handleSubmitProposal(w http.ResponseWriter, r *http.Request) {
	var req factory.SubmitProposalRequest
	if !decodeFactoryRequest(w, r, &req) {
		return
	}
	if req.AttemptID == "" || req.AttemptToken == "" {
		http.Error(w, "attemptId and attemptToken are required", http.StatusBadRequest)
		return
	}
	req.EpicID = r.PathValue("epic")
	proposal, err := m.svc.SubmitProposal(r.Context(), req)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, proposal)
}

func (m *Module) handleGetProposal(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.Atoi(r.PathValue("revision"))
	if err != nil || revision < 1 {
		http.Error(w, "invalid proposal revision", http.StatusBadRequest)
		return
	}
	proposal, err := m.svc.GetProposal(r.Context(), r.PathValue("epic"), revision)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, proposal)
}
