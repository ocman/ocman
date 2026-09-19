package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

func writeDiscoveryPlugin(t *testing.T, dir, name, id, version string) string {
	t.Helper()
	d := plugins.Description{ID: id, Name: "Test", Version: version, Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1}
	data, err := json.Marshal(plugins.Envelope{Type: plugins.TypeHello, Hello: &plugins.Hello{Mode: plugins.ModeDescribe, Token: "$OCMAN_PLUGIN_TOKEN", Description: &d}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n/bin/cat <<EOF\n"+string(data)+"\nEOF\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEnabledPluginProcesses(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &Server{stateDB: db, pluginCtx: ctx}
	t.Cleanup(s.stopPluginProcesses)
	id := "org.example.test"
	path := writeDiscoveryPlugin(t, dir, "ocman-plugin-a", id, "1")
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(strings.ReplaceAll(string(script), `"mode":"describe"`, `"mode":"$1"`) + "\nif [ \"$1\" = serve ]; then\n  read -r ack\n  echo started >> starts\n  read -r shutdown\nfi\n")
	if err := os.WriteFile(path, script, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.pluginProcesses) != 0 {
		t.Fatal("started disabled plugin")
	}
	if err := db.SetPluginEnabled(ctx, id, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncPluginProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	first := s.pluginProcesses[id]
	if first == nil {
		t.Fatal("enabled plugin not started")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := db.GetPlugin(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if p.Health.Status == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not ready: %+v", p.Health)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := s.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if s.pluginProcesses[id] != first {
		t.Fatal("rescan replaced running process")
	}
	dataDir, err := db.PluginDataDir(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// Conflicting discovery must stop the old process and preserve conflict health.
	writeDiscoveryPlugin(t, dir, "ocman-plugin-b", id, "1")
	if _, err := s.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.pluginProcesses) != 0 || first.Health().Status != "stopped" {
		t.Fatal("conflicted process still running")
	}
	p, err := db.GetPlugin(ctx, id)
	if err != nil || p.Health.Status != "conflict" {
		t.Fatalf("lost conflict health: %+v %v", p.Health, err)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "starts"))
	if err != nil || string(data) != "started\n" {
		t.Fatalf("duplicate process: %q %v", data, err)
	}
	if err := os.Remove(filepath.Join(dir, "ocman-plugin-b")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPluginEnabled(ctx, id, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncPluginProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPluginEnabled(ctx, id, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncPluginProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	if len(s.pluginProcesses) != 0 {
		t.Fatal("disabled process still running")
	}
	s.stopPluginProcesses()
	if err := s.SyncPluginProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	// Terminal health survives a server restart instead of resetting its budget.
	if err := db.SetPluginEnabled(ctx, id, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPluginProcessHealth(ctx, id, state.PluginHealth{Status: "unhealthy", RestartCount: 5}); err != nil {
		t.Fatal(err)
	}
	restarted := &Server{stateDB: db, pluginCtx: ctx}
	t.Cleanup(restarted.stopPluginProcesses)
	if _, err := restarted.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if len(restarted.pluginProcesses) != 0 {
		t.Fatal("restart reset unhealthy cutoff")
	}
	if err := db.SetPluginEnabled(ctx, id, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SyncPluginProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPluginEnabled(ctx, id, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := restarted.SyncPluginProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	if len(restarted.pluginProcesses) != 1 {
		t.Fatal("explicit reenable did not reset cutoff")
	}
}

func TestRescanPlugins(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &Server{stateDB: db}
	path := writeDiscoveryPlugin(t, dir, "ocman-plugin-a", "org.example.test", "1.0")
	rescan := func() []plugins.Discovery {
		t.Helper()
		catalog, err := s.RescanPlugins(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}
	get := func() state.PluginRegistration {
		t.Helper()
		p, err := s.stateDB.GetPlugin(ctx, "org.example.test")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	rescan()
	if p := get(); p.Enabled || p.Removed || p.Health.Status != "disabled" {
		t.Fatalf("new: %+v", p)
	}
	if err := s.stateDB.SetPluginConfiguration(ctx, "org.example.test", map[string]json.RawMessage{"value": json.RawMessage(`true`)}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.stateDB.SetPluginEnabled(ctx, "org.example.test", true, []string{"read"}); err != nil {
		t.Fatal(err)
	}
	rescan()
	if !get().Enabled {
		t.Fatal("unchanged scan revoked enablement")
	}
	firstChecksum := get().Checksum
	writeDiscoveryPlugin(t, dir, "ocman-plugin-a", "org.example.test", "2.0")
	rescan()
	if p := get(); p.Enabled || len(p.Grants) != 0 || p.Checksum == firstChecksum || string(p.Configuration.Values["value"]) != "true" {
		t.Fatalf("changed: %+v", p)
	}
	duplicate := writeDiscoveryPlugin(t, dir, "ocman-plugin-b", "org.example.test", "2.0")
	if err := s.stateDB.SetPluginEnabled(ctx, "org.example.test", true, nil); err != nil {
		t.Fatal(err)
	}
	for _, d := range rescan() {
		if !errors.Is(d.Err, plugins.ErrDuplicateID) {
			t.Fatalf("duplicate: %+v", d)
		}
	}
	if p := get(); p.Enabled || p.Health.Status != "conflict" {
		t.Fatalf("conflict: %+v", p)
	}
	if err := os.Remove(duplicate); err != nil {
		t.Fatal(err)
	}
	rescan()
	if p := get(); p.Enabled || p.Health.Status != "disabled" {
		t.Fatalf("resolved: %+v", p)
	}
	writeDiscoveryPlugin(t, dir, "ocman-plugin-a", "org.example.changed", "2.0")
	catalog := rescan()
	if len(catalog) != 1 || !errors.Is(catalog[0].Err, plugins.ErrIdentityChanged) {
		t.Fatalf("identity: %+v", catalog)
	}
	if _, err := s.stateDB.GetPlugin(ctx, "org.example.changed"); !errors.Is(err, state.ErrPluginNotFound) {
		t.Fatal("changed identity persisted")
	}
	// A fresh server still rejects a changed identity using the durable binding.
	s = &Server{stateDB: s.stateDB}
	if d := rescan(); !errors.Is(d[0].Err, plugins.ErrIdentityChanged) {
		t.Fatalf("restart identity: %+v", d)
	}
	writeDiscoveryPlugin(t, dir, "ocman-plugin-a", "org.example.test", "2.0")
	rescan()
	if get().Removed {
		t.Fatal("not rediscovered")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	rescan()
	if p := get(); !p.Removed || p.Enabled || string(p.Configuration.Values["value"]) != "true" {
		t.Fatalf("removed: %+v", p)
	}
}

func TestRescanPluginsFailures(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	s := &Server{}
	writeDiscoveryPlugin(t, dir, "ocman-plugin-test", "org.example.test", "1")
	if catalog, err := s.RescanPlugins(ctx); err != nil || len(catalog) != 1 || catalog[0].Err != nil {
		t.Fatalf("no state: %+v %v", catalog, err)
	}
	st := openTestStateDB(t)
	s.stateDB = st
	if _, err := s.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPluginEnabled(ctx, "org.example.test", true, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCMAN_PLUGIN_DIR", filepath.Join(dir, "ocman-plugin-test"))
	if _, err := s.RescanPlugins(ctx); err == nil {
		t.Fatal("directory error ignored")
	}
	p, err := st.GetPlugin(ctx, "org.example.test")
	if err != nil || !p.Enabled {
		t.Fatal("failed scan changed stored state")
	}
	t.Setenv("OCMAN_PLUGIN_DIR", "")
	t.Setenv("HOME", "")
	if _, err := s.RescanPlugins(ctx); err == nil {
		t.Fatal("missing home accepted")
	}
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RescanPlugins(ctx); !errors.Is(err, state.ErrPluginState) {
		t.Fatalf("closed store: %v", err)
	}
}

func TestDiscoveryConflictCannotReplaceIdentity(t *testing.T) {
	s := &Server{stateDB: openTestStateDB(t)}
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	writeDiscoveryPlugin(t, dir, "ocman-plugin-a", "org.example.first", "1")
	if _, err := s.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	writeDiscoveryPlugin(t, dir, "ocman-plugin-a", "org.example.other", "1")
	writeDiscoveryPlugin(t, dir, "ocman-plugin-b", "org.example.other", "1")
	if _, err := s.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.stateDB.GetPlugin(ctx, "org.example.other")
	if err != nil || p.Enabled || p.Health.Status != "conflict" || !strings.HasSuffix(p.ExecutablePath, "ocman-plugin-b") {
		t.Fatalf("conflict: %+v %v", p, err)
	}
}
