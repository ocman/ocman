package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

const claudeFixtureSession = "11111111-1111-4111-8111-111111111111"

// TestClaudeOnlyStartup runs `ocman -platforms claudecode` with no opencode
// on PATH and browses, opens and archives a Claude session over HTTP.
func TestClaudeOnlyStartup(t *testing.T) {
	if runOcmanChild() {
		return
	}
	dir := t.TempDir()
	copyTree(t, filepath.Join("..", "claudeprovider", "testdata", "projects"), filepath.Join(dir, ".claude", "projects"))
	bin := filepath.Join(dir, "bin")
	marker := filepath.Join(dir, "opencode-called")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	testutil.WriteScript(t, filepath.Join(bin, "opencode"), "#!/bin/sh\n: > \"$OCMAN_TEST_EXEC_MARKER\"\nexit 1\n")
	testutil.WriteScript(t, filepath.Join(bin, "claude"), "#!/bin/sh\necho '2.1.220 (Claude Code)'\n")
	base, out, stop := startClaudeOcman(t, "TestClaudeOnlyStartup", dir, "OCMAN_TEST_EXEC_MARKER="+marker)
	defer stop()

	identity := waitSessionIdentity(t, base, "claudecode", claudeFixtureSession)
	var listed db.Session
	for deadline := time.Now().Add(30 * time.Second); listed.ID == ""; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("Claude session never listed\n%s", out.String())
		}
		for _, s := range listSessions(base) {
			if s.ID == identity.ID {
				listed = s
			}
		}
	}
	if listed.Platform != "claudecode" || listed.Title != "Marker subagent run" || listed.Directory != "/work/repo" {
		t.Fatalf("listed = %+v", listed)
	}

	resp, err := http.Get(base + "/api/session/" + listed.ID)
	if err != nil {
		t.Fatal(err)
	}
	var detail platforms.SessionDetail
	err = json.NewDecoder(resp.Body).Decode(&detail)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || len(detail.Messages) != 4 || !strings.Contains(fmt.Sprint(partTexts(detail)), "All done") {
		t.Fatalf("open session: %d %v %+v", resp.StatusCode, err, detail.Messages)
	}

	for _, req := range []map[string]any{
		{"platform": "claudecode", "sessionId": listed.ID, "timeUpdated": listed.TimeUpdated, "archived": true},
		{"platform": "claudecode", "sessionId": listed.ID, "pinned": true},
		{"platform": "claudecode", "sessionId": listed.ID, "timeUpdated": listed.TimeUpdated},
	} {
		action := "seen"
		if _, ok := req["archived"]; ok {
			action = "archive"
		} else if _, ok := req["pinned"]; ok {
			action = "pin"
		}
		body, _ := json.Marshal(req)
		resp, err := http.Post(base+"/api/session/"+action, "application/json", bytes.NewReader(body))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %v %v", action, resp, err)
		}
		resp.Body.Close()
	}
	applied := false
	for _, s := range listSessions(base) {
		applied = applied || (s.ID == listed.ID && s.Archived && s.Pinned && s.Seen)
	}
	if !applied {
		t.Fatalf("archive/pin/seen state not applied: %+v", listSessions(base))
	}
	assertClaudeOnlyWorkflows(t, base, detail)
	stop()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("claude-only startup executed opencode: %v\n%s", err, out.String())
	}
}

