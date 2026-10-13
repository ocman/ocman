package server

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

func writeDiscoveryPlugin(t *testing.T, dir, name, id, version string) string {
	t.Helper()
	d := plugins.Description{ID: id, Name: "Test", Version: version, Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1}
	data, err := json.Marshal(plugins.Envelope{Type: plugins.TypeHello, Hello: &plugins.Hello{Mode: plugins.ModeDescribe, Token: "$OCMAN_PLUGIN_TOKEN", Description: &d}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	// Both read and printf are shell built-ins: discovery should not spend its
	// production deadline starting a second process just to emit one JSON line.
	testutil.WriteScript(t, path, "#!/bin/sh\nIFS= read -r hello <<EOF\n"+string(data)+"\nEOF\nprintf '%s\\n' \"$hello\"\n")
	return path
}
