package plugins

import (
	"encoding/json"
	"testing"
)

func catalogDescription() Description {
	return Description{
		ID: "org.example.catalog", Name: "Catalog", Version: "1", Protocol: Version{Major: 1},
		Scope: ScopeOwner, MaxConcurrency: 1, WantsCatalog: true,
		Settings: []Setting{{Key: CatalogProjectSetting, Label: "Project", Type: "string", Required: true}},
	}
}

func TestCatalogDeclarationContract(t *testing.T) {
	if err := catalogDescription().Validate(); err != nil {
		t.Fatalf("rejected conforming description: %v", err)
	}
	for name, mutate := range map[string]func(*Description){
		"missing project": func(d *Description) { d.Settings = nil },
		"optional project": func(d *Description) {
			d.Settings = []Setting{{Key: CatalogProjectSetting, Label: "Project", Type: "string"}}
		},
		"secret project": func(d *Description) {
			d.Settings = []Setting{{Key: CatalogProjectSetting, Label: "Project", Type: "string", Required: true, Secret: true}}
		},
		"numeric project": func(d *Description) {
			d.Settings = []Setting{{Key: CatalogProjectSetting, Label: "Project", Type: "integer", Required: true}}
		},
		"list project": func(d *Description) {
			d.Settings = []Setting{{Key: CatalogProjectSetting, Label: "Project", Type: "string", Required: true, List: true}}
		},
		"reserved catalog key": func(d *Description) {
			d.Settings = append(d.Settings, Setting{Key: CatalogLaunchKey, Label: "Catalog", Type: "string"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := catalogDescription()
			mutate(&d)
			if d.Validate() == nil {
				t.Fatal("accepted")
			}
		})
	}
	// A plugin without the opt-in is unconstrained: neither the project trio
	// nor the catalog key mean anything to it.
	unconstrained := catalogDescription()
	unconstrained.WantsCatalog = false
	unconstrained.Settings = []Setting{{Key: CatalogLaunchKey, Label: "Catalog", Type: "string"}}
	if err := unconstrained.Validate(); err != nil {
		t.Fatalf("opted-out plugin rejected: %v", err)
	}
}

func TestLaunchCatalogRoundTrip(t *testing.T) {
	// The host delivers exactly the four catalog members; unknown platform
	// fields (commands, liveConnection) never reach the plugin, and a missing
	// models section stays omitted.
	data, err := json.Marshal(&LaunchCatalog{
		Agents:       []CatalogAgent{{Name: "build", Mode: "primary", BuiltIn: true}},
		Models:       &CatalogModels{HasProviders: true, Models: []CatalogModel{{Provider: "p", Model: "m", IsAvailable: true}}},
		DefaultAgent: "build",
		DefaultModel: "p/m",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"agents":[{"name":"build","mode":"primary","builtIn":true}],"models":{"hasProviders":true,"models":[{"provider":"p","model":"m","isAvailable":true}]},"defaultAgent":"build","defaultModel":"p/m"}` {
		t.Fatalf("wire shape: %s", data)
	}
	var got LaunchCatalog
	if json.Unmarshal(data, &got) != nil {
		t.Fatal("unmarshal")
	}
	if got.DefaultAgent != "build" || got.DefaultModel != "p/m" || len(got.Agents) != 1 || got.Agents[0].Name != "build" || !got.Agents[0].BuiltIn {
		t.Fatalf("round trip: %+v", got)
	}
	if got.Models == nil || !got.Models.HasProviders || len(got.Models.Models) != 1 || !got.Models.Models[0].IsAvailable {
		t.Fatalf("models: %+v", got.Models)
	}
}
