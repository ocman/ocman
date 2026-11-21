package pluginsmod

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestPluginPaneDeclarationsCarryPluginIcon(t *testing.T) {
	description := plugins.Description{
		ID: "org.example.pentest", Name: "Pentest", Version: "1", Protocol: plugins.Version{Major: 1},
		Scope: plugins.ScopeOwner, MaxConcurrency: 1,
		Icon:             &plugins.IconDescriptor{Path: "assets/shield-crosshair.svg"},
		Capabilities:     []plugins.Capability{plugins.PaneCapability},
		RequestedGrants:  []string{plugins.PaneProjectGrant},
		Panes:            []plugins.PaneDescriptor{{ID: "findings", Label: "Pentest findings"}},
	}
	panes := pluginPaneDeclarations([]state.PluginRegistration{{Description: description, Enabled: true, Grants: []string{plugins.PaneProjectGrant}}}, "local")
	if len(panes) != 1 || panes[0].Icon == nil || panes[0].Icon.Path != "assets/shield-crosshair.svg" {
		t.Fatalf("pane must carry the plugin icon for pane headers: %+v", panes)
	}
	data, err := json.Marshal(panes)
	if err != nil || !strings.Contains(string(data), `"icon":{"path":"assets/shield-crosshair.svg"}`) {
		t.Fatalf("listed pane JSON must expose the icon: %s %v", data, err)
	}
	// A plugin without an icon omits the field so older clients are unaffected.
	description.Icon = nil
	data, err = json.Marshal(pluginPaneDeclarations([]state.PluginRegistration{{Description: description, Enabled: true, Grants: []string{plugins.PaneProjectGrant}}}, "local"))
	if err != nil || strings.Contains(string(data), `"icon"`) {
		t.Fatalf("icon must be omitted when undeclared: %s %v", data, err)
	}
}
