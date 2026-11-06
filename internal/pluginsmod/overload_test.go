package pluginsmod

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestPluginResponseErrorTranslation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		category plugins.ErrorCategory
		status   int
	}{
		{"busy", plugins.ErrBusy, plugins.ErrorUnavailable, 503},
		{"wrapped busy", fmt.Errorf("private diagnostic: %w", plugins.ErrBusy), plugins.ErrorUnavailable, 503},
		{"offline", platforms.ErrRemoteOffline, plugins.ErrorUnavailable, 503},
		{"invalid", state.ErrPluginInvalid, plugins.ErrorInvalidArgument, 400},
		{"denied", &plugins.WireError{Category: plugins.ErrorPermissionDenied}, plugins.ErrorPermissionDenied, 403},
		{"missing", state.ErrPluginNotFound, plugins.ErrorNotFound, 404},
		{"conflict", errPluginConflict, plugins.ErrorConflict, 409},
		{"cancelled", context.Canceled, plugins.ErrorCancelled, 408},
		{"deadline", context.DeadlineExceeded, plugins.ErrorDeadlineExceeded, 504},
		{"internal", errors.New("private diagnostic"), plugins.ErrorInternal, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := pluginResponse(nil, tc.err)
			if response.Error == nil || response.Error.Category != tc.category {
				t.Fatalf("response: %+v", response)
			}
			w := httptest.NewRecorder()
			writePluginResponse(w, response)
			want := fmt.Sprintf(`{"error":{"category":%q}}`, tc.category)
			if w.Code != tc.status || strings.TrimSpace(w.Body.String()) != want {
				t.Fatalf("response: %d %s", w.Code, w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("error response is cacheable")
			}
		})
	}
}

func TestPluginUIOverloadHTTP(t *testing.T) {
	m, candidate := startUIHelper(t, statetest.Open(t, Schema), false)
	p := m.processes[candidate.Description.ID]
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Hold exactly the advertised slots in an isolated synthetic process.
	for i := 0; i < candidate.Description.MaxConcurrency; i++ {
		_, err := p.Call(ctx, plugins.Call{OperationID: fmt.Sprintf("read-%d", i), Capability: "ui", Version: plugins.UICapability.Version,
			Method: "call", Params: []byte(`{"method":"items.wait"}`), DeadlineUnixMS: time.Now().Add(10 * time.Second).UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/plugins/ui/call", strings.NewReader(`{"pluginId":"org.example.ui","method":"items.list"}`))
	r.Header.Set("Content-Type", "application/json")
	moduletest.Mount(m).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || strings.TrimSpace(w.Body.String()) != `{"error":{"category":"unavailable"}}` {
		t.Fatalf("overload: %d %s", w.Code, w.Body)
	}
	if h := p.Health(); h.Status != "ready" || h.RestartCount != 0 {
		t.Fatalf("overload changed process health: %+v", h)
	}
}
