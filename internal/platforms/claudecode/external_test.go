package claudecode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestContinueExternalSessionOnProjectSupervisor(t *testing.T) {
	data, err := os.MkdirTemp("/tmp", "ce-") // provider socket suffix exceeds macOS's limit under $TMPDIR
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	bin := filepath.Join(data, "bin")
	installFakeTmux(t, bin, filepath.Join(data, "pids"))
	if err := os.Rename(claudetest.Binary(t), filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	config, repo := filepath.Join(data, "config"), filepath.Join(data, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(config, "projects", "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-1111-4111-8111-111111111111"
	line, _ := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": repo,
		"uuid": "first", "timestamp": "2026-01-01T00:00:00Z", "message": map[string]any{"role": "user", "content": "original"}})
	if err := os.WriteFile(filepath.Join(config, "projects", "project", id+".jsonl"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OCMAN_FAKE_PROVIDER_PID_DIR", filepath.Join(data, "pids"))
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("FAKE_CLAUDE_FIXTURE", claudetest.Fixture("plain.jsonl"))
	t.Setenv("FAKE_CLAUDE_CWD_LOG", filepath.Join(data, "cwds"))
	db, err := state.Open(filepath.Join(data, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	p := newClaudePlatformAt(ctx, nil, db, data, exe, nil, "")
	t.Cleanup(func() { cancel(); p.Close() })
	rec := &recorded{idles: map[string]int{}}
	p.SetEventHandler(func(e providerprotocol.Event) { rec.mu.Lock(); rec.events = append(rec.events, e); rec.mu.Unlock() })
	go func() { _ = p.WatchEvents(ctx) }()
	detail, err := p.Session(ctx, id, 10, 0)
	if err != nil || !detail.Session.LiveConnection {
		t.Fatalf("external session unavailable: %v %v", detail, err)
	}
	if roots, _ := db.ListManagedProviders(ctx, "claudecode"); len(roots) != 0 {
		t.Fatal("reading launched a supervisor")
	}
	// A browser already watching the browsing supervisor must be released so
	// it can reconnect to the project supervisor selected by the first send.
	streamDone := make(chan struct{})
	browsing := make(chan struct{})
	oldHook := claudeTestHook
	claudeTestHook = func(point, key string) {
		if point == "browsing" && key == id {
			close(browsing)
		}
	}
	t.Cleanup(func() { claudeTestHook = oldHook })
	go func() { _ = p.ProxyEvents(ctx, id, &discardStream{}, func() {}); close(streamDone) }()
	select {
	case <-browsing:
	case <-time.After(5 * time.Second):
		t.Fatal("browser never attached")
	}
	if err := p.SendMessage(ctx, platforms.SendMessageRequest{SessionID: id, Message: "continue"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "external reply reached headless watcher", func() bool { return rec.has("message.part.updated", "SPIKE_OK") })
	select {
	case <-streamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("browsing stream never reconnected")
	}
	if lines := readLines(filepath.Join(data, "cwds")); len(lines) != 1 || canonical(t, lines[0]) != canonical(t, repo) {
		t.Fatalf("child directory = %v", lines)
	}
	if roots, _ := db.ListManagedProviders(ctx, "claudecode"); len(roots) != 1 {
		t.Fatalf("project supervisors = %v", roots)
	}
	if !strings.Contains(detail.Session.ID, id) {
		t.Fatal("session identity changed")
	}
}

type discardStream struct{}

func (*discardStream) Write(p []byte) (int, error) { return len(p), nil }
