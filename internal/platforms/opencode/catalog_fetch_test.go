package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func catalogServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	catalogCache = newHTTPCache(catalogCache.ttl)
	fetchFailureLogs.reset()
	return strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
}

func captureLogs(t *testing.T) *logtest.Hook {
	t.Helper()
	hook := logtest.NewLocal(logrus.StandardLogger())
	prev := logrus.GetLevel()
	logrus.SetLevel(logrus.DebugLevel)
	t.Cleanup(func() {
		logrus.StandardLogger().ReplaceHooks(make(logrus.LevelHooks))
		logrus.SetLevel(prev)
	})
	return hook
}

// FR-9: an empty agent or command picker logs one WARN explaining why.
func TestCatalogFetchFailuresLogWarnAndReturnNothing(t *testing.T) {
	port := catalogServer(t, http.StatusInternalServerError, `{}`)
	hook := captureLogs(t)
	if got := agentCatalogAt(context.Background(), port, "s", "/repo"); got != nil {
		t.Fatalf("agents on failure = %+v", got)
	}
	if got := slashCommandsAt(context.Background(), port, "s", "/repo"); got != nil {
		t.Fatalf("commands on failure = %+v", got)
	}
	if !findLog(hook, logrus.WarnLevel, "agent catalog fetch failed") || !findLog(hook, logrus.WarnLevel, "slash commands fetch failed") {
		t.Fatalf("missing WARN lines: %d entries", len(hook.AllEntries()))
	}
}

// A caller that went away is not an upstream failure.
func TestCatalogCancelledContextIsNotWarn(t *testing.T) {
	port := catalogServer(t, http.StatusOK, `[]`)
	hook := captureLogs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = agentCatalogAt(ctx, port, "s", "/repo")
	if findLog(hook, logrus.WarnLevel, "agent catalog fetch failed") {
		t.Fatal("cancelled context logged at WARN")
	}
}

func TestCatalogDecodeFailuresLogWarn(t *testing.T) {
	port := catalogServer(t, http.StatusOK, `{not json`)
	hook := captureLogs(t)
	if got := agentCatalogAt(context.Background(), port, "s", "/repo"); got != nil {
		t.Fatalf("agents on decode failure = %+v", got)
	}
	if got := slashCommandsAt(context.Background(), port, "s", "/repo"); got != nil {
		t.Fatalf("commands on decode failure = %+v", got)
	}
	if !findLog(hook, logrus.WarnLevel, "agent catalog decode failed") || !findLog(hook, logrus.WarnLevel, "slash commands decode failed") {
		t.Fatalf("missing decode WARN lines: %d entries", len(hook.AllEntries()))
	}
}

// OpenCode's /agent schema (mode/hidden/native) maps onto the picker's
// sections; project agents are not built in.
func TestAgentCatalogMapsOpenCodeFields(t *testing.T) {
	port := catalogServer(t, http.StatusOK, `[
		{"name":"build","description":"builder","mode":"primary","native":true},
		{"name":"architect","description":"custom project agent","mode":"primary","native":false},
		{"name":"explore","mode":"subagent","native":true},
		{"name":"title","mode":"primary","hidden":true,"native":true}
	]`)
	entries := agentCatalogAt(context.Background(), port, "s", "/repo")
	byName := map[string]int{}
	for i, e := range entries {
		byName[e.Name] = i
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %+v", entries)
	}
	if build := entries[byName["build"]]; build.Mode != "primary" || build.Kind != "primary" || !build.BuiltIn || build.Hidden {
		t.Errorf("build = %+v", build)
	}
	if arch := entries[byName["architect"]]; arch.Mode != "primary" || arch.BuiltIn {
		t.Errorf("architect = %+v", arch)
	}
	if explore := entries[byName["explore"]]; explore.Mode != "subagent" {
		t.Errorf("explore = %+v", explore)
	}
	if title := entries[byName["title"]]; !title.Hidden {
		t.Errorf("title = %+v", title)
	}
}
