package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type fakeFactoryService struct {
	status       factory.Status
	epics        []factory.WorkEpic
	createReq    factory.CreateWorkEpicRequest
	createErr    error
	listErr      error
	getErr       error
	plan         factory.Plan
	mutation     factory.PlanMutationResult
	decision     factory.PlanDecisionRequest
	formulaErr   error
	savedFormula factory.SaveFormulaRequest
}

type factoryProjectHost struct {
	hostsvc.Host
	id   string
	root string
}

func (h factoryProjectHost) RemoteID() string { return h.id }
func (h factoryProjectHost) ProjectUpstreams(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
	return &hostsvc.ProjectUpstreams{RepoRoot: h.root}, nil
}

func (f *fakeFactoryService) ListFormulas(context.Context) ([]factory.FormulaSummary, error) {
	return []factory.FormulaSummary{{ID: factory.DefaultFormulaID, Name: "Shipped delivery", Origin: factory.FormulaOriginBuiltIn, CurrentRevision: 1}}, nil
}
func (f *fakeFactoryService) CopyFormula(context.Context, string, int) (factory.FormulaDraft, error) {
	return factory.FormulaDraft{DefinitionYAML: "schema: 1\n"}, nil
}
func (f *fakeFactoryService) ValidateFormula(string) factory.FormulaValidation {
	return factory.FormulaValidation{Valid: true, Schema: 1, Errors: []string{}}
}
func (f *fakeFactoryService) PreviewFormula(string, map[string]string) (factory.FormulaPreview, error) {
	return factory.FormulaPreview{Name: "Preview"}, nil
}
func (f *fakeFactoryService) SaveFormula(_ context.Context, req factory.SaveFormulaRequest) (factory.FormulaRevision, error) {
	f.savedFormula = req
	return factory.FormulaRevision{FormulaSummary: factory.FormulaSummary{ID: req.ID, Name: req.Name}, Revision: 1}, f.formulaErr
}
func (f *fakeFactoryService) ArchiveFormula(context.Context, string) error { return nil }
func (f *fakeFactoryService) DeleteFormula(context.Context, string) error  { return f.formulaErr }

func (f *fakeFactoryService) Start(context.Context) error           { return nil }
func (f *fakeFactoryService) Close()                                {}
func (f *fakeFactoryService) Status(context.Context) factory.Status { return f.status }
func (f *fakeFactoryService) CreateWorkEpic(_ context.Context, req factory.CreateWorkEpicRequest) (factory.WorkEpic, error) {
	f.createReq = req
	if !req.AcknowledgeLocalExecution && f.createErr == nil {
		return factory.WorkEpic{}, errors.New("local non-isolated execution must be acknowledged")
	}
	if f.createErr != nil {
		return factory.WorkEpic{}, f.createErr
	}
	return f.epics[0], nil
}
func (f *fakeFactoryService) ListWorkEpics(context.Context) ([]factory.WorkEpic, error) {
	return f.epics, f.listErr
}
func (f *fakeFactoryService) GetWorkEpic(_ context.Context, id string) (factory.WorkEpic, error) {
	if f.getErr != nil {
		return factory.WorkEpic{}, f.getErr
	}
	for _, epic := range f.epics {
		if epic.ID == id {
			return epic, nil
		}
	}
	return factory.WorkEpic{}, factory.ErrWorkEpicNotFound
}
func (f *fakeFactoryService) GetPlan(context.Context, string) (factory.Plan, error) {
	return f.plan, f.getErr
}
func (f *fakeFactoryService) MutatePlan(_ context.Context, _ string, req factory.MutatePlanRequest) (factory.PlanMutationResult, error) {
	f.mutation.Plan.Draft = req.Graph
	return f.mutation, f.createErr
}
func (f *fakeFactoryService) AddPlanningWork(context.Context, string, factory.AddPlanningWorkRequest) (factory.PlanMutationResult, error) {
	return f.mutation, f.createErr
}
func (f *fakeFactoryService) CompletePlanningWork(context.Context, string, string, factory.CompletePlanningWorkRequest) (factory.Plan, error) {
	return f.plan, f.createErr
}
func (f *fakeFactoryService) ApprovePlan(_ context.Context, _ string, req factory.PlanDecisionRequest) (factory.Plan, error) {
	f.decision = req
	return f.plan, f.createErr
}
func (f *fakeFactoryService) RevisePlan(_ context.Context, _ string, req factory.PlanDecisionRequest) (factory.Plan, error) {
	f.decision = req
	return f.plan, f.createErr
}
func (f *fakeFactoryService) RejectPlan(_ context.Context, _ string, req factory.PlanDecisionRequest) (factory.Plan, error) {
	f.decision = req
	return f.plan, f.createErr
}
func (f *fakeFactoryService) CancelPlan(_ context.Context, _ string, req factory.PlanDecisionRequest) (factory.Plan, error) {
	f.decision = req
	return f.plan, f.createErr
}

