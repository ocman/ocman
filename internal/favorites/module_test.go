package favorites

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func registered(id string) bool { return id == "opencode" }

func call(m *Module, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(rec, req)
	return rec
}

func list(t *testing.T, m *Module) []entry {
	t.Helper()
	w := call(m, http.MethodGet, "/api/favorites?platform=opencode", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w.Code, w.Body.String())
	}
	var out []entry
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAddListRemoveRoundTrip(t *testing.T) {
	m := NewModule(Deps{Store: statetest.Open(t), Registered: registered})
	if got := list(t, m); len(got) != 0 {
		t.Fatalf("initial favorites = %+v", got)
	}
	body := `{"platform":" opencode ","provider":"anthropic","model":" claude-opus-4 "}`
	for i := 0; i < 3; i++ { // idempotent
		if w := call(m, http.MethodPost, "/api/favorites", body); w.Code != http.StatusNoContent {
			t.Fatalf("POST #%d = %d: %s", i, w.Code, w.Body.String())
		}
	}
	want := entry{Platform: "opencode", Provider: "anthropic", Model: "claude-opus-4"}
	if got := list(t, m); len(got) != 1 || got[0] != want {
		t.Fatalf("favorites = %+v, want [%+v]", got, want)
	}
	if w := call(m, http.MethodDelete, "/api/favorites", body); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", w.Code, w.Body.String())
	}
	if got := list(t, m); len(got) != 0 {
		t.Fatalf("favorites after DELETE = %+v", got)
	}
}

func TestRejectsInvalidRequests(t *testing.T) {
	m := NewModule(Deps{Store: statetest.Open(t), Registered: registered})
	tests := []struct {
		name, method, target, body string
		want                       int
	}{
		{"missing platform", http.MethodGet, "/api/favorites", "", http.StatusBadRequest},
		{"unknown platform", http.MethodGet, "/api/favorites?platform=martian", "", http.StatusBadRequest},
		{"missing model", http.MethodPost, "/api/favorites", `{"platform":"opencode","provider":"anthropic"}`, http.StatusBadRequest},
		{"unknown body platform", http.MethodDelete, "/api/favorites", `{"platform":"martian","model":"m"}`, http.StatusBadRequest},
		{"malformed body", http.MethodPost, "/api/favorites", `{`, http.StatusBadRequest},
		{"wrong method", http.MethodPut, "/api/favorites?platform=opencode", "", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if w := call(m, tt.method, tt.target, tt.body); w.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestWithoutStoreOrRegistry(t *testing.T) {
	m := NewModule(Deps{Registered: registered})
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		if w := call(m, method, "/api/favorites?platform=opencode", `{"platform":"opencode","model":"m"}`); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without store = %d", method, w.Code)
		}
	}
	m = NewModule(Deps{Store: statetest.Open(t)})
	if w := call(m, http.MethodGet, "/api/favorites?platform=opencode", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("without registry = %d", w.Code)
	}
}

type failingStore struct{}

var errStore = errors.New("store down")

func (failingStore) ModelFavorites(context.Context, string) ([]state.ModelFavorite, error) {
	return nil, errStore
}
func (failingStore) AddModelFavorite(context.Context, string, string, string) error {
	return errStore
}
func (failingStore) RemoveModelFavorite(context.Context, string, string, string) error {
	return errStore
}

func TestStoreErrorsAre500(t *testing.T) {
	m := NewModule(Deps{Store: failingStore{}, Registered: registered})
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		if w := call(m, method, "/api/favorites?platform=opencode", `{"platform":"opencode","model":"m"}`); w.Code != http.StatusInternalServerError {
			t.Errorf("%s = %d", method, w.Code)
		}
	}
}
