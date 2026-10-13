package local

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

func TestCullOpenCodeRequiresAuthoritativeIdle(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		code           int
		eligible       bool
		stopErr        error
		wantStop       bool
	}{
		{"idle", `{}`, 200, true, nil, true},
		{"busy subagent", `{"unknown-child":{"type":"busy"}}`, 200, true, nil, false},
		{"retry", `{"s":{"type":"retry"}}`, 200, true, nil, false},
		{"unknown status", `{"s":{"type":"future"}}`, 200, true, nil, false},
		{"bad status", `null`, 200, true, nil, false},
		{"bad JSON", `invalid`, 200, true, nil, false},
		{"API error", `{}`, 500, true, nil, false},
		{"reopened", `{}`, 200, false, nil, false},
		{"stop failure", `{}`, 200, true, errors.New("stop failed"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := map[string]bool{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/session/status" {
					t.Error(r.URL.Path)
				}
				seen[r.URL.Query().Get("directory")] = true
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			store := newFakeStore()
			store.rows["/repo"] = ManagedInstance{Endpoint: server.URL, RuntimeID: "managed"}
			runtime := &fakeRuntime{stopErr: tc.stopErr}
			host := New(Deps{Runtime: runtime, ManagedStore: store})
			roots, err := host.ManagedIdleProjects(t.Context())
			if err != nil || len(roots) != 1 {
				t.Fatalf("roots=%v err=%v", roots, err)
			}
			stopped, _ := host.CullIdleProject(t.Context(), "/repo", []string{"/worktree", "/worktree"}, func(context.Context) (bool, error) { return tc.eligible, nil })
			if stopped != tc.wantStop || store.has("/repo") == stopped {
				t.Fatalf("stopped=%v retained=%v", stopped, store.has("/repo"))
			}
			if tc.wantStop && (!seen["/repo"] || !seen["/worktree"]) {
				t.Fatal("missed a project directory")
			}
		})
	}
}

func TestCullOpenCodeThenEnsureWakesProject(t *testing.T) {
	repo := initRepo(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	store, runtime := newFakeStore(), &fakeRuntime{endpoint: server.URL}
	host := New(Deps{Runtime: runtime, ManagedStore: store})
	first, err := host.EnsureRuntime(t.Context(), "opencode", repo)
	if err != nil {
		t.Fatal(err)
	}
	stopped, err := host.CullIdleProject(t.Context(), first.RepoRoot, nil, func(context.Context) (bool, error) { return true, nil })
	if err != nil || !stopped {
		t.Fatalf("stop=%v err=%v", stopped, err)
	}
	second, err := host.EnsureRuntime(t.Context(), "opencode", repo)
	if err != nil || !second.Launched || runtime.launchCount() != 2 {
		t.Fatalf("wake=%+v err=%v", second, err)
	}
}

func TestCullOpenCodeV2IsExcluded(t *testing.T) {
	ocv2.SetInstalledV2(true)
	t.Cleanup(func() { ocv2.SetInstalledV2(false) })
	store, runtime := newFakeStore(), &fakeRuntime{}
	store.rows[machineRoot()] = ManagedInstance{RuntimeID: "managed"}
	host := New(Deps{Runtime: runtime, ManagedStore: store})
	roots, err := host.ManagedIdleProjects(t.Context())
	if err != nil || len(roots) != 0 {
		t.Fatalf("roots=%v err=%v", roots, err)
	}
	stopped, err := host.CullIdleProject(t.Context(), machineRoot(), nil, func(context.Context) (bool, error) { t.Fatal("checked v2"); return true, nil })
	if err != nil || stopped || runtime.stopCount() != 0 {
		t.Fatal("culled v2")
	}
}

func TestCullOpenCodePreservesUnmanagedAndProbeFailures(t *testing.T) {
	store := newFakeStore()
	store.rows["/unmanaged"] = ManagedInstance{}
	store.rows["/failed"] = ManagedInstance{RuntimeID: "managed"}
	runtime := &fakeRuntime{probe: func(*ocruntime.Instance) bool { return false }}
	host := New(Deps{Runtime: runtime, ManagedStore: store})
	for _, root := range []string{"/unmanaged", "/failed", "/missing"} {
		stopped, _ := host.CullIdleProject(t.Context(), root, nil, func(context.Context) (bool, error) { return true, nil })
		if stopped || runtime.stopCount() != 0 {
			t.Fatal("stopped uncertain instance")
		}
	}
	if roots, err := New(Deps{}).ManagedIdleProjects(t.Context()); err != nil || len(roots) != 0 {
		t.Fatal(roots, err)
	}
	_ = hostsvc.IdleProjectRuntime(host)
}
