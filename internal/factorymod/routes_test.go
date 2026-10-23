package factorymod

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/module"
)

type muxRouter struct{ *http.ServeMux }

func (r muxRouter) Handle(pattern string, h http.HandlerFunc)       { r.HandleFunc(pattern, h) }
func (r muxRouter) Local(pattern string, h http.HandlerFunc)        { r.HandleFunc(pattern, h) }
func (r muxRouter) Setting(pattern string, h http.HandlerFunc)      { r.HandleFunc(pattern, h) }
func (r muxRouter) LocalSetting(pattern string, h http.HandlerFunc) { r.HandleFunc(pattern, h) }

type routeService struct {
	Service
	epicID string
	paused bool
}

func (s *routeService) GetWorkEpic(_ context.Context, id string) (factory.WorkEpic, error) {
	s.epicID = id
	return factory.WorkEpic{ID: id}, nil
}

func (s *routeService) ListFormulas(context.Context) ([]factory.NativeFormulaView, error) {
	return nil, nil
}

func (s *routeService) SetEpicPaused(_ context.Context, id string, paused bool) error {
	s.epicID, s.paused = id, paused
	return nil
}

// routeMux mounts Factory beside a catch-all, as the server does, with
// guards that count how often a request reached the localhost check.
func routeMux(t *testing.T, svc Service) (*http.ServeMux, *int) {
	t.Helper()
	local := 0
	pass := func(h http.HandlerFunc) http.HandlerFunc { return h }
	guards := module.Guards{Auth: pass, Localhost: func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { local++; h(w, r) }
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	NewModule(Deps{Service: svc, Guards: guards}).Routes(muxRouter{mux})
	return mux, &local
}

func serve(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestFactoryRoutesAnswerWrongMethodWith405DespiteCatchAll(t *testing.T) {
	mux, _ := routeMux(t, &routeService{})
	for _, tt := range []struct{ method, path, allow string }{
		{http.MethodGet, "/api/factory/epics/e/pause", "POST"},
		{http.MethodPost, "/api/factory/epics/e/usage", "GET, HEAD"},
		{http.MethodPut, "/api/factory/epics/e/proposals", "GET, HEAD, POST"},
		{http.MethodGet, "/api/factory/recovery-gates/g/resume", "POST"},
		{http.MethodDelete, "/api/factory/configuration", "GET, HEAD, POST"},
	} {
		rec := serve(mux, tt.method, tt.path)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != tt.allow {
			t.Errorf("%s %s = %d, Allow %q; want 405, %q", tt.method, tt.path, rec.Code, rec.Header().Get("Allow"), tt.allow)
		}
	}
}

func TestFactoryRoutesUnknownSubpathIs404(t *testing.T) {
	mux, _ := routeMux(t, &routeService{})
	for _, path := range []string{"/api/factory/epics/e/unknown", "/api/factory/epics/e/pause/extra", "/api/factory/nothing"} {
		if rec := serve(mux, http.MethodPost, path); rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, rec.Code)
		}
	}
}

func TestFactoryRoutesDecodePathValues(t *testing.T) {
	svc := &routeService{}
	mux, local := routeMux(t, svc)
	if rec := serve(mux, http.MethodGet, "/api/factory/epics/a%2Fb"); rec.Code != http.StatusOK || svc.epicID != "a/b" {
		t.Fatalf("GET epic = %d, id %q", rec.Code, svc.epicID)
	}
	if *local != 0 {
		t.Fatalf("read reached the localhost guard %d times", *local)
	}
	for _, tt := range []struct {
		verb   string
		paused bool
	}{{"pause", true}, {"resume", false}} {
		if rec := serve(mux, http.MethodPost, "/api/factory/epics/x%2Fy/"+tt.verb); rec.Code != http.StatusNoContent || svc.epicID != "x/y" || svc.paused != tt.paused {
			t.Fatalf("%s = %d, id %q, paused %v", tt.verb, rec.Code, svc.epicID, svc.paused)
		}
	}
	if *local != 2 {
		t.Fatalf("mutations reached the localhost guard %d times, want 2", *local)
	}
}

func TestFactoryRoutesFormulaListAcceptsTrailingSlash(t *testing.T) {
	mux, _ := routeMux(t, &routeService{})
	for _, path := range []string{"/api/factory/formulas", "/api/factory/formulas/"} {
		if rec := serve(mux, http.MethodGet, path); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

// Invalid gate and verb segments are rejected before the localhost guard.
func TestFactoryRoutesRejectInvalidActions(t *testing.T) {
	mux, local := routeMux(t, &routeService{})
	for _, path := range []string{"/api/factory/recovery-gates/g/bogus", "/api/factory/authority-gates/g/bogus", "/api/factory/project-gates/g/bogus"} {
		if rec := serve(mux, http.MethodPost, path); rec.Code != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400", path, rec.Code)
		}
	}
	if *local != 0 {
		t.Fatalf("invalid actions reached the localhost guard %d times", *local)
	}
}
