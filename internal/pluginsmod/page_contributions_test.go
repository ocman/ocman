package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/testutil"
	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
)

func TestContributionProcessHelper(t *testing.T) {
	if os.Getenv("PAGE_CONTRIBUTION_HELPER") != "1" {
		return
	}
	d := pagesTestDescription()
	d.Capabilities = []plugins.Capability{plugins.PageContributionCapability}
	d.Pages[0].Validate = true
	if plugin.Mode(os.Args[len(os.Args)-1]) == plugin.ModeServe {
		f := os.NewFile(3, "configuration")
		_ = f.Close()
	}
	var mu sync.Mutex
	name, revision := "initial", "r1"
	h := plugin.PageHandler(d, func(ctx context.Context, method string, req plugin.PageRequest) (plugin.PageDocument, error) {
		mu.Lock()
		defer mu.Unlock()
		if req.ItemID == "missing" {
			return plugin.PageDocument{}, plugin.Failure(plugin.ErrorNotFound)
		}
		values := map[string]any{}
		_ = json.Unmarshal(req.Values, &values)
		if method == "save" {
			name, _ = values["name"].(string)
			revision = "r2"
		}
		raw, _ := json.Marshal(map[string]any{"name": name})
		doc := plugin.PageDocument{Revision: revision, Values: raw, Fields: []plugin.PageField{{Key: "name", Label: "Name", Type: "text", Required: true}}}
		if method == "validate" {
			if values["name"] == "taken" {
				doc.Issues = []plugin.PageIssue{{Path: "/name", Message: "Name is already used."}}
			}
			if values["name"] == "offline" {
				return doc, plugin.Failure(plugin.ErrorUnavailable)
			}
		}
		return doc, nil
	})
	if err := plugin.Run(context.Background(), plugin.Mode(os.Args[len(os.Args)-1]), os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, h); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestContributionSavesValidateOnOwnerBeforeMutation(t *testing.T) {
	db := statetest.Open(t, Schema)
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ocman-plugin-contributions")
	script := "#!/bin/sh\nexport PAGE_CONTRIBUTION_HELPER=1\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run='^TestContributionProcessHelper$' -- \"$@\"\n"
	if err = os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	candidates, err := plugins.Scan(t.Context(), root, nil)
	if err != nil || len(candidates) != 1 || candidates[0].Err != nil {
		t.Fatalf("scan: %+v %v", candidates, err)
	}
	candidate := candidates[0]
	if err = db.DiscoverPlugin(t.Context(), candidate.Description, path, candidate.Checksum, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.SetPluginEnabled(t.Context(), candidate.Description.ID, true, []string{plugins.PageProjectGrant}); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	if err = os.Chmod(data, 0700); err != nil {
		t.Fatal(err)
	}
	process, err := plugins.StartProcess(t.Context(), plugins.LaunchConfig{Candidate: candidate, Supported: []plugins.Capability{plugins.PageContributionCapability}, DataDir: data, Configuration: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Stop()
	testutil.WaitFor(t, 10*time.Second, "contribution plugin ready", func() bool { return process.Health().Status == "ready" })
	m := NewModule(Deps{Store: db, ProjectDirectory: func(context.Context, string) (string, error) { return root, nil }})
	m.processes = map[string]*plugins.Process{candidate.Description.ID: process}
	request := func(name, revision string) json.RawMessage {
		raw, _ := json.Marshal(plugins.PageRequest{PageID: "pentest", ProjectID: "project-a", Revision: revision, Values: json.RawMessage(`{"name":"` + name + `"}`)})
		return raw
	}
	for _, name := range []string{"", "taken"} {
		doc, err := m.localPluginPage(t.Context(), candidate.Description.ID, "save", request(name, "r1"))
		if err != nil || len(doc.Issues) == 0 || string(doc.Values) != `{"name":"initial"}` {
			t.Fatalf("invalid save: %+v %v", doc, err)
		}
	}
	for _, tc := range []struct {
		name, revision string
		category       plugins.ErrorCategory
	}{{"ok", "stale", plugins.ErrorConflict}, {"offline", "r1", plugins.ErrorUnavailable}} {
		_, err := m.localPluginPage(t.Context(), candidate.Description.ID, "save", request(tc.name, tc.revision))
		var wire *plugins.WireError
		if !errors.As(err, &wire) || wire.Category != tc.category {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	doc, err := m.localPluginPage(t.Context(), candidate.Description.ID, "save", request("saved", "r1"))
	if err != nil || doc.Revision != "r2" || len(doc.Issues) != 0 {
		t.Fatalf("save: %+v %v", doc, err)
	}
	if _, err = m.localPluginPage(t.Context(), candidate.Description.ID, "execute", request("bad", "r2")); err == nil {
		t.Fatal("unknown method accepted")
	}
	if err = db.SetPluginGrants(t.Context(), candidate.Description.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = m.localPluginPage(t.Context(), candidate.Description.ID, "save", request("denied", "r2")); err == nil {
		t.Fatal("revoked save accepted")
	}
}
