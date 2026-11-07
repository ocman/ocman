package sessionlifecycle

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

func explicitRulesModule(t *testing.T, created *[][]platforms.PermissionRule, defaultsRead *bool) *Module {
	t.Helper()
	svc := &startSessions{create: func(_ platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
		*created = append(*created, rules)
		return &platforms.CreateSessionResponse{ID: "child"}, nil
	}}
	host := &startHost{steps: &[]string{}}
	return NewModule(Deps{
		Sessions: func() Sessions { return svc },
		Owner:    func(http.ResponseWriter, string, string) (hostsvc.Host, bool) { return host, true },
		CreatePlatform: func(_ context.Context, _ hostsvc.Host, id, model string) (string, string, error) {
			return id, model, nil
		},
		PermissionDefaults: func(context.Context, string, string) ([]platforms.PermissionRule, error) {
			*defaultsRead = true
			return []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}}, nil
		},
		Broadcast: func(string, []byte) {},
	})
}

// A posture chosen in the composer replaces project defaults at creation.
func TestStartExplicitPermissionRulesOverrideDefaults(t *testing.T) {
	var created [][]platforms.PermissionRule
	var defaultsRead bool
	m := explicitRulesModule(t, &created, &defaultsRead)
	body := `{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode","permissionRules":[{"permission":"edit","pattern":"*","action":"deny"},{"permission":"bash","pattern":"*","action":"deny"}]}`
	w := httptest.NewRecorder()
	m.HandleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	want := []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}, {Permission: "bash", Pattern: "*", Action: "deny"}}
	if defaultsRead || len(created) != 1 || !reflect.DeepEqual(created[0], want) {
		t.Fatalf("defaultsRead=%v created=%v", defaultsRead, created)
	}
}

// An omitted posture keeps the owner/project defaults.
func TestStartWithoutPermissionRulesUsesDefaults(t *testing.T) {
	var created [][]platforms.PermissionRule
	var defaultsRead bool
	m := explicitRulesModule(t, &created, &defaultsRead)
	w := httptest.NewRecorder()
	m.HandleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(`{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode"}`)))
	if w.Code != http.StatusOK || !defaultsRead || len(created) != 1 || len(created[0]) != 1 || created[0][0].Action != "allow" {
		t.Fatalf("status=%d defaultsRead=%v created=%v", w.Code, defaultsRead, created)
	}
}

// Malformed rules fail before any session or worktree is created.
func TestStartRejectsInvalidPermissionRules(t *testing.T) {
	for _, rules := range []string{
		`[{"permission":"edit","pattern":"*","action":"maybe"}]`,
		`[{"permission":"","pattern":"*","action":"allow"}]`,
	} {
		var created [][]platforms.PermissionRule
		var defaultsRead bool
		m := explicitRulesModule(t, &created, &defaultsRead)
		w := httptest.NewRecorder()
		m.HandleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(`{"directory":"/repo","remoteId":"box","platform":"r-box:claudecode","worktree":true,"permissionRules":`+rules+`}`)))
		if w.Code != http.StatusBadRequest || len(created) != 0 {
			t.Fatalf("rules=%s status=%d created=%v", rules, w.Code, created)
		}
	}
}
