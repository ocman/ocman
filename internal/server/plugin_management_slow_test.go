package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The marker delays only valid serve handshakes, not discovery or bad config.
func slowPluginManagementFixture(t *testing.T) *pluginManagementTest {
	t.Helper()
	f := newPluginManagementTest(t)
	path := filepath.Join(f.dir, "ocman-plugin-management")
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	delay := "\nif [ \"$1\" = serve ] && [ -f slow-start ]; then\nIFS= read -r delay < slow-start\n/bin/sleep \"$delay\"\nfi\nIFS= read -r hello <<EOF"
	script = []byte(strings.Replace(string(script), "\nIFS= read -r hello <<EOF", delay, 1))
	if err := os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/rescan", `{}`, 200)
	f.call(t, "POST", "/"+f.description.ID+"/configuration", `{"secrets":{"token":"working"}}`, 200)
	return f
}

func delayPluginManagementStart(t *testing.T, f *pluginManagementTest, seconds string) {
	t.Helper()
	dir, err := f.s.stateDB.PluginDataDir(t.Context(), f.description.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "slow-start"), []byte(seconds+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPluginManagementToleratesSlowStart(t *testing.T) {
	f := slowPluginManagementFixture(t)
	delayPluginManagementStart(t, f, "6")
	base := "/" + f.description.ID
	f.call(t, "POST", base+"/enable", f.approval(t), 200)
	for _, action := range []string{"restart", "retry", "grants", "configuration"} {
		body := `{}`
		switch action {
		case "grants":
			body = `{"grants":["context.owner"]}`
		case "configuration":
			body = `{"values":{"mode":"good"}}`
		}
		f.call(t, "POST", base+"/"+action, body, 200)
	}
	if p := f.s.pluginProcesses[f.description.ID]; p == nil || p.Health().Status != "ready" {
		t.Fatal("slow plugin did not become ready")
	}
}

func TestPluginManagementToleratesSlowRollback(t *testing.T) {
	f := slowPluginManagementFixture(t)
	base := "/" + f.description.ID
	f.call(t, "POST", base+"/enable", f.approval(t), 200)
	delayPluginManagementStart(t, f, "12")
	f.call(t, "POST", base+"/configuration", `{"values":{"mode":"bad"},"secrets":{"token":"candidate"}}`, 503)
	p, err := f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if err != nil || string(p.Configuration.Values["mode"]) != `"good"` || p.Health.Status != "ready" {
		t.Fatalf("slow rollback failed: %+v %v", p, err)
	}
	if err := f.s.stateDB.WithPluginSecrets(t.Context(), f.description.ID, func(secrets map[string]string) error {
		if secrets["token"] != "working" {
			t.Error("secret not rolled back")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
