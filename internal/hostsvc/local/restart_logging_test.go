package local

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestFailedCheckAndRestartHaveSeparateWarnings(t *testing.T) {
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
	hook := logtest.NewGlobal()
	rt := &fakeRuntime{probe: func(inst *ocruntime.Instance) bool { return inst.Endpoint != "http://127.0.0.1:6000" }}
	h := New(Deps{Runtime: rt})
	repo := initRepo(t)
	h.setInstance(repo, &ocruntime.Instance{ID: "shared-instance", Endpoint: "http://127.0.0.1:6000"})
	if _, err := h.ensureLocked(t.Context(), repo); err != nil {
		t.Fatal(err)
	}
	var check, restart *log.Entry
	for _, entry := range hook.AllEntries() {
		if entry.Message == "host: managed opencode probe failed; relaunching" {
			check = entry
		}
		if entry.Message == "host: restarting managed opencode" {
			restart = entry
		}
	}
	if check == nil || restart == nil {
		t.Fatalf("missing check or restart warning: %v", hook.AllEntries())
	}
	for _, entry := range []*log.Entry{check, restart} {
		if entry.Level != log.WarnLevel || entry.Data["repoRoot"] != repo || entry.Data["endpoint"] != "http://127.0.0.1:6000" || entry.Data["instanceID"] != "shared-instance" {
			t.Fatalf("missing warning context: %+v", entry)
		}
	}
	if check.Data["error"] == nil || restart.Data["reason"] != "failed health probe" {
		t.Fatalf("missing cause: check=%v restart=%v", check.Data, restart.Data)
	}
	hook.Reset()
	if _, err := h.restartLocked(t.Context(), repo); err != nil {
		t.Fatal(err)
	}
	for _, entry := range hook.AllEntries() {
		if entry.Message == "host: restarting managed opencode" && entry.Level == log.WarnLevel && entry.Data["reason"] == "requested restart" {
			return
		}
	}
	t.Fatal("explicit restart lacks warning")
}

func TestInconclusiveDiscoveredCheckWarnsWithoutRestart(t *testing.T) {
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	t.Cleanup(func() { logger.ReplaceHooks(oldHooks) })
	hook := logtest.NewGlobal()
	h := New(Deps{Runtime: &probeErrorRuntime{err: context.DeadlineExceeded}, DiscoverPort: func(string) string { return "6000" }})
	_, _ = h.ensureLocked(t.Context(), "repo")
	entries := hook.AllEntries()
	if len(entries) != 1 || entries[0].Level != log.WarnLevel || entries[0].Data["error"] == nil || entries[0].Data["endpoint"] != "http://127.0.0.1:6000" {
		t.Fatalf("missing failed-check warning: %v", entries)
	}
}