func TestFactoryProjectResolverAcceptsOnlyLocalOwner(t *testing.T) {
	local := factoryProjectHost{id: "local", root: "/local/repo"}
	remote := factoryProjectHost{id: "remote-1", root: "/remote/repo"}
	router := hostsvc.NewRouter(local)
	router.RegisterRemote("remote-1", remote)
	srv := &Server{hostRouter: router}
	resolver := factoryProjectResolver{server: srv}

	root, err := resolver.ResolveLocalProject(context.Background(), "/local/repo/subdir")
	if err != nil || root != "/local/repo" {
		t.Fatalf("local project = %q, %v", root, err)
	}
	router.SetDirResolver(func(string) string { return "remote-1" })
	if _, err := resolver.ResolveLocalProject(context.Background(), "/remote/repo"); err == nil {
		t.Fatal("remote Factory project unexpectedly accepted")
	}
}

func TestFactoryStatusRouteIsAuthenticatedAndReadOnly(t *testing.T) {
	auth := newTestAuth(t, "hunter2")
	want := factory.Status{
		Health: factory.HealthHealthy, Idle: true, ReadOnly: true,
		Dispatch: []factory.DispatchItem{{ID: "work-1", EpicID: "fac-1", Title: "Build", Repository: "/repo", State: factory.DispatchCompleted, AttemptID: "attempt-1", Outcome: "succeeded"}},
		Beads:    factory.BeadsHealth{Usable: true, Version: "1.1.0", ContractVersion: 1},
	}
	srv := New(nil, nil, "", nil, auth)
	srv.factory = &fakeFactoryService{status: want}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/factory/status", nil)
	request.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, request)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", rr.Code)
	}

	cookieWriter := httptest.NewRecorder()
	auth.issueCookie(cookieWriter, httptest.NewRequest(http.MethodGet, "/", nil))
	request = httptest.NewRequest(http.MethodGet, "/api/factory/status", nil)
	request.RemoteAddr = "10.0.0.5:1234"
	request.AddCookie(cookieWriter.Result().Cookies()[0])
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request)
	if rr.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	var got factory.Status
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %#v, want %#v", got, want)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/factory/status", nil)
	request.RemoteAddr = "10.0.0.5:1234"
	request.AddCookie(cookieWriter.Result().Cookies()[0])
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, request)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", rr.Code)
	}
}

