package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type fakeFactoryService struct {
	epics                 []factory.WorkEpic
	issues                []factory.Issue
	comments              []factory.IssueComment
	commentEpicID         string
	commentIssueID        string
	commentActor          string
	createReq             factory.CreateWorkEpicRequest
	claimEpicID           string
	claimIssueID          string
	materializeEpicID     string
	materializeIssueID    string
	submitProposalReq     factory.SubmitProposalRequest
	proposalEpicID        string
	proposalRevision      int
	proposalsEpicID       string
	formulaID             string
	formulaVersion        int
	formulas              []factory.NativeFormulaView
	formulaSource         string
	previewID             string
	capacityPolicy        factory.CapacityPolicy
	gateAction            string
	gateRequest           factory.PlanGateDecisionRequest
	closedEpicID          string
	closedMolEpicID       string
	closedMolID           string
	reopenedIssueID       string
	mutation              factory.GraphMutation
	recoveryGate          factory.RecoveryGate
	recoveryAction        string
	recoveryResponse      string
	removed               []factory.Issue
	queue                 []factory.DispatchItem
	implementationSession bool
	implementationChecks  int
	escalationCalls       int
	err                   error
}

func (f *fakeFactoryService) CreateRecoveryGate(context.Context, string, string, string, string, []string) (factory.RecoveryGate, error) {
	return factory.RecoveryGate{}, f.err
}
func (f *fakeFactoryService) ResolveRecoveryGate(_ context.Context, id, action, response string) (factory.RecoveryGate, error) {
	f.recoveryGate.IssueID, f.recoveryAction, f.recoveryResponse = id, action, response
	return f.recoveryGate, f.err
}

func (f *fakeFactoryService) IsImplementationSession(context.Context, string) (bool, error) {
	f.implementationChecks++
	return f.implementationSession, f.err
}
func (f *fakeFactoryService) EscalatePermission(context.Context, string, string, string, string) (factory.AuthorityEscalationGate, bool, error) {
	f.escalationCalls++
	return factory.AuthorityEscalationGate{IssueID: "gate-1"}, f.implementationSession, f.err
}
func (f *fakeFactoryService) ResolveAuthorityEscalationGate(_ context.Context, id, action string) (factory.AuthorityEscalationGate, error) {
	return factory.AuthorityEscalationGate{IssueID: id, Resolution: action}, f.err
}

func (f *fakeFactoryService) ListFormulas(context.Context) ([]factory.NativeFormulaView, error) {
	return f.formulas, f.err
}
func (f *fakeFactoryService) ValidateFormula(_ context.Context, source, id string) (factory.NativeFormulaView, error) {
	f.formulaSource = source
	f.previewID = id
	if f.err != nil {
		return factory.NativeFormulaView{}, f.err
	}
	return factory.NativeFormulaView{Source: source, Valid: true}, nil
}
func (f *fakeFactoryService) PreviewFormula(ctx context.Context, source, id string) (factory.NativeFormulaView, error) {
	return f.ValidateFormula(ctx, source, id)
}
func (f *fakeFactoryService) SaveFormula(_ context.Context, req factory.FormulaSaveRequest) (factory.NativeFormulaView, error) {
	f.formulaSource = req.Source
	if f.err != nil {
		return factory.NativeFormulaView{}, f.err
	}
	formula := factory.NativeFormulaView{ID: req.ID, Version: len(f.formulas) + 1, Source: req.Source, Valid: true}
	f.formulas = append(f.formulas, formula)
	return formula, nil
}

func (f *fakeFactoryService) GetCapacityPolicy(context.Context) (factory.CapacityPolicy, error) {
	return f.capacityPolicy, f.err
}
func (f *fakeFactoryService) SetCapacityPolicy(_ context.Context, policy factory.CapacityPolicy) (factory.CapacityPolicy, error) {
	if f.err != nil {
		return factory.CapacityPolicy{}, f.err
	}
	f.capacityPolicy = policy
	return policy, nil
}

