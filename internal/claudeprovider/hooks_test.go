package claudeprovider

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func readJSON(t *testing.T, path string) any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHookPluginInstallUninstallKeepsSettings(t *testing.T) {
	cli := claudetest.Binary(t)
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	log := filepath.Join(t.TempDir(), "plugin.log")
	t.Setenv("FAKE_CLAUDE_PLUGIN_LOG", log)
	settings := filepath.Join(config, "settings.json")
	original := `{"model":"opus","enabledPlugins":{"other@market":true},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"mine"}]}]}}`
	if err := os.WriteFile(settings, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	want := readJSON(t, settings)
	marketplace := t.TempDir()
	if _, err := WritePlugin(marketplace, nil, "ocman hook"); err != nil {
		t.Fatal(err)
	}

	if err := InstallPlugin(t.Context(), cli, marketplace); err != nil {
		t.Fatal(err)
	}
	if !PluginInstalled(config) {
		t.Fatal("installed plugin not reported")
	}
	if err := UninstallPlugin(t.Context(), cli); err != nil {
		t.Fatal(err)
	}
	if PluginInstalled(config) {
		t.Fatal("uninstalled plugin still reported")
	}
	if got := readJSON(t, settings); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings changed:\n got %v\nwant %v", got, want)
	}
	data, _ := os.ReadFile(log)
	calls := strings.Split(strings.TrimSpace(string(data)), "\n")
	wantCalls := []string{
		`["marketplace","add","` + marketplace + `"]`,
		`["install","ocman@ocman","--scope","user"]`,
		`["uninstall","ocman@ocman","--scope","user"]`,
		`["marketplace","remove","ocman"]`,
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("CLI calls = %v", calls)
	}
}

func TestHookPluginCLIFailures(t *testing.T) {
	cli := claudetest.Binary(t)
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	// An already registered marketplace fails to add; installing still works.
	t.Setenv("FAKE_CLAUDE_PLUGIN_FAIL", "marketplace")
	if err := InstallPlugin(t.Context(), cli, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_PLUGIN_FAIL", "install")
	if err := InstallPlugin(t.Context(), cli, t.TempDir()); err == nil || !strings.Contains(err.Error(), "plugin install failed") {
		t.Fatalf("install error = %v", err)
	}
	t.Setenv("FAKE_CLAUDE_PLUGIN_FAIL", "uninstall")
	if err := UninstallPlugin(t.Context(), cli); err == nil {
		t.Fatal("uninstall failure not reported")
	}
	if PluginInstalled(filepath.Join(t.TempDir(), "missing")) {
		t.Fatal("no settings must read as not installed")
	}
}

func TestWritePluginHooks(t *testing.T) {
	dir := t.TempDir()
	plugin, err := WritePlugin(dir, map[string][]byte{"s": []byte("skill")}, `"/bin/ocman" hook --addr 127.0.0.1:1`)
	if err != nil || plugin != filepath.Join(dir, "ocman") {
		t.Fatalf("plugin = %q, %v", plugin, err)
	}
	if m := readJSON(t, filepath.Join(dir, ".claude-plugin", "marketplace.json")).(map[string]any); m["name"] != "ocman" {
		t.Fatalf("marketplace = %v", m)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct{ Command string }
		}
	}
	data, _ := os.ReadFile(filepath.Join(plugin, "hooks", "hooks.json"))
	if err := json.Unmarshal(data, &hooks); err != nil || len(hooks.Hooks) != len(HookEvents) {
		t.Fatalf("hooks = %s, %v", data, err)
	}
	if h := hooks.Hooks["PreToolUse"][0]; h.Matcher != "*" || h.Hooks[0].Command != `"/bin/ocman" hook --addr 127.0.0.1:1 PreToolUse` {
		t.Fatalf("PreToolUse = %+v", h)
	}
	if _, err := WritePlugin(dir, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plugin, "hooks", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("hooks must be removed without a listener: %v", err)
	}
}

func TestRunHookPostsSessionID(t *testing.T) {
	got := make(chan providerprotocol.HookEvent, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e providerprotocol.HookEvent
		_ = json.NewDecoder(r.Body).Decode(&e)
		got <- e
	}))
	defer srv.Close()
	input := `{"tool_input":{"content":"` + strings.Repeat("x", 1<<20) + `"},"session_id":"s1","cwd":"/w"}`
	RunHook(strings.TrimPrefix(srv.URL, "http://"), "PreToolUse", strings.NewReader(input), time.Second)
	if e := <-got; e != (providerprotocol.HookEvent{Event: "PreToolUse", SessionID: "s1"}) {
		t.Fatalf("event = %+v", e)
	}
}

