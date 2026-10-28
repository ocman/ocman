package pluginsmod

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestPluginActionUnavailableAndErrors(t *testing.T) {
	m := NewModule(Deps{})
	r := plugins.ActionRequest{PluginID: "org.example.test", ActionID: "run", OperationID: "op", Placement: "global", Surface: "command-palette"}
	if got := m.Actions().Invoke(t.Context(), r); got.Error.Category != plugins.ErrorUnavailable {
		t.Fatal(got)
	}
	w := httptest.NewRecorder()
	m.handlePluginActions(w, httptest.NewRequest(http.MethodGet, "/?placement=global&surface=command-palette", nil))
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	for _, tc := range []struct {
		category plugins.ErrorCategory
		status   int
	}{
		{plugins.ErrorInvalidArgument, 400}, {plugins.ErrorPermissionDenied, 403}, {plugins.ErrorNotFound, 404}, {plugins.ErrorConflict, 409}, {plugins.ErrorUnavailable, 503}, {plugins.ErrorDeadlineExceeded, 504}, {plugins.ErrorCancelled, 408}, {plugins.ErrorInternal, 500},
	} {
		w := httptest.NewRecorder()
		writeActionResponse(w, plugins.ActionResponse{Error: &plugins.WireError{Category: tc.category}})
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.category, w.Code)
		}
	}
	db, err := state.Open(statetest.Path(t, Schema), Schema)
	if err != nil {
		t.Fatal(err)
	}
	m = NewModule(Deps{Store: db})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	m.handlePluginActions(w, httptest.NewRequest(http.MethodGet, "/?placement=global&surface=command-palette", nil))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	if got := m.Actions().Invoke(context.Background(), r); got.Error.Category != plugins.ErrorInternal {
		t.Fatal(got)
	}
}

func TestPluginResponseIsBounded(t *testing.T) {
	if got := pluginResponse(strings.Repeat("x", remote.MaxPluginResponseBytes), nil); got.Error == nil || got.Error.Category != plugins.ErrorUnavailable {
		t.Fatal("unbounded catalog response")
	}
}