// assertClaudeOnlyWorkflows checks the user-facing paths a Claude-only
// ocman must serve: an enabled default platform, Claude projects in the
// pickers, a continuable terminal session and no
// OpenCode-only probe. The caller asserts opencode never ran.
func assertClaudeOnlyWorkflows(t *testing.T, base string, terminal platforms.SessionDetail) {
	t.Helper()
	if !terminal.Session.LiveConnection {
		t.Fatal("terminal-started session cannot be continued")
	}
	// Without a platform or model, a new conversation prepares on the only
	// enabled platform.
	resp, err := http.Post(base+"/api/sessions/prepare", "application/json", strings.NewReader(`{"directory":"/work/repo","remoteId":"local"}`))
	if err != nil {
		t.Fatal(err)
	}
	var def struct{ Platform string }
	err = json.NewDecoder(resp.Body).Decode(&def)
	resp.Body.Close()
	if err != nil || def.Platform != "claudecode" {
		t.Fatalf("default platform = %q, %v", def.Platform, err)
	}
	assertClaudeOnlyAnalytics(t, base)
	var projects []db.ProjectStats
	// Project browsing is available before the background import and cached
	// index refresh complete. Wait for the data, not merely a successful HTTP read.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		getJSON(t, base+"/api/projects", &projects)
		if len(projects) > 0 && projects[0].Directory == "/work/repo" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("projects = %+v, want the Claude project", projects)
		}
	}
	var queued []json.RawMessage
	getJSON(t, base+"/api/session/"+terminal.Session.ID+"/queue?platform=claudecode", &queued)
	if len(queued) != 0 {
		t.Fatalf("terminal session queued %d messages", len(queued))
	}
	var caps struct {
		Platforms []struct {
			ID           string
			Capabilities platforms.Capabilities
		}
		Hosts []struct {
			Capabilities struct{ OpenCode, Projects bool }
		}
	}
	getJSON(t, base+"/api/capabilities", &caps)
	if len(caps.Platforms) != 1 || !caps.Platforms[0].Capabilities.AutoApprove || caps.Platforms[0].Capabilities.Revert ||
		caps.Platforms[0].Capabilities.RuntimeRestart || caps.Hosts[0].Capabilities.OpenCode || !caps.Hosts[0].Capabilities.Projects {
		t.Fatalf("capabilities = %+v", caps)
	}
	// Nothing OpenCode-only blocks the app behind the setup panel.
	var doctor struct {
		Checks []struct {
			ID           string
			OK, Required bool
		}
	}
	getJSON(t, base+"/api/doctor", &doctor)
	for _, c := range doctor.Checks {
		if c.Required && !c.OK {
			t.Fatalf("required check %s fails in a Claude-only ocman", c.ID)
		}
	}
	// OpenCode-only settings answer without discovering OpenCode.
	var options struct{ Models []string }
	getJSON(t, base+"/api/settings/judge-model/options", &options)
	var mcp struct{ Unavailable bool }
	getJSON(t, base+"/api/mcp/config", &mcp)
	if len(options.Models) != 0 || !mcp.Unavailable {
		t.Fatalf("judge options = %v, mcp config unavailable = %v", options.Models, mcp.Unavailable)
	}
}

// assertClaudeOnlyAnalytics: the usage views answer from the analytics
// store with the fixture transcripts' per-message usage (/work/repo 26/12
// with 100 cache read and 20 write, /work/other 5/1) and no OpenCode
// database. The index syncs after startup, so its old rows reach an already
// built store through the source's changed-since hand-over.
func assertClaudeOnlyAnalytics(t *testing.T, base string) {
	t.Helper()
	var stats db.Stats
	for deadline := time.Now().Add(30 * time.Second); stats.TotalTokensIn != 31; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("stats = %+v, want the transcripts' 31 input tokens", stats)
		}
		// Fast conversation reads can now finish before the independent history
		// import. Retry only its explicit not-ready response, not other errors.
		resp, err := http.Get(base + "/api/stats")
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if resp.StatusCode == http.StatusServiceUnavailable && bytes.Contains(body, []byte("import-incomplete")) {
			continue
		}
		if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &stats) != nil {
			t.Fatalf("stats status=%d body=%s", resp.StatusCode, body)
		}
	}
	if stats.TotalSessions != 3 || stats.TotalTokensOut != 13 || stats.TotalCost != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	var dash db.MetricsDashboard
	getJSON(t, base+"/api/metrics?dir=/work/repo", &dash)
	if s := dash.Summary; dash.TotalRequests != 5 || s.InputTokens != 26 || s.OutputTokens != 12 || s.CacheReadTokens != 100 || s.CacheWriteTokens != 20 {
		t.Fatalf("metrics = %d requests, %+v", dash.TotalRequests, s)
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || readErr != nil || json.Unmarshal(body, out) != nil {
		t.Fatalf("GET %s = %d: %s", url, resp.StatusCode, body)
	}
}

