package factorymod

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
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/httpx"
)

func (m *Module) handleFactoryStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, m.svc.Status(r.Context()))
}

func (m *Module) handleFactoryQueue(w http.ResponseWriter, r *http.Request) {
	queueService, ok := m.svc.(interface {
		Queue(context.Context) ([]factory.DispatchItem, error)
	})
	if !ok {
		WriteError(w, factory.ErrFactoryUnavailable)
		return
	}
	queue, err := queueService.Queue(r.Context())
	if err != nil {
		WriteError(w, err)
		return
	}
	if queue == nil {
		queue = []factory.DispatchItem{}
	}
	httpx.WriteJSON(w, queue)
}

func (m *Module) handleFactoryRecoveryGate(w http.ResponseWriter, r *http.Request) {
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
	m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
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
		gate, err := m.svc.ResolveRecoveryGate(r.Context(), gateID, parts[1], request.Response)
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, gate)
	})(w, r)
}

func (m *Module) handleFactoryAuthorityGate(w http.ResponseWriter, r *http.Request) {
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
	m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
		gateID, err := url.PathUnescape(parts[0])
		if err != nil {
			http.Error(w, "invalid authority gate ID", http.StatusBadRequest)
			return
		}
		gate, err := m.svc.ResolveAuthorityEscalationGate(r.Context(), gateID, parts[1])
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, gate)
	})(w, r)
}

func (m *Module) handleFactoryProjectGate(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.EscapedPath(), "/api/factory/project-gates/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || (parts[1] != "approve" && parts[1] != "reject") {
		http.Error(w, "invalid project gate action", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
		service, ok := m.svc.(interface {
			ResolveProjectRequest(context.Context, string, string, string, bool) (factory.ProjectRequestGate, error)
		})
		if !ok {
			WriteError(w, factory.ErrFactoryUnavailable)
			return
		}
		var request struct {
			Response                  string `json:"response"`
			AcknowledgeLocalExecution bool   `json:"acknowledgeLocalExecution"`
		}
		if !decodeFactoryRequest(w, r, &request) {
			return
		}
		gateID, err := url.PathUnescape(parts[0])
		if err != nil {
			http.Error(w, "invalid project gate ID", http.StatusBadRequest)
			return
		}
		gate, err := service.ResolveProjectRequest(r.Context(), gateID, parts[1], request.Response, request.AcknowledgeLocalExecution)
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, gate)
	})(w, r)
}

func (m *Module) handleFactoryConfiguration(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		policy, err := m.svc.GetCapacityPolicy(r.Context())
		if err != nil {
			WriteError(w, err)
			return
		}
		httpx.WriteJSON(w, policy)
	case http.MethodPost:
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
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
		})(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *Module) handleFactoryEpics(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		epics, err := m.svc.ListWorkEpics(r.Context())
		if err != nil {
			WriteError(w, err)
			return
		}
		if epics == nil {
			epics = []factory.WorkEpic{}
		}
		httpx.WriteJSON(w, epics)
	case http.MethodPost:
		m.deps.Guards.Localhost(m.handleFactoryEpicCreate)(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
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

func (m *Module) handleFactoryFormulas(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/factory/formulas")
	if path == "" || path == "/" {
		switch r.Method {
		case http.MethodGet:
			formulas, err := m.svc.ListFormulas(r.Context())
			if err != nil {
				WriteError(w, err)
				return
			}
			if formulas == nil {
				formulas = []factory.NativeFormulaView{}
			}
			httpx.WriteJSON(w, formulas)
		case http.MethodPost:
			m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
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
		m.deps.Guards.Localhost(func(w http.ResponseWriter, r *http.Request) {
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
				formula, err = m.svc.ValidateFormula(r.Context(), req.Source, req.ID)
			} else {
				formula, err = m.svc.PreviewFormula(r.Context(), req.Source, req.ID)
			}
			if err != nil {
				WriteError(w, err)
				return
			}
			httpx.WriteJSON(w, formula)
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
	formula, err := m.svc.GetFormula(r.Context(), id, version)
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