func (f *fakeFactoryService) GetFormula(_ context.Context, id string, version int) (factory.NativeFormulaView, error) {
	if f.err != nil {
		return factory.NativeFormulaView{}, f.err
	}
	f.formulaID, f.formulaVersion = id, version
	if id != "ocman/tracer" || version != 1 {
		return factory.NativeFormulaView{}, factory.ErrFormulaNotFound
	}
	return factory.NativeFormulaView{ID: id, Version: version, Source: "name = \"Tracer\"\n", Hash: "hash", Valid: true}, nil
}
func (*fakeFactoryService) Start(context.Context) error { return nil }
func (*fakeFactoryService) Close()                      {}
func (*fakeFactoryService) Status(context.Context) factory.Status {
	return factory.Status{Health: factory.HealthHealthy, Idle: true}
}
func (f *fakeFactoryService) CreateWorkEpic(_ context.Context, req factory.CreateWorkEpicRequest) (factory.WorkEpic, error) {
	f.createReq = req
	if f.err != nil {
		return factory.WorkEpic{}, f.err
	}
	return f.epics[0], nil
}
func (f *fakeFactoryService) ListWorkEpics(context.Context) ([]factory.WorkEpic, error) {
	return f.epics, f.err
}
func (f *fakeFactoryService) GetWorkEpic(_ context.Context, id string) (factory.WorkEpic, error) {
	for _, epic := range f.epics {
		if epic.ID == id {
			return epic, nil
		}
	}
	return factory.WorkEpic{}, factory.ErrWorkEpicNotFound
}
func (f *fakeFactoryService) Pour(context.Context, string) ([]factory.Issue, error) {
	return f.issues, f.err
}
func (f *fakeFactoryService) ListIssues(context.Context, string) ([]factory.Issue, error) {
	return f.issues, f.err
}
func (f *fakeFactoryService) ListIssueComments(_ context.Context, epicID, issueID string) ([]factory.IssueComment, error) {
	f.commentEpicID, f.commentIssueID = epicID, issueID
	return f.comments, f.err
}
func (f *fakeFactoryService) AddIssueComment(_ context.Context, epicID, issueID, actor, body string) (factory.IssueComment, error) {
	f.commentEpicID, f.commentIssueID, f.commentActor = epicID, issueID, actor
	comment := factory.IssueComment{ID: int64(len(f.comments) + 1), IssueID: issueID, Actor: actor, Body: body, CreatedAt: 1}
	f.comments = append(f.comments, comment)
	return comment, f.err
}
func (f *fakeFactoryService) ListRemovedIssues(context.Context, string) ([]factory.Issue, error) {
	return f.removed, f.err
}
func (f *fakeFactoryService) Queue(context.Context) ([]factory.DispatchItem, error) {
	return f.queue, f.err
}
func (f *fakeFactoryService) ClaimPlan(_ context.Context, epicID, issueID string) (factory.ClaimedPlan, error) {
	f.claimEpicID, f.claimIssueID = epicID, issueID
	return factory.ClaimedPlan{}, f.err
}
func (f *fakeFactoryService) Materialize(_ context.Context, epicID, issueID string) (factory.Materialization, error) {
	f.materializeEpicID, f.materializeIssueID = epicID, issueID
	return factory.Materialization{}, f.err
}
func (f *fakeFactoryService) SubmitProposal(_ context.Context, req factory.SubmitProposalRequest) (factory.ProposalRevision, error) {
	f.submitProposalReq = req
	return factory.ProposalRevision{}, f.err
}
func (f *fakeFactoryService) GetProposal(_ context.Context, epicID string, revision int) (factory.ProposalRevision, error) {
	f.proposalEpicID, f.proposalRevision = epicID, revision
	return factory.ProposalRevision{}, f.err
}
func (f *fakeFactoryService) ListProposals(_ context.Context, epicID string) ([]factory.ProposalRevision, error) {
	f.proposalsEpicID = epicID
	return []factory.ProposalRevision{{EpicID: epicID, Revision: 1}}, f.err
}
func (f *fakeFactoryService) DecidePlanGate(_ context.Context, _ string, action string, req factory.PlanGateDecisionRequest) (factory.PlanGate, error) {
	f.gateAction, f.gateRequest = action, req
	return factory.PlanGate{Resolution: action}, f.err
}
func (f *fakeFactoryService) CloseEpic(_ context.Context, epicID string) error {
	f.closedEpicID = epicID
	return f.err
}
func (f *fakeFactoryService) ReopenIssue(_ context.Context, epicID, issueID string) error {
	f.reopenedIssueID = epicID + "/" + issueID
	return f.err
}