func TestFactoryEpicRoutesAuthMethodsAndLocalhostProtection(t *testing.T) {
	auth := newTestAuth(t, "hunter2")
	srv := New(nil, nil, "", nil, auth)
	svc := &fakeFactoryService{epics: []factory.WorkEpic{{ID: "fac-1", Goal: "Ship it"}}}
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/factory/epics", nil)
	request.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, request)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d, want 401", rr.Code)
	}

	cookieWriter := httptest.NewRecorder()
	auth.issueCookie(cookieWriter, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := cookieWriter.Result().Cookies()[0]
	for _, tt := range []struct {
		name, method, path, remote, origin string
		want                               int
	}{
		{name: "remote create", method: http.MethodPost, path: "/api/factory/epics", remote: "10.0.0.5:1234", want: http.StatusForbidden},
		{name: "foreign origin", method: http.MethodPost, path: "/api/factory/epics", remote: "127.0.0.1:1234", origin: "https://evil.example", want: http.StatusForbidden},
		{name: "unsupported collection method", method: http.MethodPut, path: "/api/factory/epics", remote: "127.0.0.1:1234", want: http.StatusMethodNotAllowed},
		{name: "detail is read only", method: http.MethodPost, path: "/api/factory/epics/fac-1", remote: "127.0.0.1:1234", want: http.StatusMethodNotAllowed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{}`))
			req.RemoteAddr = tt.remote
			req.AddCookie(cookie)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestFactoryEpicCreateListAndGet(t *testing.T) {
	epic := factory.WorkEpic{ID: "fac-1", Goal: "Ship it", InitialProject: "/repo", InstantiationID: "request-1"}
	svc := &fakeFactoryService{epics: []factory.WorkEpic{epic}}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	create := httptest.NewRequest(http.MethodPost, "/api/factory/epics", strings.NewReader(`{"instantiationId":"request-1","goal":"Ship it","initialProject":"/repo","acknowledgeLocalExecution":true}`))
	create.RemoteAddr = "127.0.0.1:1234"
	created := httptest.NewRecorder()
	mux.ServeHTTP(created, create)
	if created.Code != http.StatusCreated || svc.createReq != (factory.CreateWorkEpicRequest{InstantiationID: "request-1", Goal: "Ship it", InitialProject: "/repo", AcknowledgeLocalExecution: true}) {
		t.Fatalf("create = %d, req %#v: %s", created.Code, svc.createReq, created.Body.String())
	}

	for _, path := range []string{"/api/factory/epics", "/api/factory/epics/fac-1"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"fac-1"`) {
			t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}

	for name, body := range map[string]string{
		"false acknowledgement": `{"instantiationId":"request-1","goal":"Ship it","initialProject":"/repo","acknowledgeLocalExecution":false}`,
		"extra field":           `{"acknowledgeLocalExecution":true,"extra":true}`,
		"multiple values":       `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/factory/epics", strings.NewReader(body))
			req.RemoteAddr = "127.0.0.1:1234"
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestFactoryEpicErrorMapping(t *testing.T) {
	for _, tt := range []struct {
		name, method string
		err          error
		want         int
	}{
		{name: "missing", method: http.MethodGet, err: factory.ErrWorkEpicNotFound, want: http.StatusNotFound},
		{name: "conflict", method: http.MethodPost, err: factory.ErrInstantiationConflict, want: http.StatusConflict},
		{name: "unavailable", method: http.MethodPost, err: factory.ErrFactoryUnavailable, want: http.StatusServiceUnavailable},
		{name: "beads failure", method: http.MethodPost, err: factory.ErrBeadsFailure, want: http.StatusBadGateway},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(nil, nil, "", nil, nil)
			srv.factory = &fakeFactoryService{createErr: tt.err, getErr: tt.err}
			mux, err := srv.routes()
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			path := "/api/factory/epics/fac-1"
			body := strings.NewReader("")
			if tt.method == http.MethodPost {
				path = "/api/factory/epics"
				body = strings.NewReader(`{"instantiationId":"request-1","goal":"Ship it","initialProject":"/repo","acknowledgeLocalExecution":true}`)
			}
			req := httptest.NewRequest(tt.method, path, body)
			req.RemoteAddr = "127.0.0.1:1234"
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
func TestFactoryPlanRoutesExposeReadsAndProtectedMutations(t *testing.T) {
	svc := &fakeFactoryService{plan: factory.Plan{Revision: 2, Hash: "hash-2", State: factory.PlanDraft}, mutation: factory.PlanMutationResult{Plan: factory.Plan{Revision: 3}}}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	read := httptest.NewRecorder()
	mux.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/factory/epics/fac-1/plan", nil))
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"revision":2`) {
		t.Fatalf("GET plan = %d: %s", read.Code, read.Body.String())
	}

	remote := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/plan/approve", strings.NewReader(`{"expectedRevision":2,"expectedHash":"hash-2","actor":"dries"}`))
	remote.RemoteAddr = "10.0.0.5:1234"
	blocked := httptest.NewRecorder()
	mux.ServeHTTP(blocked, remote)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("remote approval = %d, want 403", blocked.Code)
	}

	local := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/plan/approve", strings.NewReader(`{"expectedRevision":2,"expectedHash":"hash-2","actor":"dries"}`))
	local.RemoteAddr = "127.0.0.1:1234"
	approved := httptest.NewRecorder()
	mux.ServeHTTP(approved, local)
	if approved.Code != http.StatusOK || svc.decision.ExpectedRevision != 2 || svc.decision.ExpectedHash != "hash-2" {
		t.Fatalf("approval = %d, req=%#v: %s", approved.Code, svc.decision, approved.Body.String())
	}
}

