package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
	"github.com/ocman/ocman/internal/testutil"
)

// TestCodexOnlyStartup runs `ocman -platforms codex` against a fake codex:
// a stored thread is imported and opened, and a new conversation started
// from the composer streams its reply through the tmux-hosted supervisor.
func TestCodexOnlyStartup(t *testing.T) {
	if runOcmanChild() {
		return
	}
	dir := testutil.ShortTempDir(t, "cx-") // short: the supervisor socket lives under the data dir
	bin := filepath.Join(dir, "bin")
	claudetest.InstallFakeTmux(t, bin, filepath.Join(dir, "pids"))
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteScript(t, filepath.Join(bin, "codex"), fmt.Sprintf(`#!/bin/sh
case "$1" in
--version) echo "codex-cli 9.9.9" ;;
app-server) OCMAN_TEST_FAKE_CODEX=1 exec %q ;;
*) exit 2 ;;
esac
`, exe))
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(dir, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	base, out, stop := startClaudeOcman(t, "TestCodexOnlyStartup", dir, "OCMAN_TEST_PLATFORMS=codex", "CODEX_HOME=",
		"OCMAN_FAKE_PROVIDER_PID_DIR="+filepath.Join(dir, "pids"))
	defer stop()

	// The stored thread is imported through Codex's own history reads.
	identity := waitSessionIdentity(t, base, "codex", codextest.StoredThread)
	var detail platforms.SessionDetail
	for deadline := time.Now().Add(30 * time.Second); !strings.Contains(fmt.Sprint(partTexts(detail)), codextest.StoredReply); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("stored thread never opened: %+v\n%s", detail.Messages, out.String())
		}
		resp, err := http.Get(base + "/api/session/" + identity.ID)
		if err != nil {
			continue
		}
		detail = platforms.SessionDetail{}
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		resp.Body.Close()
	}
	if detail.Session == nil || detail.Session.Platform != "codex" || detail.Session.Directory != codextest.Directory || len(detail.Messages) != 2 {
		t.Fatalf("stored detail = %+v %d messages", detail.Session, len(detail.Messages))
	}

	// A new conversation: the model reference's prefix picks Codex.
	body, _ := json.Marshal(map[string]any{"directory": project, "remoteId": "local", "worktree": false,
		"prompt": "Say hi.", "send": map[string]any{"message": "Say hi.", "model": "codex:openai/gpt-test"}})
	var started struct {
		SessionID        string `json:"sessionId"`
		Platform         string `json:"platform"`
		FirstMessageSent bool   `json:"firstMessageSent"`
		Error            string `json:"firstMessageError"`
	}
	resp, err := http.Post(base+"/api/sessions/start", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&started)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || started.Platform != "codex" || !started.FirstMessageSent {
		t.Fatalf("start = %d %+v\n%s", resp.StatusCode, started, out.String())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/session/"+started.SessionID+"/stream?platform=codex", nil)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	scanner := bufio.NewScanner(stream.Body)
	scanner.Buffer(nil, 8<<20)
	streamed := false
	for !streamed && scanner.Scan() {
		streamed = strings.Contains(scanner.Text(), codextest.LiveReply)
	}
	if !streamed {
		t.Fatalf("reply never streamed (status %d)\n%s", stream.StatusCode, out.String())
	}
}
