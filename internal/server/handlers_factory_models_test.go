package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type fakeModelsFactory struct {
	*fakeFactoryService
	epicID string
	models model.EpicModels
}

func (f *fakeModelsFactory) SetEpicModels(_ context.Context, epicID string, models model.EpicModels) (model.EpicModels, error) {
	if models.Plan == "bad" {
		return model.EpicModels{}, fmt.Errorf("%w: bad", factory.ErrInvalidRequest)
	}
	f.epicID, f.models = epicID, models
	return models, nil
}

func TestFactoryEpicModelsRoute(t *testing.T) {
	post := func(srv *Server, body string) *httptest.ResponseRecorder {
		mux, err := srv.routes()
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/factory/epics/fac-1/models", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	svc := &fakeModelsFactory{fakeFactoryService: &fakeFactoryService{}}
	srv := New(nil, nil, "", nil, nil)
	srv.factory = svc
	rec := post(srv, `{"plan":"p/plan","implementation":"p/impl","verification":"p/verify"}`)
	if rec.Code != http.StatusOK || svc.epicID != "fac-1" || svc.models != (model.EpicModels{Plan: "p/plan", Implementation: "p/impl", Verification: "p/verify"}) || !strings.Contains(rec.Body.String(), `"implementation":"p/impl"`) {
		t.Fatalf("set models = %d %s; %#v", rec.Code, rec.Body.String(), svc.models)
	}
	if rec := post(srv, `{"plan":"bad"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid models = %d: %s", rec.Code, rec.Body.String())
	}
	srv.factory = &fakeFactoryService{}
	if rec := post(srv, `{}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported factory = %d: %s", rec.Code, rec.Body.String())
	}
}
