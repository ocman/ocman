package codextransport

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platforms/codex/acptest"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
	"github.com/ocman/ocman/internal/testutil"
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
		requireFakeStoredAccount()
		acptest.Serve(os.Stdin, os.Stdout, acptest.Config{State: fakeAccountState(), Permission: os.Getenv("FAKE_CODEX_APPROVAL") != ""})
	case len(os.Args) > 1 && os.Args[1] == "--version":
		fmt.Println("codex-cli 9.9.9")
	case len(os.Args) > 1 && os.Args[1] == "app-server":
		requireFakeStoredAccount()
		if path := os.Getenv("FAKE_CODEX_LAUNCHES"); path != "" {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				panic(err)
			}
			_, _ = fmt.Fprintf(f, "%s %q\n", os.Getenv("CODEX_HOME"), os.Args[1:])
			_ = f.Close()
		}
		s := codextest.Serve(os.Stdin, os.Stdout, func(s *codextest.Server) {
			fakeCodex(s)
			if path := fakeAccountState(); path != "" {
				acptest.ConfigureNative(s, path)
			}
		})
		<-s.Done()
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func fakeAccountState() string {
	if os.Getenv("FAKE_CODEX_CONNECTION_ACCOUNT") != "" {
		return filepath.Join(os.Getenv("CODEX_HOME"), "fixture-account-state.json")
	}
	return os.Getenv("FAKE_CODEX_ACP_STATE")
}

func requireFakeStoredAccount() {
	if os.Getenv("FAKE_CODEX_CONNECTION_ACCOUNT") == "" {
		return
	}
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "CODEX_ACCESS_TOKEN", "OPENAI_IDENTITY_TOKEN_FILE", "OPENAI_FEDERATION_RULE_ID", "OPENAI_BASE_URL"} {
		if _, exists := os.LookupEnv(key); exists {
			fmt.Fprintln(os.Stderr, "named account inherited an authentication override:", key)
			os.Exit(97)
		}
	}
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
// stops launched supervisors and their descendants at cleanup.
func installFakeTmux(t *testing.T) string {
	t.Helper()
	bin, pids := t.TempDir(), t.TempDir()
	testutil.InstallProviderTmux(t, bin, pids)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OCMAN_FAKE_PROVIDER_PID_DIR", pids)
	return pids
}
