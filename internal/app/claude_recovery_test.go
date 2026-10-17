package app

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// TestDashboardKillRestartComposition kills the whole ocman process
// (SIGKILL, no shutdown) while a launched Claude session waits on a
// permission, starts a new ocman process on the same data with another MCP
// listener, and answers the prompt over HTTP. The tmux-hosted project
// supervisor survives both, keeps the prompt answerable, finishes the
// turn, and its next child reaches the new listener.
func TestDashboardKillRestartComposition(t *testing.T) {
	if runOcmanChild() {
		return
	}
	dir := testutil.ShortTempDir(t, "kr-") // short: unix socket path limit
	bin, pids := filepath.Join(dir, "bin"), filepath.Join(dir, "pids")
	claudetest.InstallFakeTmux(t, bin, pids)
	claudetest.InstallBinary(t, filepath.Join(bin, "claude"))
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	hold, args, mcpLog := filepath.Join(dir, "release"), filepath.Join(dir, "args.json"), filepath.Join(dir, "mcp.log")
	env := []string{"FAKE_CLAUDE_FIXTURE=" + claudetest.Fixture("permission.jsonl"), "FAKE_CLAUDE_HOLD=" + hold,
		"FAKE_CLAUDE_ARGS=" + args, "OCMAN_FAKE_PROVIDER_PID_DIR=" + pids, "FAKE_CLAUDE_MCP_LOG=" + mcpLog}
	// reached reports that a child connected to ocman's MCP listener at addr
	// and got an initialize answer: reachability, not just the argument.
	reached := func(addr string) bool {
		data, _ := os.ReadFile(mcpLog)
		return strings.Contains(string(data), "http://"+addr+"/mcp ok\n")
	}
	freePort := func() string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().String()
	}
	run := func(mcp string) (string, *exec.Cmd, func()) {
		base, _, cmd, stop := startClaudeOcmanCmd(t, "TestDashboardKillRestartComposition", dir, append(env, "OCMAN_TEST_MCP_ADDR="+mcp)...)
		return base, cmd, stop
	}
	pending := func(base, id string) []map[string]any {
		resp, err := http.Get(base + "/api/session/" + id + "/permissions?platform=claudecode")
		if err != nil {
			return nil
		}
		defer resp.Body.Close()
		var prompts []map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&prompts)
		return prompts
	}
	start := func(base string) string {
		body, _ := json.Marshal(map[string]any{"directory": project, "remoteId": "local", "platform": "claudecode", "worktree": false,
			"prompt": "go", "send": map[string]any{"message": "go"}})
		var started struct {
			SessionID        string `json:"sessionId"`
			FirstMessageSent bool   `json:"firstMessageSent"`
		}
		eventually(t, "session started", func() bool {
			resp, err := http.Post(base+"/api/sessions/start", "application/json", strings.NewReader(string(body)))
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&started) == nil && started.FirstMessageSent
		})
		return started.SessionID
	}
	mcpArg := func() string {
		var argv []string
		data, _ := os.ReadFile(args)
		_ = json.Unmarshal(data, &argv)
		if i := slices.Index(argv, "--mcp-config"); i >= 0 {
			return argv[i+1]
		}
		return ""
	}

	firstMCP := freePort()
	base, cmd, _ := run(firstMCP)
	id := start(base)
	eventually(t, "permission asked", func() bool { return len(pending(base, id)) == 1 })
	permission := pending(base, id)[0]["id"]
	if !strings.Contains(mcpArg(), firstMCP) || !reached(firstMCP) {
		t.Fatalf("child mcp config = %q, want %s reached", mcpArg(), firstMCP)
	}
	if err := cmd.Process.Kill(); err != nil { // no graceful shutdown
		t.Fatal(err)
	}
	_ = cmd.Wait()

	secondMCP := freePort()
	base, _, stop := run(secondMCP)
	defer stop()
	eventually(t, "permission answerable after the kill", func() bool {
		p := pending(base, id)
		return len(p) == 1 && p[0]["id"] == permission
	})
	body, _ := json.Marshal(map[string]any{"reply": "once"})
	resp, err := http.Post(base+"/api/session/"+id+"/permissions/"+permission.(string)+"?platform=claudecode", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("answer = %d", resp.StatusCode)
	}
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "turn finished", func() bool {
		resp, err := http.Get(base + "/api/session/" + id + "?platform=claudecode")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var detail platforms.SessionDetail
		if json.NewDecoder(resp.Body).Decode(&detail) != nil || detail.Session == nil {
			return false
		}
		return detail.Session.Status == db.StatusDone
	})
	if supervisors, _ := filepath.Glob(filepath.Join(pids, "*.pid")); len(supervisors) != 1 {
		t.Fatalf("the restart launched another supervisor: %v", supervisors)
	}
	// The surviving supervisor's next child uses the rebound listener.
	start(base)
	eventually(t, "next child on the new listener", func() bool { return strings.Contains(mcpArg(), secondMCP) && reached(secondMCP) })
}
