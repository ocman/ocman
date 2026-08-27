package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
)

type fakeFactoryService struct{ status factory.Status }

func (f fakeFactoryService) Start(context.Context) error           { return nil }
func (f fakeFactoryService) Close()                                {}
func (f fakeFactoryService) Status(context.Context) factory.Status { return f.status }

func TestFactoryStatusRouteIsAuthenticatedAndReadOnly(t *testing.T) {
	auth := newTestAuth(t, "hunter2")
	want := factory.Status{
		Health: factory.HealthHealthy, Idle: true, ReadOnly: true,
		Beads: factory.BeadsHealth{Usable: true, Version: "1.1.0", ContractVersion: 1},
	}
	srv := New(nil, nil, "", nil, auth)
	srv.factory = fakeFactoryService{status: want}
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
	if got != want {
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
