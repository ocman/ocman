package codextransport

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/ocman/ocman/internal/platforms/codex/acptest"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
)

// TestMain doubles as both executables the platform launches: `ocman
// provider codex` (through the fake tmux) and, with FAKE_CODEX set, the
// `codex` CLI that supervisor and history reader start.
func TestMain(m *testing.M) {
	switch {
	case RunProviderCommand(os.Args[1:]):
		os.Exit(0)
	case os.Getenv("FAKE_CODEX") == "":
		os.Exit(m.Run())
	case len(os.Args) == 1 && os.Getenv("FAKE_CODEX_ACP_STATE") != "":
		acptest.Serve(os.Stdin, os.Stdout, acptest.Config{State: os.Getenv("FAKE_CODEX_ACP_STATE"), Permission: os.Getenv("FAKE_CODEX_APPROVAL") != ""})
	case len(os.Args) > 1 && os.Args[1] == "--version":
		fmt.Println("codex-cli 9.9.9")
	case len(os.Args) > 1 && os.Args[1] == "app-server":
		if path := os.Getenv("FAKE_CODEX_LAUNCHES"); path != "" {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				panic(err)
			}
			_, _ = fmt.Fprintf(f, "%s %q\n", os.Getenv("CODEX_HOME"), os.Args[1:])
			_ = f.Close()
		}
		s := codextest.Serve(os.Stdin, os.Stdout)
		fakeCodex(s)
		if path := os.Getenv("FAKE_CODEX_ACP_STATE"); path != "" {
			acptest.ConfigureNative(s, path)
		}
		<-s.Done()
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func thread(id string) map[string]any {
	return map[string]any{"id": id, "cwd": "/repo", "source": "appServer", "status": map[string]any{"type": "idle"},
		"createdAt": 1, "updatedAt": 2, "modelProvider": "openai", "preview": "hello", "turns": []any{}}
}

// fakeCodex supplies native metadata only. Execution belongs to the ACP peer.
func fakeCodex(s *codextest.Server) {
	s.Handle(codexappserver.MethodSkillsList, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{map[string]any{"cwd": "/repo", "skills": []any{map[string]any{"name": "pdf", "description": "PDF", "path": "/s/pdf", "enabled": true}}}}}, nil
	})
	s.Handle(codexappserver.MethodAccountRead, func(json.RawMessage) (any, error) {
		return map[string]any{"account": map[string]any{"type": "chatgpt", "planType": "plus"}}, nil
	})
	s.Handle(codexappserver.MethodRateLimits, func(json.RawMessage) (any, error) {
		return map[string]any{"rateLimits": map[string]any{"primary": map[string]any{"usedPercent": 10, "windowDurationMins": 300, "resetsAt": 5}}}, nil
	})
	s.Handle(codexappserver.MethodThreadList, func(raw json.RawMessage) (any, error) {
		var p codexappserver.ThreadListParams
		_ = json.Unmarshal(raw, &p)
		if p.Archived != nil && *p.Archived {
			return map[string]any{"data": []any{}}, nil
		}
		return map[string]any{"data": []any{thread("thr")}}, nil
	})
	s.Handle(codexappserver.MethodThreadRead, func(json.RawMessage) (any, error) { return map[string]any{"thread": thread("thr")}, nil })
	s.Handle(codexappserver.MethodThreadTurns, func(json.RawMessage) (any, error) { return map[string]any{"data": []any{}}, nil })
	s.Handle(codexappserver.MethodThreadItems, func(json.RawMessage) (any, error) { return map[string]any{"data": []any{}}, nil })
	s.Handle(codexappserver.MethodModelList, func(json.RawMessage) (any, error) {
		return map[string]any{"data": []any{map[string]any{"id": "m", "model": "gpt-test", "isDefault": true}}}, nil
	})
}

// installFakeTmux puts the repository's provider tmux shim first on PATH. It
// runs launched supervisors as plain children; they are killed at cleanup.
func installFakeTmux(t *testing.T) string {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	script, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "hostsvc", "local", "testdata", "provider-tmux"))
	if err != nil {
		t.Fatal(err)
	}
	bin, pids := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), script, 0o700); err != nil {
		t.Fatal(err)
	}
	// Pay macOS's first-execution check now, not inside tmux.Run's 2 s limit.
	_ = exec.Command(filepath.Join(bin, "tmux"), "-L", "x", "-f", "/dev/null", "has-session", "-t", "warm").Run()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OCMAN_FAKE_PROVIDER_PID_DIR", pids)
	t.Cleanup(func() {
		paths, _ := filepath.Glob(filepath.Join(pids, "*.pid"))
		for _, path := range paths {
			data, _ := os.ReadFile(path)
			if pid, _ := strconv.Atoi(string(data)); pid > 0 {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	})
	return pids
}
