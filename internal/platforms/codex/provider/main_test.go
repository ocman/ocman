package codexprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	acpexec "github.com/ocman/ocman/internal/platforms/acp"
	"github.com/ocman/ocman/internal/platforms/codex/acptest"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// TestMain doubles as a fake `codex` executable when FAKE_CODEX is set: it
// answers --version and serves the scripted fake as `app-server`.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CODEX") == "" {
		os.Exit(m.Run())
	}
	switch {
	case len(os.Args) == 1 && os.Getenv("FAKE_CODEX_ACP_STATE") != "":
		acptest.Serve(os.Stdin, os.Stdout, acptest.Config{State: os.Getenv("FAKE_CODEX_ACP_STATE"), Permission: os.Getenv("FAKE_CODEX_APPROVAL") != "", Hang: os.Getenv("FAKE_CODEX_ACP_HANG") != "", Exit: os.Getenv("FAKE_CODEX_ACP_EXIT") != "", Question: os.Getenv("FAKE_CODEX_ACP_QUESTION") != ""})
	case len(os.Args) > 1 && os.Args[1] == "--version":
		fmt.Println("codex-cli 9.9.9")
	case len(os.Args) > 1 && os.Args[1] == "app-server":
		s := codextest.Serve(os.Stdin, os.Stdout)
		scripted(s)
		if path := os.Getenv("FAKE_CODEX_ACP_STATE"); path != "" {
			acptest.ConfigureNative(s, path)
		}
		if os.Getenv("FAKE_CODEX") == "exit" {
			s.Handle("test/exit", func(json.RawMessage) (any, error) {
				go func() { time.Sleep(100 * time.Millisecond); os.Exit(0) }()
				return map[string]any{"pid": os.Getpid()}, nil
			})
		}
		<-s.Done()
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestNewSupervisorSpawnsCodex(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CODEX", "1")
	t.Setenv("FAKE_CODEX_ACP_STATE", filepath.Join(t.TempDir(), "state.json"))
	dir, err := os.MkdirTemp("", "cx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup, backend, err := newSupervisor(ctx, Options{CLI: exe, ACPCommand: acpexec.Command{Executable: exe}, Dir: dir, Version: "test"}, filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); backend.closeClient() })
	rec := httptest.NewRecorder()
	sup.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello", nil))
	var hello providerprotocol.Hello
	if err := json.Unmarshal(rec.Body.Bytes(), &hello); err != nil {
		t.Fatal(err)
	}
	if hello.PlatformID != "codex" || hello.CLIVersion != "9.9.9" || !hello.Capabilities.Composer || hello.Capabilities.Revert || !hello.Capabilities.Compact || !hello.Capabilities.ModelCatalog {
		t.Fatalf("hello = %+v", hello)
	}
	body, _ := json.Marshal(platforms.SendMessageRequest{Message: "look",
		Images: []platforms.ImageAttachment{{URL: "data:image/png;base64,AA"}}})
	rec = httptest.NewRecorder()
	sup.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/sessions/thr/send", bytes.NewReader(body)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("send through a spawned app-server = %d %s", rec.Code, rec.Body.String())
	}
	if v := cliVersion(Options{}); v != "unavailable" {
		t.Fatalf("version without a CLI = %q", v)
	}
	if v := codexVersion(filepath.Join(dir, "missing")); v != "unavailable" {
		t.Fatalf("version of a missing CLI = %q", v)
	}
}

func TestPublicSupervisorAlwaysSelectsACP(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sup, err := NewSupervisor(ctx, Options{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	sup.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello", nil))
	var hello providerprotocol.Hello
	if err := json.Unmarshal(rec.Body.Bytes(), &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Capabilities.Ownership || hello.Capabilities.ShellExec || hello.Capabilities.ForkAtMessage {
		t.Fatalf("native execution was selected: %+v", hello.Capabilities)
	}
}
