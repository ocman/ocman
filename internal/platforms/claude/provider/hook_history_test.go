package claudeprovider

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/historyjournal"
	"github.com/ocman/ocman/internal/platforms/claude/provider/claudetest"
)

func generatedHooks(t *testing.T, hook string) map[string]string {
	t.Helper()
	var config struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string }
		}
	}
	if err := json.Unmarshal(hooksJSON(hook), &config); err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for event, matchers := range config.Hooks {
		out[event] = matchers[0].Hooks[0].Command
	}
	return out
}

// History hooks run in every Claude session, including ones started in a
// terminal, while status-only hooks still start ocman only when it is managed.
func TestHistoryHooksRunOutsideOcman(t *testing.T) {
	t.Setenv("OCMAN_MANAGED", "")
	hooks := generatedHooks(t, "printf '%s'")
	for _, event := range HistoryHookEvents {
		out, err := exec.Command("sh", "-c", hooks[event]).CombinedOutput()
		if err != nil || string(out) != event {
			t.Errorf("%s history hook: error = %v, output = %q", event, err, out)
		}
	}
	for _, event := range []string{"PreToolUse", "Notification"} {
		out, err := exec.Command("sh", "-c", hooks[event]).CombinedOutput()
		if err != nil || len(out) != 0 {
			t.Errorf("%s ran outside ocman: error = %v, output = %q", event, err, out)
		}
	}
}

func TestHookCommandQuotesJournalPath(t *testing.T) {
	cmd := HookCommand("/opt/o cman/ocman", "127.0.0.1:1", "/tmp/it's here")
	if cmd != `"/opt/o cman/ocman" hook --addr 127.0.0.1:1 --journal '/tmp/it'\''s here'` {
		t.Fatalf("command = %s", cmd)
	}
	if got := HookCommand("/bin/ocman", "", "/j"); got != `"/bin/ocman" hook --journal '/j'` {
		t.Fatalf("command without listener = %s", got)
	}
}

func TestHookJournalsSessionTranscript(t *testing.T) {
	dir := t.TempDir()
	input := `{"tool_response":{"content":"` + strings.Repeat("x", 1<<20) + `"},"session_id":"s1","transcript_path":"/t/s1.jsonl","cwd":"/w","hook_event_name":"PostToolUse"}`
	in := readHookInput(strings.NewReader(input))
	recordHook(dir, "PostToolUse", in)
	if id := in.SessionID; id != "s1" {
		t.Fatalf("session = %q", id)
	}
	entries, err := readJournal(dir)
	if err != nil || len(entries) != 1 || entries[0].Err != nil {
		t.Fatalf("journal = %+v, %v", entries, err)
	}
	rec := entries[0].Record
	if rec.Session != "s1" || rec.Type != "PostToolUse" || !strings.Contains(string(rec.Data), `"transcriptPath":"/t/s1.jsonl"`) || len(rec.Data) > 512 {
		t.Fatalf("record = %+v (%d bytes)", rec, len(rec.Data))
	}
	// Status-only events and input without a session are not history.
	status := readHookInput(strings.NewReader(`{"session_id":"s1"}`))
	recordHook(dir, "PreToolUse", status)
	if status.SessionID != "s1" {
		t.Fatal("status event lost its session id")
	}
	invalid := readHookInput(strings.NewReader(`not json`))
	recordHook(dir, "Stop", invalid)
	if invalid.SessionID != "" {
		t.Fatal("invalid input produced a session")
	}
	if entries, _ := readJournal(dir); len(entries) != 1 {
		t.Fatalf("journaled non-history input: %d entries", len(entries))
	}
}

