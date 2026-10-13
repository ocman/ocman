package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/factorymod"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type controlFixture struct {
	srv      *Server
	platform *fakePlatform
	svc      *fakeFactoryService
	mux      http.Handler
	mu       sync.Mutex
	sent     []platforms.SendMessageRequest
	rules    platforms.SetPermissionRulesRequest
	creates  int
}

func newControlFixture(t *testing.T) *controlFixture {
	t.Helper()
	f := &controlFixture{}
	endpoint := connectedPlanningMCP(t)
	f.platform = &fakePlatform{id: "local-agent", caps: platforms.Capabilities{PermissionRules: true}}
	f.platform.createSessionFn = func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		f.creates++
		f.platform.sessions = append(f.platform.sessions, db.Session{ID: "control-1"})
		return &platforms.CreateSessionResponse{ID: "control-1"}, nil
	}
	f.platform.setPermissionRulesFn = func(req platforms.SetPermissionRulesRequest) error { f.rules = req; return nil }
	f.platform.sendMessageFn = func(req platforms.SendMessageRequest) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sent = append(f.sent, req)
		return nil
	}
	registry := platforms.NewRegistry()
	registry.Register(f.platform)
	f.srv = New(nil, openTestStateDB(t), "", registry, nil)
	tracer := factory.BuiltInTracerFormula()
	if _, err := f.srv.stateDB.CreateFactoryEpic(t.Context(), "epic-1", "Ship", "", "/repo", "", model.NativeFormula{ID: tracer.ID, Version: tracer.Version, Source: tracer.Source, Hash: tracer.Hash,
		Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}}}); err != nil {
		t.Fatal(err)
	}
	f.svc = &fakeFactoryService{
		epics:  []factory.WorkEpic{{ID: "epic-1", Goal: "Ship", Status: "open", InitialProject: "/repo", Projects: []model.EpicProject{{Path: "/repo"}, {Path: "/other", Removable: true}}}},
		issues: []factory.Issue{{ID: "issue-1", EpicID: "epic-1", Kind: "implementation", Title: "Transport", Status: "closed", Outcome: "failed", OutcomeReason: "merged branch", Recovery: &factory.RecoveryGate{IssueID: "gate-1"}}},
	}
	useFactory(f.srv, f.svc)
	f.srv.hostRouter = hostsvc.NewRouter(&ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
		return &hostsvc.EnsureRuntimeResult{Endpoint: endpoint, RepoRoot: req.ProjectDir}, nil
	}})
	mux, err := f.srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	f.mux = mux
	return f
}

func (f *controlFixture) call(method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, nil)
	request.RemoteAddr = "127.0.0.1:1234"
	f.mux.ServeHTTP(recorder, request)
	return recorder
}

// delivered reports whether text reached the control session, sent or queued.
func (f *controlFixture) delivered(t *testing.T, text string) bool {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, req := range f.sent {
		if strings.Contains(req.Message, text) {
			return true
		}
	}
	queued, err := f.srv.stateDB.ListQueuedMessages(t.Context(), "local-agent", "control-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range queued {
		if strings.Contains(message.Text, text) {
			return true
		}
	}
	return false
}

