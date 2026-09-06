package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/routines"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
)

type routineTestHost struct{ hostsvc.Host }

func (*routineTestHost) RemoteID() string { return "local" }
func (*routineTestHost) EnsureProjectOpencode(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:1234", RepoRoot: "/repo", Runtime: ocruntime.Instance{ID: "test"}}, nil
}

func routineHTTPServer(t *testing.T) (*Server, http.Handler, *atomic.Int32) {
	t.Helper()
	srv := testServer(t)
	sent := &atomic.Int32{}
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{
		id: "opencode",
		createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "session-1"}, nil
		},
		sendMessageFn: func(platforms.SendMessageRequest) error { sent.Add(1); return nil },
	})
	router := hostsvc.NewRouter(&routineTestHost{})
	srv.registry = registry
	srv.sessions = sessionsvc.New(registry, sessionsvc.Hooks{})
	srv.hostRouter = router
	var ids atomic.Int32
	var now atomic.Int64
	now.Store(1000)
	srv.routineSvc = routines.New(routines.Deps{
		Store: srv.stateDB, Router: router, Sessions: srv.sessions, Platforms: registry,
		Now:   func() time.Time { return time.UnixMilli(now.Add(1)) },
		NewID: func(prefix string) string { return prefix + string('0'+ids.Add(1)) },
	})
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	return srv, mux, sent
}

func doRoutineRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

const validRoutineBody = `{"name":"Daily check","prompt":"inspect","directory":"/repo","agent":"build","model":"openai/gpt-5.4","sessionMode":"new","schedule":{"kind":"none"},"enabled":true}`

