package factorymod

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

func (m *Module) handleFactoryStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, m.svc.Status(r.Context()))
}

func (m *Module) handleFactoryQueue(w http.ResponseWriter, r *http.Request) {
	queue, err := m.svc.Queue(r.Context())
	if err != nil {
		WriteError(w, err)
		return
	}
	if queue == nil {
		queue = []factory.DispatchItem{}
	}
	httpx.WriteJSON(w, queue)
}

// Gate actions are validated before the localhost guard, so a malformed
// action is a 400 wherever it comes from.
func (m *Module) handleFactoryRecoveryGate(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "resume" && action != "retry" && action != "cancel" {
		http.Error(w, "invalid recovery gate action", http.StatusBadRequest)
		return
	}
	m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Response string `json:"response"`
		}
		if !decodeFactoryRequest(w, r, &request) {
			return
		}
		gate, err := m.svc.ResolveRecoveryGate(r.Context(), r.PathValue("gate"), action, request.Response)
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, gate)
	})(w, r)
}

func (m *Module) handleFactoryAuthorityGate(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "approve" && action != "reject" {
		http.Error(w, "invalid authority gate action", http.StatusBadRequest)
		return
	}
	m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
		gate, err := m.svc.ResolveAuthorityEscalationGate(r.Context(), r.PathValue("gate"), action)
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, gate)
	})(w, r)
}

func (m *Module) handleFactoryProjectGate(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "approve" && action != "reject" {
		http.Error(w, "invalid project gate action", http.StatusBadRequest)
		return
	}
	m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Response                  string `json:"response"`
			AcknowledgeLocalExecution bool   `json:"acknowledgeLocalExecution"`
		}
		if !decodeFactoryRequest(w, r, &request) {
			return
		}
		gate, err := m.svc.ResolveProjectRequest(r.Context(), r.PathValue("gate"), action, request.Response, request.AcknowledgeLocalExecution)
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, gate)
	})(w, r)
}

func (m *Module) handleGetConfiguration(w http.ResponseWriter, r *http.Request) {
	policy, err := m.svc.GetCapacityPolicy(r.Context())
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, policy)
}

func (m *Module) handleSetConfiguration(w http.ResponseWriter, r *http.Request) {
	var policy factory.CapacityPolicy
	if !decodeFactoryRequest(w, r, &policy) {
		return
	}
	policy, err := m.svc.SetCapacityPolicy(r.Context(), policy)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, policy)
}

func (m *Module) handleListEpics(w http.ResponseWriter, r *http.Request) {
	epics, err := m.svc.ListWorkEpics(r.Context())
	if err != nil {
		WriteError(w, err)
		return
	}
	if epics == nil {
		epics = []factory.WorkEpic{}
	}
	httpx.WriteJSON(w, epics)
}

func (m *Module) handleFactoryEpicCreate(w http.ResponseWriter, r *http.Request) {
	var req factory.CreateWorkEpicRequest
	if !decodeFactoryRequest(w, r, &req) {
		return
	}
	epic, err := m.svc.CreateWorkEpic(r.Context(), req)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, epic)
}

func (m *Module) handleListFormulas(w http.ResponseWriter, r *http.Request) {
	formulas, err := m.svc.ListFormulas(r.Context())
	if err != nil {
		WriteError(w, err)
		return
	}
	if formulas == nil {
		formulas = []factory.NativeFormulaView{}
	}
	httpx.WriteJSON(w, formulas)
}

func (m *Module) handleSaveFormula(w http.ResponseWriter, r *http.Request) {
	var req factory.FormulaSaveRequest
	if !decodeFactoryRequest(w, r, &req) {
		return
	}
	formula, err := m.svc.SaveFormula(r.Context(), req)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSONStatus(w, http.StatusCreated, formula)
}

// checkFormula serves POST formulas/validate and formulas/preview.
func checkFormula(check func(context.Context, string, string) (factory.NativeFormulaView, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Source string `json:"source"`
			ID     string `json:"id"`
		}
		if !decodeFactoryRequest(w, r, &req) {
			return
		}
		formula, err := check(r.Context(), req.Source, req.ID)
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, formula)
	}
}

func (m *Module) handleGetFormula(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		http.Error(w, "invalid Formula revision", http.StatusBadRequest)
		return
	}
	formula, err := m.svc.GetFormula(r.Context(), r.PathValue("formula"), version)
	if err != nil {
		WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, formula)
}

func decodeFactoryRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, httpx.MaxRequestBody))
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

// WriteError maps a Factory error to its HTTP status; the server's
// permission pipeline answers Factory escalations the same way.
func WriteError(w http.ResponseWriter, err error) {
	if errors.Is(err, model.ErrRecoveryWorkspaceBusy) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if errors.Is(err, factory.ErrFormulaCorrupt) {
		httpx.ServerError(w, "reading Factory Formula", err)
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
	if errors.Is(err, factory.ErrEpicProjectNotFound) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if errors.Is(err, factory.ErrInstantiationConflict) {
		http.Error(w, "factory instantiation conflict", http.StatusConflict)
		return
	}
	if errors.Is(err, factory.ErrEpicIDTaken) {
		http.Error(w, err.Error(), http.StatusConflict)
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
	if errors.Is(err, factory.ErrEpicProjectPermanent) || errors.Is(err, factory.ErrEpicProjectHistory) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if errors.Is(err, factory.ErrInvalidFormula) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, model.ErrEpicClosureBlocked) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	httpx.ServerError(w, "handling Factory request", err)
}