func TestRunHookReturnsWithinTimeoutWhenOcmanIsDown(t *testing.T) {
	// A listener that accepts and never answers, and stdin that never closes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	stuck, w := io.Pipe()
	defer w.Close()
	go func() { _, _ = w.Write([]byte(`{"session_id":"s1"}`)) }()
	start := time.Now()
	RunHook(ln.Addr().String(), "Stop", stuck, HookTimeout)
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("hook took %v", d)
	}
	// Nothing listening at all.
	addr := ln.Addr().String()
	_ = ln.Close()
	start = time.Now()
	RunHook(addr, "Stop", strings.NewReader(`{"session_id":"s1"}`), HookTimeout)
	if d := time.Since(start); d > HookTimeout {
		t.Fatalf("refused hook took %v", d)
	}
	RunHook(addr, "Stop", strings.NewReader(`not json`), HookTimeout)
}

func sessionStatus(t *testing.T, h *liveHarness, id string) db.SessionStatus {
	t.Helper()
	sessions, err := h.adapter.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.ID == id {
			return s.Status
		}
	}
	t.Fatalf("session %s not listed in %+v", id, sessions)
	return ""
}

func waitEvent(t *testing.T, h *liveHarness, id string, want db.SessionStatus) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if got, _, _ := h.events.get(id); got == want {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("sink status = %q, want %q", got, want)
		}
	}
}

func TestTerminalSessionHooksDriveStatus(t *testing.T) {
	h := startLive(t, claudetest.Fixture("plain.jsonl"), nil)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if sessions, _ := h.adapter.Sessions(t.Context(), "", 0); len(sessions) == 2 {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("sessions = %+v", sessions)
		}
	}
	if s := sessionStatus(t, h, otherID); s != db.StatusInterrupted {
		t.Fatalf("transcript status = %q", s)
	}
	hook := func(event string) {
		t.Helper()
		if err := h.adapter.Hook(t.Context(), providerprotocol.HookEvent{Event: event, SessionID: otherID}); err != nil {
			t.Fatal(err)
		}
	}
	hook("PreToolUse")
	hook("PostToolUse")
	waitEvent(t, h, otherID, db.StatusBusy)
	if s := sessionStatus(t, h, otherID); s != db.StatusBusy {
		t.Fatalf("after PreToolUse status = %q", s)
	}
	hook("Notification")
	waitEvent(t, h, otherID, db.StatusWaiting)
	if s := sessionStatus(t, h, otherID); s != db.StatusWaiting {
		t.Fatalf("after Notification status = %q", s)
	}
	hook("Stop")
	waitEvent(t, h, otherID, db.StatusDone)
	if s := sessionStatus(t, h, otherID); s != db.StatusDone {
		t.Fatalf("after Stop status = %q", s)
	}
	if _, _, idles := h.events.get(otherID); idles != 1 {
		t.Fatalf("idle edges = %d", idles)
	}
	// A settled hook does not override a finished transcript.
	if err := h.adapter.Hook(t.Context(), providerprotocol.HookEvent{Event: "SessionStart", SessionID: mainID}); err != nil {
		t.Fatal(err)
	}
	if s := sessionStatus(t, h, mainID); s != db.StatusWaiting {
		t.Fatalf("finished transcript status = %q", s)
	}
	if err := h.adapter.Hook(t.Context(), providerprotocol.HookEvent{Event: "Bogus", SessionID: otherID}); err == nil {
		t.Fatal("unknown hook event accepted")
	}
}

func TestOwnedSessionIgnoresHooks(t *testing.T) {
	h := startLive(t, claudetest.Fixture("plain.jsonl"), nil)
	id := h.create(t)
	before := sessionStatus(t, h, id)
	for _, event := range []string{"PreToolUse", "Notification"} {
		if err := h.adapter.Hook(t.Context(), providerprotocol.HookEvent{Event: event, SessionID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if s := sessionStatus(t, h, id); s != before {
		t.Fatalf("status = %q, want %q", s, before)
	}
	if s, _, _ := h.events.get(id); s != "" {
		t.Fatalf("hook reached the status sink: %q", s)
	}
}
