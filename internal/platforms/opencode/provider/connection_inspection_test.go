package opencode

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectConnectionUsesOnlySelectedNativeAccountMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, version, connections string
		ready, isolation           bool
	}{
		{"key", "2.1.2", `[{"type":"credential","id":"cred-a","label":"Work account","method":"key"}]`, true, true},
		{"oauth", "2.1.2", `[{"type":"credential","id":"cred-a","label":"Work account","method":"oauth"}]`, true, true},
		{"no-login", "2.1.2", `[]`, false, true},
		{"env", "2.1.2", `[{"type":"env","name":"ANTHROPIC_API_KEY"}]`, false, false},
		{"external", "2.1.2", `[{"type":"credential","id":"a","label":"External","method":"external"}]`, false, false},
		{"unknown", "2.1.2", `[{"type":"credential","id":"a","label":"Unknown","method":"future"}]`, false, false},
		{"needs-auth", "2.1.2", `[{"type":"credential","id":"a","label":"Work account","method":"oauth","status":{"status":"needs_auth","message":"secret-never-echo"}}]`, false, true},
		{"no-sibling-fallback", "2.1.2", `[{"type":"credential","id":"a","label":"Work account","method":"external"},{"type":"credential","id":"b","label":"Sibling","method":"key"}]`, false, false},
		{"unsafe-label", "2.1.2", `[{"type":"credential","id":"a","label":"sk-secret-never-echo","method":"key"}]`, false, true},
		{"control-label", "2.1.2", `[{"type":"credential","id":"a","label":"Work\nsecret-never-echo","method":"key"}]`, false, true},
		{"unsupported-version", "2.0.31", `[]`, false, false},
		{"unknown-version", "2.2.0", `[]`, false, false},
		{"malformed-version", "secret-never-echo", `[]`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, cfg := accountConfig(t, "alpha")
			a, err := NewConnection(c, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.CloseConnection() })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("inspection attempted a mutation")
				}
				_, password, _ := r.BasicAuth()
				if password != "alpha-password" {
					t.Error("inspection used another account's server auth")
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/info":
					fmt.Fprintf(w, `{"version":%q}`, tc.version)
				case "/api/integration":
					fmt.Fprintf(w, `{"data":[{"id":"anthropic","connections":%s}]}`, tc.connections)
				default:
					t.Errorf("unexpected native API %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			u, _ := url.Parse(server.URL)
			a.Discovery().SetMachineServer(u.Port())
			result, err := a.InspectConnection(t.Context())
			if err != nil || result.Ready != tc.ready || result.IsolationSupported != tc.isolation {
				t.Fatalf("inspection=%+v err=%v", result, err)
			}
			if strings.Contains(fmt.Sprint(result), "secret-never-echo") {
				t.Fatal("inspection leaked untrusted native output")
			}
			if tc.ready && (result.AccountLabel != "Work account" || result.AuthMode == "") {
				t.Fatal("missing selected account metadata")
			}
		})
	}
}

func TestInspectConnectionHasNoDefaultFallback(t *testing.T) {
	c, cfg := accountConfig(t, "alpha")
	a, err := NewConnection(c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.CloseConnection() })
	if result, err := a.InspectConnection(t.Context()); err != nil || result.Ready {
		t.Fatal("private account used a default server")
	}
	if result, err := New(nil, nil, nil, cfg.Auth).InspectConnection(t.Context()); err != nil || result.Ready {
		t.Fatal("legacy availability granted named readiness")
	}
}

func TestInspectConfigurationBeforeNativeDatabaseExists(t *testing.T) {
	_, cfg := accountConfig(t, "reservation")
	cfg.Native.DBPath = filepath.Join(t.TempDir(), "bootstrap-not-opened-by-inspector.db")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/info" {
			_, _ = w.Write([]byte(`{"version":"2.1.2"}`))
			return
		}
		if r.URL.Path != "/api/integration" {
			t.Error("unexpected inspection request")
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(server.Close)
	result, err := InspectConfiguration(t.Context(), cfg, server.URL)
	if err != nil || result.Ready || !result.IsolationSupported {
		t.Fatalf("reservation inspection=%+v %v", result, err)
	}
	for _, endpoint := range []string{"", "http://localhost:8123", "http://example.org:8123", "http://user:secret@127.0.0.1:8123", "http://127.0.0.1:8123/other"} {
		if _, err := InspectConfiguration(t.Context(), cfg, endpoint); err == nil {
			t.Fatalf("accepted implicit/non-private endpoint %q", endpoint)
		}
	}
}
