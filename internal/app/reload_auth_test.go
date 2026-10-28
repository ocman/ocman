package app

import (
	"bytes"
	"context"
	"flag"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	"github.com/ocman/ocman/internal/state"
)

type reloadAuthPlatform struct{ startupPlatform }

func (reloadAuthPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "claudecode", SourceKey: "auth-test"}, nil
}
func (reloadAuthPlatform) ReloadIdle(context.Context) (bool, error) { return true, nil }

func TestGeneratedOpenCodePasswordSurvivesReload(t *testing.T) {
	if os.Getenv("OCMAN_TEST_RELOAD_AUTH") == "1" {
		knownPlatforms["claudecode"] = func(ctx context.Context, _ *db.DB, _ *state.DB, auth ocapi.Auth) platforms.Integration {
			client := &http.Client{Transport: auth.Transport(nil), Timeout: time.Second}
			resp, err := client.Get(os.Getenv("OCMAN_TEST_AUTH_ENDPOINT"))
			if err == nil {
				_ = resp.Body.Close()
			}
			return reloadAuthPlatform{}
		}
		flag.CommandLine = flag.NewFlagSet("ocman", flag.ExitOnError)
		os.Args = []string{"ocman", "-addr=" + os.Getenv("OCMAN_TEST_AUTH_ADDR"), "-db=" + filepath.Join(os.Getenv("HOME"), "missing.db"), "-mcp-addr=", "-log-file=off", "-opencode-server-generate-password"}
		Run("test", skillCatalog)
		return
	}
	home := t.TempDir()
	store, err := state.Open(filepath.Join(home, ".local", "share", "ocman", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(t.Context(), "platforms.enabled", "claudecode"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	var mu sync.Mutex
	password := ""
	var probes, failures atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, got, ok := r.BasicAuth()
		mu.Lock()
		if password == "" {
			password = got
		}
		valid := ok && got != "" && got == password && user == ocapi.DefaultUsername
		mu.Unlock()
		if !valid {
			failures.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		}
		probes.Add(1)
	}))
	t.Cleanup(upstream.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	childCtx, cancelChild := context.WithCancel(context.Background())
	t.Cleanup(cancelChild)
	cmd := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestGeneratedOpenCodePasswordSurvivesReload$")
	cmd.Env = append(os.Environ(), "OCMAN_TEST_RELOAD_AUTH=1", "OCMAN_TEST_AUTH_ENDPOINT="+upstream.URL, "OCMAN_TEST_AUTH_ADDR="+addr,
		"HOME="+home, "XDG_DATA_HOME="+home, "XDG_CONFIG_HOME="+home, "OCMAN_PLUGIN_DIR="+home,
		"OCMAN_AUTH_PASSWORD=", "OPENCODE_SERVER_PASSWORD=", "OCMAN_RELAY_URL=", "OCMAN_PUBLIC_BASE_URL=", "WAILS_BINDINGS=", "OTEL_EXPORTER_OTLP_ENDPOINT=")
	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 1)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { err := cmd.Wait(); done <- result{append([]byte(nil), output.Bytes()...), err} }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case r := <-done:
			if t.Failed() {
				t.Logf("child output:\n%s", r.output)
			}
			if r.err != nil {
				t.Errorf("child: %v\n%s", r.err, r.output)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("child did not stop")
		}
	})
	client := &http.Client{Timeout: time.Second}
	base := "http://" + addr
	deadline := time.Now().Add(15 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/api/settings/platforms")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("child did not start")
	}
	resp, err := client.Post(base+"/api/settings/platforms", "application/json", strings.NewReader(`{"enabled":["opencode","claudecode"]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d", resp.StatusCode)
	}
	for deadline := time.Now().Add(15 * time.Second); probes.Load() < 2 && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if probes.Load() != 2 || failures.Load() != 0 {
		t.Fatalf("surviving endpoint: %d probes, %d authentication failures", probes.Load(), failures.Load())
	}
}