func (f *fakeFactoryService) CloseMol(_ context.Context, epicID, molID string) error {
	f.closedMolEpicID, f.closedMolID = epicID, molID
	return f.err
}
func (f *fakeFactoryService) MutateGraph(_ context.Context, mutation factory.GraphMutation) error {
	f.mutation = mutation
	return f.err
}

func (f *fakeFactoryService) CompleteAttempt(context.Context, string, string, string, string) error {
	return nil
}

func TestWriteFactoryErrorSeparatesClientAndServerFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want int
	}{
		{"corrupt Formula", factory.ErrFormulaCorrupt, http.StatusInternalServerError},
		{"unavailable", factory.ErrFactoryUnavailable, http.StatusServiceUnavailable},
		{"missing Formula", factory.ErrFormulaNotFound, http.StatusNotFound},
		{"missing epic", factory.ErrWorkEpicNotFound, http.StatusNotFound},
		{"instantiation conflict", factory.ErrInstantiationConflict, http.StatusConflict},
		{"permission", factory.ErrActionNotPermitted, http.StatusForbidden},
		{"non-local project", factory.ErrProjectNotLocalGit, http.StatusBadRequest},
		{"acknowledgement", factory.ErrAcknowledgementRequired, http.StatusBadRequest},
		{"store failure", errors.New("database unavailable"), http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeFactoryError(rec, tt.err)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestFactoryEpicRoutes(t *testing.T) {
	svc := &fakeFactoryService{epics: []factory.WorkEpic{{ID: "fac-1", Goal: "Ship", Attempts: []model.FactoryAttempt{{ID: "attempt-1", Session: model.PlanningSession{Platform: "opencode", ID: "session-1"}}}}, {ID: "fac/encoded", Goal: "Encoded"}}, issues: []factory.Issue{{ID: "fac-1.child", Kind: "mol", FormulaID: "custom/child", FormulaVersion: 1, FormulaHash: "child-hash", Bindings: map[string]string{"goal": "Ship"}}}}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRequest(http.MethodPost, "/api/factory/epics", strings.NewReader(`{"goal":"Ship","initialProject":"/repo","acknowledgeLocalExecution":true}`))
	create.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated || svc.createReq.Goal != "Ship" || !svc.createReq.AcknowledgeLocalExecution {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/factory/epics", "/api/factory/epics/fac-1", "/api/factory/epics/fac%2Fencoded", "/api/factory/epics/fac-1/issues", "/api/factory/epics/fac-1/removed-issues"} {
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
		if path == "/api/factory/epics/fac-1" && !strings.Contains(rec.Body.String(), `"attempts":[{"id":"attempt-1"`) {
			t.Fatalf("detail = %s", rec.Body.String())
		}
	}
	gate := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/plan-gate/approve", strings.NewReader(`{"expectedRevision":1,"expectedHash":"hash"}`))
	gate.RemoteAddr = "127.0.0.1:1"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, gate)
	if rec.Code != http.StatusOK || svc.gateAction != "approve" || svc.gateRequest.ExpectedHash != "hash" {
		t.Fatalf("gate = %d: %s", rec.Code, rec.Body.String())
	}
	recovery := httptest.NewRequest(http.MethodPost, "/api/factory/recovery-gates/gate%2F1/resume", strings.NewReader(`{"response":"Use A"}`))
	recovery.RemoteAddr = "127.0.0.1:1"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, recovery)
	if rec.Code != http.StatusOK || svc.recoveryGate.IssueID != "gate/1" || svc.recoveryAction != "resume" || svc.recoveryResponse != "Use A" {
		t.Fatalf("recovery = %d: %s; call = %#v/%q/%q", rec.Code, rec.Body.String(), svc.recoveryGate, svc.recoveryAction, svc.recoveryResponse)
	}
	pour := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/pour", nil)
	pour.RemoteAddr = "127.0.0.1:1"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, pour)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"formulaId":"custom/child"`) || !strings.Contains(rec.Body.String(), `"bindings":{"goal":"Ship"}`) {
		t.Fatalf("pour = %d: %s", rec.Code, rec.Body.String())
	}
	for _, tt := range []struct{ path, wantMol string }{{"/api/factory/epics/fac-1/mols/fac-1/close", "fac-1"}, {"/api/factory/epics/fac-1/close", ""}} {
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, tt.path, nil)
		req.RemoteAddr = "127.0.0.1:1"
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("close %s = %d: %s", tt.path, rec.Code, rec.Body.String())
		}
		if tt.wantMol == "" && svc.closedEpicID != "fac-1" {
			t.Fatalf("closed epic = %q", svc.closedEpicID)
		}
		if tt.wantMol != "" && (svc.closedMolEpicID != "fac-1" || svc.closedMolID != tt.wantMol) {
			t.Fatalf("closed Mol = %q/%q", svc.closedMolEpicID, svc.closedMolID)
		}
	}
	rec = httptest.NewRecorder()
	reopen := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/issues/fac-1.1.4/reopen", nil)
	reopen.RemoteAddr = "127.0.0.1:1"
	mux.ServeHTTP(rec, reopen)
	if rec.Code != http.StatusNoContent || svc.reopenedIssueID != "fac-1/fac-1.1.4" {
		t.Fatalf("reopen = %d: %s; call = %q", rec.Code, rec.Body.String(), svc.reopenedIssueID)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/factory/epics/fac-1/issues/fac-1.child/comments", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("comments = %d: %s", rec.Code, rec.Body.String())
	}
	remoteComment := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/issues/fac-1.child/comments", strings.NewReader(`{"body":"Nope"}`))
	remoteComment.RemoteAddr = "192.0.2.1:1"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, remoteComment)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote add comment = %d: %s", rec.Code, rec.Body.String())
	}
	comment := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/issues/fac-1.child/comments", strings.NewReader(`{"body":"Looks good"}`))
	comment.RemoteAddr = "127.0.0.1:1"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, comment)
	if rec.Code != http.StatusCreated || svc.commentEpicID != "fac-1" || svc.commentIssueID != "fac-1.child" || svc.commentActor != "user" || !strings.Contains(rec.Body.String(), `"body":"Looks good"`) {
		t.Fatalf("add comment = %d: %s; call = %q/%q/%q", rec.Code, rec.Body.String(), svc.commentEpicID, svc.commentIssueID, svc.commentActor)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/factory/epics/fac-1/proposals", nil))
	if rec.Code != http.StatusOK || svc.proposalsEpicID != "fac-1" {
		t.Fatalf("proposal history = %d: %s; call = %q", rec.Code, rec.Body.String(), svc.proposalsEpicID)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/factory/epics/fac-1/proposals/1", nil))
	if rec.Code != http.StatusOK || svc.proposalEpicID != "fac-1" || svc.proposalRevision != 1 {
		t.Fatalf("proposal = %d: %s; call = %q, %d", rec.Code, rec.Body.String(), svc.proposalEpicID, svc.proposalRevision)
	}
}

func TestFactoryFormulaRoute(t *testing.T) {
	svc := &fakeFactoryService{}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/factory/formulas/ocman%2Ftracer/1", "/api/factory/formulas/missing/1", "/api/factory/formulas/ocman%2Ftracer/2"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if path == "/api/factory/formulas/ocman%2Ftracer/1" && (rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"ocman/tracer"`)) {
			t.Fatalf("GET Formula = %d: %s", rec.Code, rec.Body.String())
		}
		if path != "/api/factory/formulas/ocman%2Ftracer/1" && rec.Code != http.StatusNotFound {
			t.Fatalf("missing Formula = %d: %s", rec.Code, rec.Body.String())
		}
	}
}

