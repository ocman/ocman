package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type promptEnsureHost struct {
	hostsvc.Host
	ensure func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error)
}

func (h *promptEnsureHost) RemoteID() string { return "local" }
func (h *promptEnsureHost) EnsureProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return h.ensure(ctx, req)
}

func TestManagedPromptSessionsEnsuresProjectInstance(t *testing.T) {
	srv := testServer(t)
	var ensured string
	var created platforms.CreateSessionRequest
	srv.hostRouter = hostsvc.NewRouter(&promptEnsureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		ensured = req.ProjectDir
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:5599", RepoRoot: req.ProjectDir}, nil
	}})
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode", createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		created = req
		return &platforms.CreateSessionResponse{ID: "scheduled-session"}, nil
	}})
	srv.registry = reg

	platformID, session, err := (managedPromptSessions{srv}).CreateScheduledSession(t.Context(), "local", "/repo")
	if err != nil || platformID != "opencode" || session.ID != "scheduled-session" {
		t.Fatalf("platform=%q session=%+v err=%v", platformID, session, err)
	}
	if ensured != "/repo" || created.Directory != "/repo" || created.Port != "5599" {
		t.Fatalf("ensured=%q created=%+v", ensured, created)
	}
}

func TestManagedPromptSessionsQueuesForBusyReusedSession(t *testing.T) {
	srv, registry := newSessionsTestServer(t)
	sent := 0
	registry.Register(&fakePlatform{
		id:       "opencode",
		sessions: []db.Session{mkSession("opencode", "reused", "scheduled", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error {
			sent++
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "busy"}}, nil
		},
	})
	if err := (managedPromptSessions{srv}).SendScheduledMessage(t.Context(), "opencode", platforms.SendMessageRequest{SessionID: "reused", Message: "again"}, true); err != nil {
		t.Fatal(err)
	}
	queued, err := srv.queueSvc().List("opencode", "reused")
	if err != nil || len(queued) != 1 || sent != 0 {
		t.Fatalf("queued=%+v sent=%d err=%v", queued, sent, err)
	}
}