func TestFactoryControlSessionIsOnePerEpic(t *testing.T) {
	f := newControlFixture(t)
	if got := f.call(http.MethodGet, "/api/factory/epics/epic-1/control"); got.Code != http.StatusNotFound {
		t.Fatalf("control before start = %d", got.Code)
	}
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"sessionId":"control-1"`) {
		t.Fatalf("start = %d %s", got.Code, got.Body.String())
	}
	if len(f.sent) != 1 || !strings.Contains(f.sent[0].Message, "control session for Factory Work Epic epic-1") || !strings.Contains(f.sent[0].Message, "factory_control") || !strings.Contains(f.sent[0].Message, "/other") || !strings.Contains(f.sent[0].Message, "include the Issue title alongside its ID") {
		t.Fatalf("intro = %#v", f.sent)
	}
	projectRule := platforms.PermissionRule{Permission: "external_directory", Pattern: filepath.Join("/other", "**"), Action: "allow"}
	controlRule := platforms.PermissionRule{Permission: "mcp_factory_control", Pattern: "factory_control", Action: "allow"}
	if !slices.Contains(f.rules.Rules, controlRule) || !slices.Contains(f.rules.Rules, projectRule) || slices.Contains(f.rules.Rules, platforms.PermissionRule{Permission: "edit", Pattern: "*", Action: "allow"}) {
		t.Fatalf("rules = %#v", f.rules.Rules)
	}
	if got := f.call(http.MethodGet, "/api/factory/epics/epic-1/control"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"sessionId":"control-1"`) {
		t.Fatalf("control = %d %s", got.Code, got.Body.String())
	}

	// Discussing an Issue reuses the session and posts its evidence.
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/issues/issue-1/discuss"); got.Code != http.StatusCreated || f.creates != 1 || !f.delivered(t, "merged branch") {
		t.Fatalf("discuss = %d, creates %d", got.Code, f.creates)
	}
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/issues/missing/discuss"); got.Code != http.StatusBadRequest {
		t.Fatalf("discuss missing = %d", got.Code)
	}

	// Factory news reaches the same session.
	f.srv.factoryModule().AttentionToControl(t.Context(), "epic-1", []string{"Recovery required: Transport"})
	deadline := time.Now().Add(5 * time.Second)
	for !f.delivered(t, "Recovery required: Transport") {
		if time.Now().After(deadline) {
			t.Fatal("attention never reached the control session")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A control session that no longer exists is replaced.
	f.platform.sessions = nil
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control"); got.Code != http.StatusOK || f.creates != 2 {
		t.Fatalf("replace = %d, creates %d", got.Code, f.creates)
	}

	// A closed Epic gets no new control session.
	f.svc.epics[0].Status = "closed"
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control"); got.Code != http.StatusBadRequest {
		t.Fatalf("closed epic = %d %s", got.Code, got.Body.String())
	}
}

func TestFactoryControlMessagesRespectEpicPlanningModel(t *testing.T) {
	for _, selected := range []string{"test/planner", ""} {
		t.Run("model="+selected, func(t *testing.T) {
			f := newControlFixture(t)
			f.svc.epics[0].Models = model.EpicModels{Plan: selected, Implementation: "test/implementer", Verification: "test/verifier"}
			f.platform.sessionModelsFn = func() *platforms.SessionModelsResponse {
				return &platforms.SessionModelsResponse{Models: []platforms.SessionModel{{Provider: "test", Model: "astra", IsAvailable: true}}}
			}
			if _, err := f.srv.factoryModule().PostControl(t.Context(), "epic-1", "First update"); err != nil {
				t.Fatal(err)
			}
			if len(f.sent) != 1 || f.sent[0].Model != selected {
				t.Fatalf("initial control prompt = %#v, want model %q", f.sent, selected)
			}
			queued, err := f.srv.stateDB.ListQueuedMessages(t.Context(), "local-agent", "control-1")
			if err != nil || len(queued) != 1 || queued[0].Model != selected {
				t.Fatalf("first update = %#v, %v, want model %q", queued, err, selected)
			}
			f.svc.epics[0].Models.Plan = "test/new-planner"
			if _, err := f.srv.factoryModule().PostControl(t.Context(), "epic-1", "Next update"); err != nil {
				t.Fatal(err)
			}
			queued, err = f.srv.stateDB.ListQueuedMessages(t.Context(), "local-agent", "control-1")
			if err != nil || len(queued) != 2 || queued[1].Model != "test/new-planner" || queued[0].Model != selected || f.creates != 1 {
				t.Fatalf("updates after model change = %#v, %v, creates %d", queued, err, f.creates)
			}
		})
	}
}