func TestFactoryFormulaRouteDistinguishesUnavailableStorage(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	srv.factory = &fakeFactoryService{err: factory.ErrFactoryUnavailable}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/factory/formulas/custom%2Fteam/1", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable Formula = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFactoryFormulaLibraryRoutes(t *testing.T) {
	svc := &fakeFactoryService{formulas: []factory.NativeFormulaView{{ID: "ocman/tracer", Version: 1, Valid: true}}}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	request := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := httptest.NewRecorder(); func() *httptest.ResponseRecorder {
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/factory/formulas", nil))
		return rec
	}().Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	if rec := request("/api/factory/formulas", `{"id":"custom/team","source":"version = 1"}`); rec.Code != http.StatusCreated || svc.formulaSource != "version = 1" {
		t.Fatalf("save = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request("/api/factory/formulas/preview", `{"id":"custom/self","source":"version = 1"}`); rec.Code != http.StatusOK || svc.previewID != "custom/self" {
		t.Fatalf("preview = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request("/api/factory/formulas/validate", `{"source":"version = 1","extra":true}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid preview = %d", rec.Code)
	}
}

func TestFactoryFormulaRoutesReturnValidationFeedback(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	srv.factory = &fakeFactoryService{err: fmt.Errorf("%w: TOML source is required", factory.ErrInvalidFormula)}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/factory/formulas", strings.NewReader(`{"id":"custom/team","source":"name: YAML"}`))
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "TOML source is required") {
		t.Fatalf("validation response = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFactoryConfigurationRoute(t *testing.T) {
	svc := &fakeFactoryService{capacityPolicy: factory.CapacityPolicy{GlobalCapacity: 10, ProjectCapacity: 4, ProjectOverrides: map[string]int{}}}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	if rec := httptest.NewRecorder(); func() *httptest.ResponseRecorder {
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/factory/configuration", nil))
		return rec
	}().Code != http.StatusOK {
		t.Fatalf("GET = %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/factory/configuration", strings.NewReader(`{"globalCapacity":12,"projectCapacity":3,"projectOverrides":{}}`))
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || svc.capacityPolicy.GlobalCapacity != 12 {
		t.Fatalf("POST = %d: %s", rec.Code, rec.Body.String())
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/factory/configuration", strings.NewReader(`{"globalCapacity":10,"projectCapacity":4,"projectOverrides":{},"unexpected":true}`))
	bad.RemoteAddr = "127.0.0.1:1"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid POST = %d", rec.Code)
	}
	svc.err = fmt.Errorf("%w: factory capacity must be between 1 and 1000", factory.ErrInvalidRequest)
	bad = httptest.NewRequest(http.MethodPost, "/api/factory/configuration", strings.NewReader(`{"globalCapacity":0,"projectCapacity":4,"projectOverrides":{}}`))
	bad.RemoteAddr = "127.0.0.1:1"
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, bad)
	if rec.Code != http.StatusBadRequest || svc.capacityPolicy.GlobalCapacity != 12 {
		t.Fatalf("rejected update = %d: %s; policy = %#v", rec.Code, rec.Body.String(), svc.capacityPolicy)
	}
}

func TestFactoryEpicRoutesRejectInvalidRequests(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	srv.factory = &fakeFactoryService{err: fmt.Errorf("%w: nope", factory.ErrInvalidRequest)}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/factory/epics", strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid create = %d", rec.Code)
	}
}