func TestFactoryPlanMutationRoutes(t *testing.T) {
	svc := &fakeFactoryService{
		plan:     factory.Plan{Revision: 3, Hash: "hash-3", State: factory.PlanDraft},
		mutation: factory.PlanMutationResult{Plan: factory.Plan{Revision: 3}},
	}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name, path, body string
		want             int
	}{
		{name: "add planning", path: "/api/factory/epics/fac-1/planning", body: `{"expectedRevision":2,"target":{"id":"api","hostId":"local","repository":"/repo","deliveryBase":{}},"acknowledgeLocalExecution":true}`, want: http.StatusCreated},
		{name: "complete planning", path: "/api/factory/epics/fac-1/planning/fac-1.1/complete", body: `{"expectedRevision":2,"expectedHash":"hash-2"}`, want: http.StatusOK},
		{name: "revise", path: "/api/factory/epics/fac-1/plan/revise", body: `{"expectedRevision":2,"expectedHash":"hash-2"}`, want: http.StatusOK},
		{name: "reject", path: "/api/factory/epics/fac-1/plan/reject", body: `{"expectedRevision":2,"expectedHash":"hash-2"}`, want: http.StatusOK},
		{name: "cancel", path: "/api/factory/epics/fac-1/plan/cancel", body: `{"expectedRevision":2,"expectedHash":"hash-2"}`, want: http.StatusOK},
		{name: "unknown decision", path: "/api/factory/epics/fac-1/plan/nope", body: `{}`, want: http.StatusNotFound},
		{name: "unknown mutation", path: "/api/factory/epics/fac-1/nope", body: `{}`, want: http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.RemoteAddr = "127.0.0.1:1234"
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
	if svc.decision.Actor != "operator" {
		t.Fatalf("decision actor = %q, want operator", svc.decision.Actor)
	}
}

func TestFactoryPlanStaleMutationReturnsCurrentGraph(t *testing.T) {
	svc := &fakeFactoryService{mutation: factory.PlanMutationResult{Stale: true, Plan: factory.Plan{Revision: 5, Hash: "current"}}}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/plan/mutate", strings.NewReader(`{"expectedRevision":4,"graph":{"intent":"stale","targets":[],"items":[],"dependencies":[]}}`))
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"stale":true`) || !strings.Contains(rec.Body.String(), `"plan":{"schemaVersion"`) || !strings.Contains(rec.Body.String(), `"revision":5`) {
		t.Fatalf("stale mutation = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFactoryPlanCASConflictsReturnCurrentPlan(t *testing.T) {
	current := factory.Plan{Revision: 5, Hash: "current", State: factory.PlanDraft, Draft: factory.PlanGraph{Intent: "current graph"}}
	svc := &fakeFactoryService{createErr: &factory.PlanConflictError{Current: current}}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ path, body string }{
		{path: "/api/factory/epics/fac-1/plan/mutate", body: `{"expectedRevision":4,"graph":{"intent":"stale"}}`},
		{path: "/api/factory/epics/fac-1/planning", body: `{"expectedRevision":4,"target":{"id":"api"}}`},
		{path: "/api/factory/epics/fac-1/planning/fac-1.1/complete", body: `{"expectedRevision":4,"expectedHash":"stale"}`},
		{path: "/api/factory/epics/fac-1/plan/approve", body: `{"expectedRevision":4,"expectedHash":"stale"}`},
		{path: "/api/factory/epics/fac-1/plan/revise", body: `{"expectedRevision":4,"expectedHash":"stale"}`},
		{path: "/api/factory/epics/fac-1/plan/reject", body: `{"expectedRevision":4,"expectedHash":"stale"}`},
		{path: "/api/factory/epics/fac-1/plan/cancel", body: `{"expectedRevision":4,"expectedHash":"stale"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"stale":true`) || !strings.Contains(rec.Body.String(), `"plan":{"schemaVersion"`) || !strings.Contains(rec.Body.String(), `"revision":5`) || !strings.Contains(rec.Body.String(), `"intent":"current graph"`) {
			t.Fatalf("POST %s = %d: %s", tt.path, rec.Code, rec.Body.String())
		}
	}
}

func TestFactoryFormulaRoutesExposeDraftValidationPreviewAndProtectedPersistence(t *testing.T) {
	svc := &fakeFactoryService{}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{
		"/api/factory/formulas/copy":     `{"id":"ocman/default","revision":1}`,
		"/api/factory/formulas/validate": `{"definitionYaml":"schema: 1"}`,
		"/api/factory/formulas/preview":  `{"definitionYaml":"schema: 1","parameters":{"goal":"Ship","initial_project":"/repo"}}`,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}

	saveBody := `{"id":"custom/team","name":"Team","definitionYaml":"schema: 1"}`
	remote := httptest.NewRequest(http.MethodPost, "/api/factory/formulas", strings.NewReader(saveBody))
	remote.RemoteAddr = "10.0.0.5:1234"
	remoteRec := httptest.NewRecorder()
	mux.ServeHTTP(remoteRec, remote)
	if remoteRec.Code != http.StatusForbidden {
		t.Fatalf("remote save = %d, want 403", remoteRec.Code)
	}

	local := httptest.NewRequest(http.MethodPost, "/api/factory/formulas", strings.NewReader(saveBody))
	local.RemoteAddr = "127.0.0.1:1234"
	localRec := httptest.NewRecorder()
	mux.ServeHTTP(localRec, local)
	if localRec.Code != http.StatusCreated || svc.savedFormula.ID != "custom/team" {
		t.Fatalf("local save = %d, %#v: %s", localRec.Code, svc.savedFormula, localRec.Body.String())
	}

	svc.formulaErr = factory.ErrFormulaReferenced
	deleteReq := httptest.NewRequest(http.MethodPost, "/api/factory/formulas/delete", strings.NewReader(`{"id":"custom/team"}`))
	deleteReq.RemoteAddr = "127.0.0.1:1234"
	deleteRec := httptest.NewRecorder()
	mux.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusConflict {
		t.Fatalf("referenced delete = %d, want 409", deleteRec.Code)
	}
}