func TestRoutineHTTPLifecycle(t *testing.T) {
	_, handler, sent := routineHTTPServer(t)
	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/routines", ""); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("empty list: %d %s", rec.Code, rec.Body.String())
	}

	create := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", validRoutineBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	var created state.Routine
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil || created.ID == "" || created.Name != "Daily check" || created.SessionMode != routines.SessionNew || !strings.Contains(create.Body.String(), `"agent":"build"`) || !strings.Contains(create.Body.String(), `"model":"openai/gpt-5.4"`) {
		t.Fatalf("created=%+v err=%v", created, err)
	}

	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/routines/"+created.ID, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"Daily check"`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}
	updatedBody := strings.Replace(validRoutineBody, "Daily check", "Renamed", 1)
	if rec := doRoutineRequest(t, handler, http.MethodPut, "/api/routines/"+created.ID, updatedBody); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"Renamed"`) {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/routines/"+created.ID+"/history", ""); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("empty history: %d %s", rec.Code, rec.Body.String())
	}

	run := doRoutineRequest(t, handler, http.MethodPost, "/api/routines/"+created.ID+"/run", "")
	if run.Code != http.StatusOK || sent.Load() != 1 || !strings.Contains(run.Body.String(), `"sessionId":"session-1"`) {
		t.Fatalf("run: %d %s sent=%d", run.Code, run.Body.String(), sent.Load())
	}
	history := doRoutineRequest(t, handler, http.MethodGet, "/api/routines/"+created.ID+"/history", "")
	if history.Code != http.StatusOK || !strings.HasPrefix(history.Body.String(), "[") || !strings.Contains(history.Body.String(), `"routineId":"`+created.ID+`"`) {
		t.Fatalf("history: %d %s", history.Code, history.Body.String())
	}

	if rec := doRoutineRequest(t, handler, http.MethodDelete, "/api/routines/"+created.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/routines", ""); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("list after delete: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRoutineHTTPValidationConflictAndMissing(t *testing.T) {
	_, handler, _ := routineHTTPServer(t)
	for _, body := range []string{`{`, `{"name":"","prompt":"inspect","directory":"/repo","schedule":{"kind":"none"}}`, `{"name":"bad","prompt":"inspect","directory":"/repo","sessionMode":"existing","schedule":{"kind":"none"}}`, `{"name":"bad","prompt":"inspect","directory":"/repo","sessionMode":"invalid","schedule":{"kind":"none"}}`} {
		if rec := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("bad body %q: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := doRoutineRequest(t, handler, http.MethodPut, "/api/routines/missing", `{`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad update body: %d %s", rec.Code, rec.Body.String())
	}
	overflow := `{"name":"bad timeout","prompt":"inspect","directory":"/repo","schedule":{"kind":"timeout","timeoutMs":-9223372036854775808}}`
	if rec := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", overflow); rec.Code != http.StatusBadRequest {
		t.Fatalf("overflow: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", strings.Repeat(" ", maxRequestBody+1)); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized body: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", validRoutineBody); rec.Code != http.StatusCreated {
		t.Fatalf("first create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", validRoutineBody); rec.Code != http.StatusConflict {
		t.Fatalf("conflict: %d %s", rec.Code, rec.Body.String())
	}
	otherBody := strings.Replace(validRoutineBody, "Daily check", "Other", 1)
	other := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", otherBody)
	var otherRoutine state.Routine
	if other.Code != http.StatusCreated || json.Unmarshal(other.Body.Bytes(), &otherRoutine) != nil {
		t.Fatalf("other create: %d %s", other.Code, other.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodPut, "/api/routines/"+otherRoutine.ID, validRoutineBody); rec.Code != http.StatusConflict {
		t.Fatalf("update conflict: %d %s", rec.Code, rec.Body.String())
	}

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/routines/missing", ""},
		{http.MethodPut, "/api/routines/missing", validRoutineBody},
		{http.MethodDelete, "/api/routines/missing", ""},
		{http.MethodPost, "/api/routines/missing/run", ""},
		{http.MethodGet, "/api/routines/missing/history", ""},
	} {
		if rec := doRoutineRequest(t, handler, tc.method, tc.path, tc.body); rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestRoutineHTTPMethodAndLocalhostGuards(t *testing.T) {
	_, handler, _ := routineHTTPServer(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodPatch, "/api/routines"},
		{http.MethodPost, "/api/routines/missing"},
		{http.MethodGet, "/api/routines/missing/run"},
		{http.MethodPost, "/api/routines/missing/history"},
	} {
		if rec := doRoutineRequest(t, handler, tc.method, tc.path, ""); rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/routines", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("remote request: %d %s", rec.Code, rec.Body.String())
	}

	for _, path := range []string{"/api/routines/id/unknown", "/api/routines/id/history/extra"} {
		if rec := doRoutineRequest(t, handler, http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	rec = httptest.NewRecorder()
	(&Server{}).handleRoutines(rec, httptest.NewRequest(http.MethodGet, "/api/routines", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing service: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRoutineHTTPRejectsOverlappingSharedSessionRuns(t *testing.T) {
	_, handler, _ := routineHTTPServer(t)
	body := strings.Replace(validRoutineBody, `"sessionMode":"new"`, `"sessionMode":"reuse"`, 1)
	created := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", body)
	var routine state.Routine
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &routine) != nil {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	path := "/api/routines/" + routine.ID + "/run"
	if rec := doRoutineRequest(t, handler, http.MethodPost, path, ""); rec.Code != http.StatusOK {
		t.Fatalf("first run: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodPost, path, ""); rec.Code != http.StatusConflict {
		t.Fatalf("overlapping run: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRoutineHTTPUnexpectedStoreError(t *testing.T) {
	srv, handler, _ := routineHTTPServer(t)
	created := doRoutineRequest(t, handler, http.MethodPost, "/api/routines", validRoutineBody)
	var routine state.Routine
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &routine) != nil {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	if err := srv.stateDB.Close(); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/routines", ""},
		{http.MethodPost, "/api/routines", validRoutineBody},
		{http.MethodGet, "/api/routines/" + routine.ID, ""},
		{http.MethodPut, "/api/routines/" + routine.ID, validRoutineBody},
		{http.MethodDelete, "/api/routines/" + routine.ID, ""},
		{http.MethodPost, "/api/routines/" + routine.ID + "/run", ""},
		{http.MethodGet, "/api/routines/" + routine.ID + "/history", ""},
	} {
		if rec := doRoutineRequest(t, handler, request.method, request.path, request.body); rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s %s: %d %s", request.method, request.path, rec.Code, rec.Body.String())
		}
	}
}