func TestFactoryAcknowledgementRequiredIsBadRequest(t *testing.T) {
	srv := New(nil, nil, "", nil, nil)
	srv.factory = &fakeFactoryService{err: factory.ErrAcknowledgementRequired}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/factory/epics", strings.NewReader(`{"goal":"Ship","initialProject":"/repo"}`))
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFactoryRoutesSanitizeServiceFailures(t *testing.T) {
	svc := &fakeFactoryService{err: errors.New("private store failure")}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	failures := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/factory/epics", ""},
		{http.MethodPost, "/api/factory/epics", `{"goal":"Ship","initialProject":"/repo"}`},
		{http.MethodGet, "/api/factory/epics/fac-1/issues", ""},
		{http.MethodGet, "/api/factory/epics/fac-1/issues/work/comments", ""},
		{http.MethodPost, "/api/factory/epics/fac-1/issues/work/comments", `{"body":"note"}`},
		{http.MethodGet, "/api/factory/epics/fac-1/removed-issues", ""},
		{http.MethodPost, "/api/factory/epics/fac-1/mutations", `{"action":"edit","issueId":"work","title":"Rename"}`},
		{http.MethodPost, "/api/factory/epics/fac-1/pour", ""},
		{http.MethodPost, "/api/factory/epics/fac-1/close", ""},
		{http.MethodPost, "/api/factory/epics/fac-1/mols/mol-1/close", ""},
		{http.MethodPost, "/api/factory/epics/fac-1/issues/work/reopen", ""},
		{http.MethodPost, "/api/factory/epics/fac-1/plans/plan-1", ""},
		{http.MethodPost, "/api/factory/epics/fac-1/materializations/materialize-1", ""},
		{http.MethodPost, "/api/factory/epics/fac-1/plan-gate/approve", `{"expectedRevision":1,"expectedHash":"hash"}`},
		{http.MethodGet, "/api/factory/epics/fac-1/proposals", ""},
		{http.MethodPost, "/api/factory/epics/fac-1/proposals", `{"attemptId":"attempt-1","attemptToken":"token","manifest":{"epicId":"fac-1"}}`},
		{http.MethodGet, "/api/factory/epics/fac-1/proposals/1", ""},
		{http.MethodGet, "/api/factory/formulas", ""},
		{http.MethodPost, "/api/factory/formulas", `{"id":"custom/team","source":"version = 1"}`},
		{http.MethodPost, "/api/factory/formulas/validate", `{"id":"custom/team","source":"version = 1"}`},
		{http.MethodGet, "/api/factory/formulas/custom%2Fteam/1", ""},
	}
	for _, tt := range failures {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.RemoteAddr = "127.0.0.1:1"
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "private store failure") {
				t.Fatalf("response = %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	methods := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/factory/epics"},
		{http.MethodDelete, "/api/factory/epics/fac-1/issues/work/comments"},
		{http.MethodGet, "/api/factory/epics/fac-1/pour"},
		{http.MethodPut, "/api/factory/formulas"},
		{http.MethodGet, "/api/factory/formulas/validate"},
		{http.MethodPost, "/api/factory/formulas/custom%2Fteam/1"},
	}
	for _, tt := range methods {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("response = %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestFactoryPlanningRoutes(t *testing.T) {
	svc := &fakeFactoryService{}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(http.MethodPost, "/api/factory/epics/fac-1/plans/fac-1.plan", "", "192.0.2.1:1"); rec.Code != http.StatusForbidden {
		t.Fatalf("remote claim = %d", rec.Code)
	}
	if rec := request(http.MethodPost, "/api/factory/epics/fac-1/plans/fac-1.plan", "", "127.0.0.1:1"); rec.Code != http.StatusCreated || svc.claimEpicID != "fac-1" || svc.claimIssueID != "fac-1.plan" {
		t.Fatalf("claim = %d, %q, %q", rec.Code, svc.claimEpicID, svc.claimIssueID)
	}
	if rec := request(http.MethodPost, "/api/factory/epics/fac-1/materializations/fac-1.materialize", "", "192.0.2.1:1"); rec.Code != http.StatusForbidden {
		t.Fatalf("remote materialization = %d", rec.Code)
	}
	if rec := request(http.MethodPost, "/api/factory/epics/fac-1/materializations/fac-1.materialize", "", "127.0.0.1:1"); rec.Code != http.StatusCreated || svc.materializeEpicID != "fac-1" || svc.materializeIssueID != "fac-1.materialize" {
		t.Fatalf("materialization = %d, %q, %q", rec.Code, svc.materializeEpicID, svc.materializeIssueID)
	}
	if rec := request(http.MethodPost, "/api/factory/epics/fac-1/proposals", `{"epicId":"wrong","manifest":{},"extra":true}`, "127.0.0.1:1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown proposal field = %d", rec.Code)
	}
	if rec := request(http.MethodPost, "/api/factory/epics/fac-1/proposals", `{"manifest":{}} {}`, "127.0.0.1:1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("multiple proposal bodies = %d", rec.Code)
	}
	if rec := request(http.MethodPost, "/api/factory/epics/fac-1/proposals", `{"epicId":"wrong","attemptId":"attempt-1","attemptToken":"fat_token","manifest":{"epicId":"fac-1"}}`, "127.0.0.1:1"); rec.Code != http.StatusCreated || svc.submitProposalReq.EpicID != "fac-1" {
		t.Fatalf("proposal submit = %d, %#v", rec.Code, svc.submitProposalReq)
	}
	if rec := request(http.MethodPost, "/api/factory/epics/fac-1/proposals", `{"manifest":{"epicId":"fac-1"}}`, "127.0.0.1:1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("tokenless proposal = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(http.MethodPost, "/api/factory/epics/fac-1/proposals", `{}`, "192.0.2.1:1"); rec.Code != http.StatusForbidden {
		t.Fatalf("remote proposal = %d", rec.Code)
	}
	if rec := request(http.MethodGet, "/api/factory/epics/fac-1/proposals/0", "", "127.0.0.1:1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid revision = %d", rec.Code)
	}
}

func TestFactoryGraphMutationRouteIsLocalAndStrict(t *testing.T) {
	svc := &fakeFactoryService{}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	request := func(body, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/mutations", strings.NewReader(body))
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(`{"action":"edit","issueId":"fac-1.1","title":"Rename","unexpected":true}`, "127.0.0.1:1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(`{"action":"edit","issueId":"fac-1.1","title":"Rename"}`, "192.0.2.1:1"); rec.Code != http.StatusForbidden {
		t.Fatalf("remote mutation = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := request(`{"action":"edit","epicId":"wrong","issueId":"fac-1.1","title":"Rename"}`, "127.0.0.1:1"); rec.Code != http.StatusNoContent || svc.mutation.EpicID != "fac-1" || svc.mutation.Actor != "user" {
		t.Fatalf("mutation = %d: %#v", rec.Code, svc.mutation)
	}
	svc.err = fmt.Errorf("%w: invalid mutation", factory.ErrInvalidRequest)
	if rec := request(`{"action":"edit","issueId":"fac-1.1","title":"Rename"}`, "127.0.0.1:1"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid mutation = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFactoryStatusQueueAndGateRoutes(t *testing.T) {
	svc := &fakeFactoryService{queue: []factory.DispatchItem{{ID: "work", State: "ready"}}}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/api/factory/status", "/api/factory/queue"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
	for _, path := range []string{"/api/factory/recovery-gates/gate/invalid", "/api/factory/authority-gates/gate/invalid"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s = %d", path, rec.Code)
		}
	}
	for _, path := range []string{"/api/factory/recovery-gates/gate/resume", "/api/factory/authority-gates/gate/approve"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/factory/authority-gates/gate%2F1/approve", nil)
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"issueId":"gate/1"`) {
		t.Fatalf("authority resolution = %d: %s", rec.Code, rec.Body.String())
	}
	svc.err = errors.New("queue failed")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/factory/queue", nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "queue failed") {
		t.Fatalf("failed queue = %d: %s", rec.Code, rec.Body.String())
	}
}
