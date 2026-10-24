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

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
)

func pagesTestDescription() plugins.Description {
	return plugins.Description{ID: "org.example.pages", Name: "Pages", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 2, Capabilities: []plugins.Capability{plugins.PageCapability}, RequestedGrants: []string{plugins.PageProjectGrant}, Pages: []plugins.PageDescriptor{{ID: "pentest", Label: "Pentest"}}}
}

func TestPageProcessHelper(t *testing.T) {
	if os.Getenv("PAGE_PROCESS_HELPER") != "1" {
		return
	}
	mode := plugin.Mode(os.Args[len(os.Args)-1])
	if mode == plugin.ModeServe {
		f := os.NewFile(3, "configuration")
		_ = f.Close()
	}
	h := plugin.PageHandler(pagesTestDescription(), func(ctx context.Context, method string, r plugin.PageRequest) (plugin.PageDocument, error) {
		return plugin.PageDocument{Revision: "r1", Values: json.RawMessage(`{"name":"staging"}`), Fields: []plugin.PageField{{Key: "name", Label: "Name", Type: "text"}}}, nil
	})
	err := plugin.Run(context.Background(), mode, os.Getenv("OCMAN_PLUGIN_TOKEN"), pagesTestDescription(), os.Stdin, os.Stdout, h)
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestLocalPagesRecheckEnablementGrantsAndProject(t *testing.T) {
	db := statetest.Open(t, Schema)
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ocman-plugin-pages")
	script := "#!/bin/sh\nexport PAGE_PROCESS_HELPER=1\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run='^TestPageProcessHelper$' -- \"$@\"\n"
	if err = os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	candidates, err := plugins.Scan(t.Context(), root, nil)
	if err != nil || len(candidates) != 1 || candidates[0].Err != nil {
		t.Fatalf("discovery: %+v %v", candidates, err)
	}
	candidate := candidates[0]
	if err = db.DiscoverPlugin(t.Context(), candidate.Description, path, candidate.Checksum, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.SetPluginEnabled(t.Context(), candidate.Description.ID, true, []string{plugins.PageProjectGrant}); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if err = os.Chmod(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	process, err := plugins.StartProcess(t.Context(), plugins.LaunchConfig{Candidate: candidate, Supported: []plugins.Capability{plugins.PageCapability}, DataDir: dataDir, Configuration: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Stop()
	deadline := time.Now().Add(10 * time.Second)
	for process.Health().Status != "ready" {
		if time.Now().After(deadline) {
			t.Fatalf("plugin not ready: %+v %s", process.Health(), process.RedactedStderr(func(s string) string { return s }))
		}
		time.Sleep(10 * time.Millisecond)
	}
	known := true
	m := NewModule(Deps{Store: db, ProjectDirectory: func(_ context.Context, id string) (string, error) {
		if !known || id != "project-a" {
			return "", errors.New("unknown")
		}
		return root, nil
	}})
	m.processes = map[string]*plugins.Process{candidate.Description.ID: process}
	request := json.RawMessage(`{"pageId":"pentest","projectId":"project-a"}`)
	for _, method := range []string{"read", "save"} {
		doc, err := m.localPluginPage(t.Context(), candidate.Description.ID, method, request)
		if err != nil || doc.Revision != "r1" {
			t.Fatalf("page %s: %+v %v", method, doc, err)
		}
	}
	known = false
	if _, err = m.localPluginPage(t.Context(), candidate.Description.ID, "read", request); err == nil {
		t.Fatal("unknown project admitted")
	}
	known = true
	if err = db.SetPluginGrants(t.Context(), candidate.Description.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = m.localPluginPage(t.Context(), candidate.Description.ID, "save", request); err == nil {
		t.Fatal("revoked grant admitted")
	}
	if err = db.SetPluginEnabled(t.Context(), candidate.Description.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = m.localPluginPage(t.Context(), candidate.Description.ID, "read", request); err == nil {
		t.Fatal("disabled plugin admitted")
	}
}
