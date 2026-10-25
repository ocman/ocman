package claudecode

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
)

func TestNativePermissionsRouteToOwningSupervisor(t *testing.T) {
	var written atomic.Bool
	a, _ := reloadTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sessions":
			_, _ = w.Write([]byte(`[]`))
		case "/sessions/s/native-permission":
			if r.Method == http.MethodPost {
				var req platforms.SetPermissionRulesRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				written.Store(req.NativeMode != nil && *req.NativeMode == "auto" && req.Rules == nil)
				w.WriteHeader(http.StatusNoContent)
			} else {
				_, _ = w.Write([]byte(`{"mode":"auto"}`))
			}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}, platforms.Capabilities{NativeAutoPermission: true})
	p := &claudePlatform{Adapter: a, projects: &claudeProjects{
		roots:    map[string]*claudeProject{"/repo": {watched: watched{adapter: a}}},
		sessions: map[string]*provider.Adapter{"s": a},
	}}
	state, err := p.NativePermissionState(t.Context(), "s")
	if err != nil || state.Mode != "auto" {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	auto := "auto"
	if err := p.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: "s", NativeMode: &auto}); err != nil {
		t.Fatal(err)
	}
	if !written.Load() {
		t.Fatal("native mode did not reach the owning supervisor")
	}
	rules, err := provider.OpenRules("")
	if err != nil {
		t.Fatal(err)
	}
	a.SetRules(rules)
	if err := p.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: "s", Rules: []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := p.PermissionRules(t.Context(), "s")
	if err != nil || len(got) != 1 {
		t.Fatalf("rules=%v err=%v", got, err)
	}
}