func TestFactoryControlUpdateRequiresEpic(t *testing.T) {
	f := newControlFixture(t)
	if _, err := f.srv.factoryModule().PostControl(t.Context(), "missing", "Update"); !errors.Is(err, factory.ErrWorkEpicNotFound) {
		t.Fatalf("missing Epic update = %v", err)
	}
	if f.creates != 0 || len(f.sent) != 0 {
		t.Fatalf("missing Epic launched %d sessions and sent %d messages", f.creates, len(f.sent))
	}
}

func TestFactoryDiscussIssueWithManyInstalledSkills(t *testing.T) {
	factorySkillRulesFixture(t)
	skills := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "opencode", "skills")
	for i := range 110 {
		directory := filepath.Join(skills, strings.Repeat("s", i+1))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("# Skill\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := newControlFixture(t)
	got := f.call(http.MethodPost, "/api/factory/epics/epic-1/issues/issue-1/discuss")
	if got.Code != http.StatusCreated {
		t.Fatalf("investigate blocked issue = %d %s", got.Code, got.Body.String())
	}
	if len(f.rules.Rules) <= 100 || !f.delivered(t, "Look into Issue Transport (issue-1)") {
		t.Fatalf("rules = %d, issue investigation not delivered", len(f.rules.Rules))
	}
	for _, rule := range []platforms.PermissionRule{
		{Permission: "external_directory", Pattern: "*", Action: "deny"},
		{Permission: "bash", Pattern: "*", Action: "deny"},
		{Permission: "edit", Pattern: "*", Action: "deny"},
	} {
		if !slices.Contains(f.rules.Rules, rule) {
			t.Fatalf("missing restriction %#v", rule)
		}
	}
	for _, rule := range factorymod.ExternalDirectoryRules() {
		if !slices.Contains(f.rules.Rules, rule) {
			t.Fatalf("missing skill rule %#v", rule)
		}
	}
}

// A session whose first prompt may not have arrived is never adopted.
func TestFactoryControlSessionIsNotAdoptedWithoutItsProtocol(t *testing.T) {
	f := newControlFixture(t)
	aborted := false
	f.platform.abortFn = func(req platforms.AbortRequest) error { aborted = req.SessionID == "control-1"; return nil }
	f.platform.sendMessageFn = func(platforms.SendMessageRequest) error { return errors.New("response lost") }
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control"); got.Code == http.StatusOK || !aborted {
		t.Fatalf("failed start = %d, aborted %v", got.Code, aborted)
	}
	if _, found, _ := f.srv.stateDB.GetFactoryControlSession(t.Context(), "epic-1"); found {
		t.Fatal("a session without its protocol was recorded")
	}
}

