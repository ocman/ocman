package ocapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

// TestMain pins the installed-OpenCode check so no test runs the real
// `opencode --version`.
func TestMain(m *testing.M) {
	ocv2.SetInstalledV2(false)
	os.Exit(m.Run())
}

func TestAddServerEnvAndEnabled(t *testing.T) {
	env := map[string]string{}
	New("").AddServerEnv(env)
	if len(env) != 0 || New("").Enabled() {
		t.Fatalf("disabled auth: env %v, Enabled %v", env, New("").Enabled())
	}
	a := New("pw")
	a.AddServerEnv(env)
	want := map[string]string{
		"OPENCODE_SERVER_USERNAME": DefaultUsername,
		"OPENCODE_SERVER_PASSWORD": "pw",
		"OPENCODE_PASSWORD":        "pw",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, env[k], v)
		}
	}
	if !a.Enabled() {
		t.Error("Enabled() = false with a password")
	}
}

// With v2 installed, a v1 route against a v2 server is translated and
// every upstream call, the dialect probe included, carries Basic Auth.
func TestTransportV2TranslatesWithBasicAuth(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if u, p, ok := r.BasicAuth(); !ok || u != DefaultUsername || p != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/info":
			_, _ = w.Write([]byte(`{"version":"2.0.1"}`))
		case "/api/session/active":
			_, _ = w.Write([]byte(`{"data":{"ses_1":{}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	ocv2.ForgetHost(srv.Listener.Addr().String())

	client := &http.Client{Transport: New("pw").Transport(nil)}
	resp, err := client.Get(srv.URL + "/session/status")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	var got map[string]map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got["ses_1"]["type"] != "busy" {
		t.Errorf("translated status = %v, want ses_1 busy", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/api/info" || paths[1] != "/api/session/active" {
		t.Errorf("upstream paths = %v", paths)
	}
}