func TestPromptScheduleHTTPLifecycle(t *testing.T) {
	srv := testServer(t)
	now := time.UnixMilli(1000)
	sessions := &fakeSessions{}
	srv.promptScheduleSvc = newPromptScheduleService(srv.stateDB, sessions, func() time.Time { return now }, func() string { return "ps_http" })

	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handlePromptSchedules(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}

	create := request(http.MethodPost, "/api/prompt-schedules", `{"directory":"/repo","prompt":" exact\n","runAt":5000}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	if got := create.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("create content type = %q", got)
	}
	var schedule state.PromptSchedule
	if err := json.Unmarshal(create.Body.Bytes(), &schedule); err != nil || schedule.ID != "ps_http" {
		t.Fatalf("schedule=%+v err=%v", schedule, err)
	}

	for _, path := range []string{"/api/prompt-schedules?directory=%2Frepo", "/api/prompt-schedules/ps_http"} {
		if rec := request(http.MethodGet, path, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ps_http") {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
	}

	run := request(http.MethodPost, "/api/prompt-schedules/ps_http/run-now", "")
	if run.Code != http.StatusOK {
		t.Fatalf("run-now: %d %s", run.Code, run.Body.String())
	}
	if len(sessions.created) != 1 || sessions.created[0].Directory != "/repo" || len(sessions.sent) != 1 || sessions.sent[0].Message != " exact\n" {
		t.Fatalf("created=%+v sent=%+v", sessions.created, sessions.sent)
	}
	if !strings.Contains(run.Body.String(), "session-1") || !strings.Contains(run.Body.String(), "completed") {
		t.Fatalf("run response: %s", run.Body.String())
	}
}

func TestPromptScheduleHTTPCancelAndValidation(t *testing.T) {
	srv := testServer(t)
	srv.promptScheduleSvc = newPromptScheduleService(srv.stateDB, &fakeSessions{}, func() time.Time { return time.UnixMilli(1000) }, func() string { return "ps_cancel" })

	bad := httptest.NewRecorder()
	srv.handlePromptSchedules(bad, httptest.NewRequest(http.MethodPost, "/api/prompt-schedules", strings.NewReader(`{"directory":"/repo"}`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad create: %d %s", bad.Code, bad.Body.String())
	}

	create := httptest.NewRecorder()
	srv.handlePromptSchedules(create, httptest.NewRequest(http.MethodPost, "/api/prompt-schedules", strings.NewReader(`{"directory":"/repo","prompt":"later","runAt":5000}`)))
	cancel := httptest.NewRecorder()
	srv.handlePromptSchedules(cancel, httptest.NewRequest(http.MethodPost, "/api/prompt-schedules/ps_cancel/cancel", nil))
	if cancel.Code != http.StatusOK || !strings.Contains(cancel.Body.String(), "canceled") {
		t.Fatalf("cancel: %d %s", cancel.Code, cancel.Body.String())
	}
	second := httptest.NewRecorder()
	srv.handlePromptSchedules(second, httptest.NewRequest(http.MethodPost, "/api/prompt-schedules/ps_cancel/run-now", nil))
	if second.Code != http.StatusConflict {
		t.Fatalf("run canceled: %d %s", second.Code, second.Body.String())
	}
	missing := httptest.NewRecorder()
	srv.handlePromptSchedules(missing, httptest.NewRequest(http.MethodPost, "/api/prompt-schedules/missing/run-now", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("run missing: %d %s", missing.Code, missing.Body.String())
	}
}

func TestPromptScheduleHTTPRecurringReuseAndEnablement(t *testing.T) {
	srv := testServer(t)
	now := time.Date(2030, 1, 1, 8, 30, 0, 0, time.UTC)
	srv.promptScheduleSvc = newPromptScheduleService(srv.stateDB, &fakeSessions{}, func() time.Time { return now }, func() string { return "ps_recurring" })

	request := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.handlePromptSchedules(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec
	}
	create := request("/api/prompt-schedules", `{"directory":"/repo","prompt":"repeat","timingType":"cron","cron":"0 9 * * *","timezone":"Europe/Brussels","sessionMode":"reuse"}`)
	if create.Code != http.StatusCreated || !strings.Contains(create.Body.String(), `"sessionMode":"reuse"`) || !strings.Contains(create.Body.String(), `"enabled":true`) {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	disabled := request("/api/prompt-schedules/ps_recurring/disable", "")
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body.String(), `"enabled":false`) {
		t.Fatalf("disable: %d %s", disabled.Code, disabled.Body.String())
	}
	enabled := request("/api/prompt-schedules/ps_recurring/enable", "")
	if enabled.Code != http.StatusOK || !strings.Contains(enabled.Body.String(), `"enabled":true`) {
		t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
	}
}

func TestPromptScheduleHTTPStorageErrors(t *testing.T) {
	store := newFakeStore()
	store.listErr = errors.New("list failed")
	srv := &Server{promptScheduleSvc: newPromptScheduleService(store, &fakeSessions{}, func() time.Time { return time.UnixMilli(1000) }, nil)}
	rec := httptest.NewRecorder()
	srv.handlePromptSchedules(rec, httptest.NewRequest(http.MethodGet, "/api/prompt-schedules?directory=%2Frepo", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list storage error: %d %s", rec.Code, rec.Body.String())
	}
	store.listErr = nil
	store.createErr = errors.New("create failed")
	rec = httptest.NewRecorder()
	srv.handlePromptSchedules(rec, httptest.NewRequest(http.MethodPost, "/api/prompt-schedules", strings.NewReader(`{"directory":"/repo","prompt":"later","runAt":5000}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("create storage error: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPromptScheduleRecoveryFailureStopsStartup(t *testing.T) {
	srv := testServer(t)
	store := newFakeStore()
	store.recoverErr = errors.New("locked")
	srv.promptScheduleSvc = newPromptScheduleService(store, &fakeSessions{}, nil, nil)
	if err := srv.StartOnListener(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "recovering interrupted prompt schedules") {
		t.Fatalf("StartOnListener error = %v", err)
	}
}