func TestFactoryControlProposalsRunOnlyOnConfirmation(t *testing.T) {
	f := newControlFixture(t)
	ctx := t.Context()
	for _, tc := range []struct {
		action string
		args   map[string]string
		want   string
	}{
		{"explode", nil, "unsupported"},
		{"reopen", nil, "issue_id is required"},
		{"mutate_graph", map[string]string{"mutation_json": `{"bogus":1}`}, "mutation_json is invalid"},
		{"resolve_recovery", map[string]string{"recovery_gate_id": "gate-1", "decision": "maybe"}, "decision must be"},
	} {
		if _, err := f.srv.factoryModule().ProposeControl(ctx, "epic-1", tc.action, tc.args, "Do it"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s = %v, want %q", tc.action, err, tc.want)
		}
	}
	if _, err := f.srv.factoryModule().ProposeControl(ctx, "epic-1", "pause", nil, " "); err == nil {
		t.Fatal("a proposal without a summary was accepted")
	}
	propose := func(action string, args map[string]string) string {
		t.Helper()
		id, err := f.srv.factoryModule().ProposeControl(ctx, "epic-1", action, args, "Do "+action)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	reopen := propose("reopen", map[string]string{"issue_id": "issue-1"})
	if f.svc.reopenedIssueID != "" {
		t.Fatal("a proposal ran before confirmation")
	}
	if got := f.call(http.MethodGet, "/api/factory/epics/epic-1/control-actions/"+reopen); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"status":"pending"`) || !strings.Contains(got.Body.String(), "Do reopen") {
		t.Fatalf("read = %d %s", got.Code, got.Body.String())
	}
	if got := f.call(http.MethodGet, "/api/factory/epics/epic-2/control-actions/"+reopen); got.Code != http.StatusNotFound {
		t.Fatalf("another epic read = %d", got.Code)
	}
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control-actions/"+reopen+"/confirm"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"status":"done"`) || f.svc.reopenedIssueID != "epic-1/issue-1" {
		t.Fatalf("confirm = %d %s, reopened %q", got.Code, got.Body.String(), f.svc.reopenedIssueID)
	}
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control-actions/"+reopen+"/confirm"); got.Code != http.StatusBadRequest {
		t.Fatalf("second confirm = %d", got.Code)
	}

	pause := propose("pause", nil)
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control-actions/"+pause+"/dismiss"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"status":"dismissed"`) || f.svc.pausedEpicID != "" {
		t.Fatalf("dismiss = %d %s", got.Code, got.Body.String())
	}

	for _, tc := range []struct {
		action, status string
		args           map[string]string
		check          func() bool
	}{
		{"resume", "done", nil, func() bool { return f.svc.pausedEpicID == "epic-1" && !f.svc.paused }},
		{"deliver_as_is", "done", nil, func() bool { return f.svc.closedEpicID == "epic-1" }},
		{"mutate_graph", "done", map[string]string{"mutation_json": `{"action":"delete","issueId":"issue-1"}`}, func() bool { return f.svc.mutation.Actor == "user" && f.svc.mutation.EpicID == "epic-1" }},
		{"resolve_recovery", "done", map[string]string{"recovery_gate_id": "gate-1", "decision": "cancel"}, func() bool { return f.svc.recoveryGate.IssueID == "gate-1" && f.svc.recoveryAction == "cancel" }},
		{"resolve_recovery", "failed", map[string]string{"recovery_gate_id": "foreign", "decision": "cancel"}, func() bool { return f.svc.recoveryGate.IssueID == "gate-1" }},
	} {
		id := propose(tc.action, tc.args)
		got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control-actions/"+id+"/confirm")
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"status":"`+tc.status+`"`) || !tc.check() {
			t.Fatalf("%s confirm = %d %s", tc.action, got.Code, got.Body.String())
		}
	}
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control-actions/missing/confirm"); got.Code != http.StatusBadRequest {
		t.Fatalf("missing proposal = %d", got.Code)
	}
	if got := f.call(http.MethodPut, "/api/factory/epics/epic-1/control-actions/"+pause+"/confirm"); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT = %d", got.Code)
	}

	// With a control session, the outcome is posted back so it can carry on.
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control"); got.Code != http.StatusOK {
		t.Fatalf("start = %d", got.Code)
	}
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control-actions/"+propose("pause", nil)+"/confirm"); got.Code != http.StatusOK || !f.delivered(t, "The user confirmed: Do pause") {
		t.Fatalf("outcome = %d", got.Code)
	}

	f.svc.epics[0].Status = "closed"
	if _, err := f.srv.factoryModule().ProposeControl(ctx, "epic-1", "pause", nil, "Pause"); err == nil {
		t.Fatal("proposal on a closed Epic accepted")
	}
}

// lifecyclePlatform answers lifecycle reads where Owns cannot see the session.
type lifecyclePlatform struct {
	*fakePlatform
	err error
}

func (p *lifecyclePlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return &platforms.SessionLifecycle{}, p.err
}

// A session Owns misses but the lifecycle read finds is kept; only a session
// known to be gone is replaced, and other read errors surface.
func TestFactoryControlSessionSurvivesOwnsMisses(t *testing.T) {
	f := newControlFixture(t)
	if got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control"); got.Code != http.StatusOK {
		t.Fatalf("start = %d", got.Code)
	}
	f.platform.sessions = nil
	wrapped := &lifecyclePlatform{fakePlatform: f.platform}
	registry := platforms.NewRegistry()
	registry.Register(wrapped)
	f.srv.registry = registry
	if _, _, err := f.srv.factoryModule().EnsureControlSession(t.Context(), "epic-1"); err != nil || f.creates != 1 {
		t.Fatalf("lifecycle hit = %v, creates %d", err, f.creates)
	}
	wrapped.err = errors.New("database is locked")
	if _, _, err := f.srv.factoryModule().EnsureControlSession(t.Context(), "epic-1"); err == nil || f.creates != 1 {
		t.Fatalf("transient error = %v, creates %d", err, f.creates)
	}
	wrapped.err = platforms.ErrNotFound
	if _, created, err := f.srv.factoryModule().EnsureControlSession(t.Context(), "epic-1"); err != nil || !created || f.creates != 2 {
		t.Fatalf("gone = %v, created %v, creates %d", err, created, f.creates)
	}
}

// An Epic that closes while its control session launches never adopts it.
func TestFactoryControlSessionIsNotAdoptedByAClosingEpic(t *testing.T) {
	f := newControlFixture(t)
	aborted := false
	f.platform.abortFn = func(platforms.AbortRequest) error { aborted = true; return nil }
	// The Epic closes after the open check, while the session launches.
	f.platform.sendMessageFn = func(platforms.SendMessageRequest) error {
		return f.srv.stateDB.CloseFactoryEpic(t.Context(), "epic-1", true)
	}
	if _, _, err := f.srv.factoryModule().EnsureControlSession(t.Context(), "epic-1"); err == nil || !aborted {
		t.Fatalf("closing epic = %v, aborted %v", err, aborted)
	}
	if _, found, _ := f.srv.stateDB.GetFactoryControlSession(t.Context(), "epic-1"); found {
		t.Fatal("a closed Epic adopted a control session")
	}
	archived, err := f.srv.stateDB.ArchivedSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archived[state.Key{Platform: "local-agent", SessionID: "control-1"}]; !ok {
		t.Fatalf("unadopted session not archived: %v", archived)
	}
}

// A confirmed proposal finishes even when the client goes away.
func TestFactoryControlConfirmOutlivesTheRequest(t *testing.T) {
	f := newControlFixture(t)
	id, err := f.srv.factoryModule().ProposeControl(t.Context(), "epic-1", "pause", nil, "Pause")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	f.svc.onSetPaused = cancel // the client disconnects mid-run
	action, err := f.srv.factoryModule().DecideControlAction(ctx, "epic-1", id, "confirm")
	if err != nil || action.Status != "done" {
		t.Fatalf("confirm = %#v, %v", action, err)
	}
	stored, _, _ := f.srv.stateDB.GetFactoryControlAction(t.Context(), "epic-1", id)
	if stored.Status != "done" {
		t.Fatalf("stored = %#v", stored)
	}
}

// A proposal whose run outlives its deadline still records a terminal outcome.
func TestFactoryControlConfirmRecordsATimedOutRun(t *testing.T) {
	f := newControlFixture(t)
	previous := factorymod.ControlRunTimeout
	factorymod.ControlRunTimeout = 20 * time.Millisecond
	t.Cleanup(func() { factorymod.ControlRunTimeout = previous })
	id, err := f.srv.factoryModule().ProposeControl(t.Context(), "epic-1", "pause", nil, "Pause")
	if err != nil {
		t.Fatal(err)
	}
	f.svc.onSetPaused = func() { time.Sleep(50 * time.Millisecond) } // runs past its deadline
	if _, err := f.srv.factoryModule().DecideControlAction(t.Context(), "epic-1", id, "confirm"); err != nil {
		t.Fatal(err)
	}
	stored, _, _ := f.srv.stateDB.GetFactoryControlAction(t.Context(), "epic-1", id)
	if stored.Status != "done" && stored.Status != "failed" {
		t.Fatalf("stored = %#v, want a terminal outcome", stored)
	}
}
