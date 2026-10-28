package pluginsmod

import (
	"testing"

	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestPageCatalogIncludesPluginNameForRootTabs(t *testing.T) {
	d := plugins.Description{ID: "example.inventory", Name: "Inventory", Scope: plugins.ScopeOwner,
		Capabilities: []plugins.Capability{plugins.PageTableCapability},
		Pages:        []plugins.PageDescriptor{{ID: "assets", Label: "Assets", Scope: "plugin"}, {ID: "jobs", Label: "Jobs", Scope: "plugin"}},
	}
	registration := state.PluginRegistration{Enabled: true, Description: d, Grants: []string{plugins.PagePluginGrant}}
	pages := pluginPageDeclarations([]state.PluginRegistration{registration}, "owner-a")
	if len(pages) != 2 {
		t.Fatalf("pages: %+v", pages)
	}
	for _, page := range pages {
		if page.PluginID != d.ID || page.PluginLabel != "Inventory" || page.OwnerID != "owner-a" {
			t.Fatalf("lost plugin identity: %+v", page)
		}
	}
	if pages[0].Page.Label != "Assets" || pages[1].Page.Label != "Jobs" {
		t.Fatal("page labels changed")
	}
	registration.Grants = nil
	if len(pluginPageDeclarations([]state.PluginRegistration{registration}, "owner-a")) != 0 {
		t.Fatal("unapproved roots exposed")
	}
}