func TestEnsurePluginInstallsOrRefreshesStaleCache(t *testing.T) {
	cli := claudetest.Binary(t)
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	log := filepath.Join(t.TempDir(), "plugin.log")
	t.Setenv("FAKE_CLAUDE_PLUGIN_LOG", log)
	marketplace := t.TempDir()
	if _, err := EnsurePlugin(t.Context(), cli, config, marketplace); err == nil {
		t.Fatal("ensured a plugin that was never written")
	}
	if _, err := WritePlugin(marketplace, nil, `"/bin/ocman" hook --journal '/j'`); err != nil {
		t.Fatal(err)
	}
	calls := func() []string {
		data, _ := os.ReadFile(log)
		_ = os.Remove(log)
		return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(data)), "\n", " "))
	}
	if action, err := EnsurePlugin(t.Context(), cli, config, marketplace); err != nil || action != "installed" {
		t.Fatalf("install = %q, %v", action, err)
	}
	if got := calls(); len(got) != 2 || !strings.Contains(got[1], `"install"`) {
		t.Fatalf("install calls = %v", got)
	}
	cache := func(version string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(config, "plugins"), 0o700); err != nil {
			t.Fatal(err)
		}
		data := `{"version":2,"plugins":{"ocman@ocman":[{"scope":"user","version":"` + version + `"}]}}`
		if err := os.WriteFile(filepath.Join(config, "plugins", "installed_plugins.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cache("1.0.0")
	if action, err := EnsurePlugin(t.Context(), cli, config, marketplace); err != nil || action != "updated" {
		t.Fatalf("refresh = %q, %v", action, err)
	}
	if got := calls(); len(got) != 2 || got[0] != `["marketplace","update","ocman"]` || got[1] != `["update","ocman@ocman","--scope","user"]` {
		t.Fatalf("refresh calls = %v", got)
	}
	cache(PluginVersion(marketplace))
	if action, err := EnsurePlugin(t.Context(), cli, config, marketplace); err != nil || action != "" {
		t.Fatalf("current plugin = %q, %v", action, err)
	}
	if got := calls(); len(got) != 0 {
		t.Fatalf("current plugin ran CLI: %v", got)
	}
	cache("1.0.0")
	t.Setenv("FAKE_CLAUDE_PLUGIN_FAIL", "update")
	if _, err := EnsurePlugin(t.Context(), cli, config, marketplace); err == nil {
		t.Fatal("refresh failure not reported")
	}
	if InstalledVersion(filepath.Join(t.TempDir(), "missing")) != "" {
		t.Fatal("missing cache reported a version")
	}
}

func TestWritePluginVersionTracksContent(t *testing.T) {
	dir := t.TempDir()
	version := func() string {
		t.Helper()
		var manifest struct{ Version string }
		data, err := os.ReadFile(filepath.Join(dir, "ocman", ".claude-plugin", "plugin.json"))
		if err != nil || json.Unmarshal(data, &manifest) != nil {
			t.Fatalf("manifest = %s, %v", data, err)
		}
		return manifest.Version
	}
	if _, err := WritePlugin(dir, map[string][]byte{"s": []byte("one")}, `"/bin/ocman" hook --journal '/j'`); err != nil {
		t.Fatal(err)
	}
	first := version()
	if _, err := WritePlugin(dir, map[string][]byte{"s": []byte("one")}, `"/bin/ocman" hook --journal '/j'`); err != nil || version() != first {
		t.Fatalf("unchanged plugin changed version: %s -> %s", first, version())
	}
	if _, err := WritePlugin(dir, map[string][]byte{"s": []byte("one")}, `"/other/ocman" hook --journal '/j'`); err != nil || version() == first {
		t.Fatal("changed hooks kept the cached plugin version")
	}
	if got := PluginVersion(dir); got != version() || !strings.HasPrefix(got, "1.0.0-") {
		t.Fatalf("PluginVersion = %q", got)
	}
}

// readJournal returns every complete record in the journal directory.
func readJournal(dir string) ([]historyjournal.Entry, error) {
	segments, err := historyjournal.Segments(dir)
	var out []historyjournal.Entry
	for _, segment := range segments {
		if err != nil {
			break
		}
		var entries []historyjournal.Entry
		entries, err = historyjournal.ReadSegment(dir, segment, 0, 100)
		out = append(out, entries...)
	}
	return out, err
}
