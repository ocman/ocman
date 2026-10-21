package autoapprove

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	"github.com/NoUseFreak/ocman/internal/platforms/platformtest"
)

func TestCreateJudgeSessionHiddenAtCreation(t *testing.T) {
	const title = "(auto-approve subagent)"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/session" {
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			// OpenCode publishes session.created before returning this response.
			// The sidebar's subagent title filter must already apply then.
			if payload["title"] != title {
				t.Errorf("session visible at creation: title = %q, want %q", payload["title"], title)
			}
			if payload["directory"] != "/project" {
				t.Errorf("directory = %q, want /project", payload["directory"])
			}
			_, _ = w.Write([]byte(`{"id":"judge"}`))
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	j := &PermissionJudge{httpClient: server.Client()}
	id, err := j.createSession(context.Background(), u.Port(), "/project", title)
	if err != nil || id != "judge" {
		t.Fatalf("createSession = %q, %v", id, err)
	}
}

func TestPermissionJudgeAuthenticatesRequests(t *testing.T) {
	const password = "judge-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || pass != password {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"judge"}`))
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)

	j := newPermissionJudge(ocapi.New(password))
	if id, err := j.createSession(context.Background(), u.Port(), "/project", "judge"); err != nil || id != "judge" {
		t.Fatalf("authenticated judge request failed: %q %v", id, err)
	}
}

// TestNewPermissionJudgeDefaults verifies the constructor wires the
// built-in model + a non-nil HTTP client and port-discovery func.
func TestNewPermissionJudgeDefaults(t *testing.T) {
	j := newPermissionJudge(ocapi.New(""))
	if j.modelProvider != judgeModelProvider || j.modelID != judgeModelID {
		t.Errorf("model = %q/%q, want %q/%q", j.modelProvider, j.modelID, judgeModelProvider, judgeModelID)
	}
	if j.httpClient == nil {
		t.Error("httpClient must be non-nil")
	}
	if svc := NewService(Deps{OpenCodeAuth: ocapi.New("")}); svc.judge.openCodePort == nil {
		t.Error("openCodePort discovery func must be non-nil")
	}
	if svc := NewService(Deps{OpenCodeAuth: ocapi.New(""), OpenCodeDisabled: true}); svc.judge.openCodePort != nil {
		t.Error("disabled OpenCode must not resolve ports")
	}
}

// Ports come from the registered adapter's own discovery, never a
// process-wide one.
func TestServiceDiscoversThroughOpenCodeAdapter(t *testing.T) {
	none := NewService(Deps{OpenCodeAuth: ocapi.New("")})
	if none.openCodePort("/repo") != "" || len(none.discoverOpenCodePorts()) != 0 {
		t.Fatal("no adapter must mean no ports")
	}
	adapter := opencode.New(nil, nil)
	adapter.Discovery().SetManagedPortSource(func(context.Context) map[string]string { return map[string]string{"/repo": "4500"} })
	svc := NewService(Deps{OpenCodeAuth: ocapi.New(""), OpencodePlatform: func() platforms.Platform { return openCodePlatform{Adapter: adapter} }})
	if got := svc.openCodePort("/repo"); got != "4500" {
		t.Fatalf("openCodePort = %q, want 4500", got)
	}
	if got := svc.discoverOpenCodePorts()["/repo"]; got != "4500" {
		t.Fatalf("discoverOpenCodePorts = %q, want 4500", got)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"", "b", "c"}, "b"},
		{[]string{"a", "b"}, "a"},
		{[]string{"", ""}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := firstNonEmpty(c.in...); got != c.want {
			t.Errorf("firstNonEmpty(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// openCodePlatform keeps the native adapter's optional capabilities visible
// while satisfying the application contract in tests.
type openCodePlatform struct {
	*opencode.Adapter
	platformtest.NoBrowsing
}
