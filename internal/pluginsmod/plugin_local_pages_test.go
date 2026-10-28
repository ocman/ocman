package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/sdk/plugin"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func localPageDescription() plugin.Description {
	return plugin.Description{ID: "org.example.local-pages", Name: "Local pages", Version: "1", Protocol: plugin.Version{Major: 1},
		Scope: plugin.ScopeOwner, MaxConcurrency: 2, Capabilities: []plugin.Capability{plugin.PluginPageCapability},
		RequestedGrants: []string{plugin.PagePluginGrant}, Pages: []plugin.PageDescriptor{{ID: "targets", Label: "Targets", Scope: "plugin", Validate: true}}}
}

func TestPluginLocalPageProcessHelper(t *testing.T) {
	if os.Getenv("PLUGIN_LOCAL_PAGE_HELPER") != "1" {
		return
	}
	mode := plugin.Mode(os.Args[len(os.Args)-1])
	if mode == plugin.ModeServe {
		_ = os.NewFile(3, "configuration").Close()
	}
	d := localPageDescription()
	h := plugin.PageHandler(d, func(context.Context, string, plugin.PageRequest) (plugin.PageDocument, error) {
		return plugin.PageDocument{Revision: "r1", Values: json.RawMessage(`{"name":"registry"}`), Fields: []plugin.PageField{{Key: "name", Label: "Name", Type: "text"}}}, nil
	})
	if err := plugin.Run(context.Background(), mode, os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, h); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestPluginLocalPagesSkipProjectDiscoveryButKeepOwnerGrants(t *testing.T) {
	db := statetest.Open(t, Schema)
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ocman-plugin-local-pages")
	script := "#!/bin/sh\nexport PLUGIN_LOCAL_PAGE_HELPER=1\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run='^TestPluginLocalPageProcessHelper$' -- \"$@\"\n"
	if err = os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	candidates, err := plugins.Scan(t.Context(), root, nil)
	if err != nil || len(candidates) != 1 || candidates[0].Err != nil {
		t.Fatalf("discovery: %+v %v", candidates, err)
	}
	candidate := candidates[0]
	id := candidate.Description.ID
	if err = db.DiscoverPlugin(t.Context(), candidate.Description, path, candidate.Checksum, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.SetPluginEnabled(t.Context(), id, true, []string{plugins.PagePluginGrant}); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	if err = os.Chmod(data, 0o700); err != nil {
		t.Fatal(err)
	}
	process, err := plugins.StartProcess(t.Context(), plugins.LaunchConfig{Candidate: candidate, Supported: []plugins.Capability{plugins.PluginPageCapability}, DataDir: data, Configuration: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Stop()
	deadline := time.Now().Add(10 * time.Second)
	for process.Health().Status != "ready" {
		if time.Now().After(deadline) {
			t.Fatal("plugin not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	lookups := 0
	m := NewModule(Deps{Store: db, ProjectDirectory: func(context.Context, string) (string, error) { lookups++; return "", errors.New("unknown project") }})
	m.processes = map[string]*plugins.Process{id: process}
	request := json.RawMessage(`{"pageId":"targets"}`)
	if doc, err := m.localPluginPage(t.Context(), id, "read", request); err != nil || doc.Revision != "r1" {
		t.Fatalf("plugin-local read: %+v %v", doc, err)
	}
	request = json.RawMessage(`{"pageId":"targets","revision":"r1","values":{"name":"edited"}}`)
	if _, err := m.localPluginPage(t.Context(), id, "save", request); err != nil {
		t.Fatal(err)
	}
	if lookups != 0 {
		t.Fatal("plugin-local read/save enumerated projects")
	}
	if _, err := m.localPluginPage(t.Context(), id, "read", json.RawMessage(`{"pageId":"targets","projectId":"foreign"}`)); err == nil {
		t.Fatal("explicit foreign project bypassed authorization")
	}
	if lookups != 1 {
		t.Fatal("explicit project was not checked")
	}
	if err := db.SetPluginGrants(t.Context(), id, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.localPluginPage(t.Context(), id, "read", json.RawMessage(`{"pageId":"targets"}`)); err == nil {
		t.Fatal("revoked plugin-local grant admitted")
	}
	if err := db.SetPluginEnabled(t.Context(), id, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.localPluginPage(t.Context(), id, "read", json.RawMessage(`{"pageId":"targets"}`)); err == nil {
		t.Fatal("disabled plugin admitted")
	}
}