// runOcmanChild runs ocman itself when re-executed by startClaudeOcman.
func runOcmanChild() bool {
	addr := os.Getenv("OCMAN_TEST_CLAUDE_ADDR")
	if addr == "" {
		return false
	}
	flag.CommandLine = flag.NewFlagSet("ocman", flag.ExitOnError)
	os.Args = []string{"ocman", "-platforms=claudecode", "-addr=" + addr, "-mcp-addr=" + os.Getenv("OCMAN_TEST_MCP_ADDR"), "-log-file=off"}
	Run("dev", skillCatalog)
	return true
}

// startClaudeOcman runs `ocman -platforms claudecode` in a subprocess with
// home and data under dir and only dir/bin (no opencode) on PATH.
func startClaudeOcman(t *testing.T, test, dir string, env ...string) (string, *bytes.Buffer, func()) {
	t.Helper()
	base, out, _, stop := startClaudeOcmanCmd(t, test, dir, env...)
	return base, out, stop
}

// startClaudeOcmanCmd is startClaudeOcman also returning the process, so a
// test can kill the dashboard outright.
func startClaudeOcmanCmd(t *testing.T, test, dir string, env ...string) (string, *bytes.Buffer, *exec.Cmd, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	out := &bytes.Buffer{}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+test+"$")
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = append(os.Environ(), "OCMAN_TEST_CLAUDE_ADDR="+addr,
		"PATH="+filepath.Join(dir, "bin")+":/usr/bin:/bin", "HOME="+dir, "CLAUDE_CONFIG_DIR=",
		"XDG_DATA_HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_STATE_HOME="+dir, "SHELL=/bin/sh",
		"OCMAN_AUTH_PASSWORD=", "OPENCODE_SERVER_PASSWORD=", "OPENCODE_CONFIG=", "OPENCODE_CONFIG_DIR=", "OCMAN_RELAY_URL=", "OCMAN_PUBLIC_BASE_URL=", "WAILS_BINDINGS=", "OCMAN_TEST_MCP_ADDR=")
	cmd.Env = append(cmd.Env, env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	return "http://" + addr, out, cmd, func() {
		if !stopped {
			stopped = true
			_ = cmd.Process.Signal(syscall.SIGTERM)
			_ = cmd.Wait()
		}
	}
}

func listSessions(base string) []db.Session {
	resp, err := http.Get(base + "/api/sessions")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var sessions []db.Session
	_ = json.NewDecoder(resp.Body).Decode(&sessions)
	return sessions
}

func partTexts(d platforms.SessionDetail) []string {
	var out []string
	for _, p := range d.Parts {
		var part struct{ Text string }
		_ = json.Unmarshal(p.Data, &part)
		out = append(out, part.Text)
	}
	return out
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(to, strings.TrimPrefix(path, from))
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestClaudeComposerStart starts a Claude conversation the way the
// new-conversation composer does and streams the fake CLI's reply.
func TestClaudeComposerStart(t *testing.T) {
	if runOcmanChild() {
		return
	}
	// Short: the project supervisor's unix socket lives under HOME.
	dir := testutil.ShortTempDir(t, "cc-") // short: unix socket path limit
	bin := filepath.Join(dir, "bin")
	claudetest.InstallFakeTmux(t, bin, filepath.Join(dir, "pids"))
	claudetest.InstallBinary(t, filepath.Join(bin, "claude"))
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	stdinLog, gate, fixture := filepath.Join(dir, "stdin.log"), filepath.Join(dir, "gate"), filepath.Join(dir, "turns.jsonl")
	// Three turns on one child; the second (an immediate follow-up) is held
	// open by gate, so a queued follow-up must wait for its idle edge.
	turn := func(n int, hold string) string {
		rows := `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:` + strconv.Itoa(n) + `","message":{"role":"user","content":"x"}}}
`
		if hold != "" {
			rows += `{"direction":"wait","message":` + strconv.Quote(hold) + `}
`
		}
		return rows + `{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_` + strconv.Itoa(n) + `","model":"haiku","content":[{"type":"text","text":"SPIKE_OK"}]}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"SPIKE_OK"}}
`
	}
	if err := os.WriteFile(fixture, []byte(turn(1, "")+turn(2, gate)+turn(3, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	base, out, stop := startClaudeOcman(t, "TestClaudeComposerStart", dir, "FAKE_CLAUDE_FIXTURE="+fixture,
		"OCMAN_FAKE_PROVIDER_PID_DIR="+filepath.Join(dir, "pids"), "FAKE_CLAUDE_STDIN="+stdinLog)
	defer stop()

	// No platform: the model reference's prefix picks Claude Code.
	body, _ := json.Marshal(map[string]any{"directory": project, "remoteId": "local", "worktree": false,
		"prompt": "Reply with exactly SPIKE_OK.", "send": map[string]any{"message": "Reply with exactly SPIKE_OK.", "model": "claudecode:anthropic/haiku"}})
	var started struct {
		SessionID        string `json:"sessionId"`
		Platform         string `json:"platform"`
		FirstMessageSent bool   `json:"firstMessageSent"`
		Error            string `json:"firstMessageError"`
	}
	for deadline := time.Now().Add(30 * time.Second); started.SessionID == ""; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("session never started\n%s", out.String())
		}
		resp, err := http.Post(base+"/api/sessions/start", "application/json", bytes.NewReader(body))
		if err != nil {
			continue
		}
		if resp.StatusCode == http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&started)
		}
		resp.Body.Close()
	}
	if started.Platform != "claudecode" || !started.FirstMessageSent {
		t.Fatalf("start = %+v\n%s", started, out.String())
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/session/"+started.SessionID+"/stream?platform=claudecode", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	stream := bufio.NewScanner(resp.Body)
	stream.Buffer(nil, 8<<20)
	streamed := false
	for !streamed && stream.Scan() {
		line := stream.Text()
		streamed = strings.Contains(line, "SPIKE_OK") && (strings.Contains(line, `"part.upserted"`) || strings.Contains(line, `"part.delta"`))
	}
	if !streamed {
		t.Fatalf("reply never streamed (status %d)\n%s", resp.StatusCode, out.String())
	}

	// The session ocman launched is live, so the composer accepts follow-ups,
	// held (Ctrl/Cmd+Enter) or sent now, and they reach the same child.
	var detail platforms.SessionDetail
	getJSON(t, base+"/api/session/"+started.SessionID+"?platform=claudecode", &detail)
	if detail.Session == nil || !detail.Session.LiveConnection {
		t.Fatalf("launched session detail = %+v, want a live connection", detail.Session)
	}
	follow := func(queue bool) {
		t.Helper()
		msg, _ := json.Marshal(map[string]any{"message": fmt.Sprintf("FOLLOW_UP_%v", queue), "queue": queue})
		resp, err := http.Post(base+"/api/session/"+started.SessionID+"/message?platform=claudecode", "application/json", bytes.NewReader(msg))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("follow-up (queue=%v) = %d\n%s", queue, resp.StatusCode, out.String())
		}
	}
	received := func() string { data, _ := os.ReadFile(stdinLog); return string(data) }
	await := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); !ok(); time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: %s\n%s", what, received(), out.String())
			}
		}
	}
	idle := func() bool {
		var d platforms.SessionDetail
		getJSON(t, base+"/api/session/"+started.SessionID+"?platform=claudecode", &d)
		return d.Session != nil && d.Session.Status == "done"
	}
	await("first turn idle", idle)
	follow(false) // sent now: reaches the child and opens a turn held by gate
	await("immediate follow-up reached claude", func() bool { return strings.Contains(received(), "FOLLOW_UP_false") })
	follow(true) // held for the next idle edge
	time.Sleep(500 * time.Millisecond)
	if strings.Contains(received(), "FOLLOW_UP_true") {
		t.Fatalf("a queued follow-up reached claude mid-turn: %s", received())
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	await("queued follow-up delivered after idle", func() bool { return strings.Contains(received(), "FOLLOW_UP_true") })
	await("queued turn idle", idle)
	time.Sleep(500 * time.Millisecond)
	if got := received(); strings.Count(got, "FOLLOW_UP_true") != 1 || strings.Index(got, "FOLLOW_UP_false") > strings.Index(got, "FOLLOW_UP_true") {
		t.Fatalf("queued follow-up delivered out of order or more than once: %s", got)
	}
}
