package plugin_test

import (
	"encoding/json"
	"testing"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

func TestParseConfigurationWithCatalog(t *testing.T) {
	line := []byte(`{"project":"/srv/repo","token":"s3cret","catalog":{"agents":[{"name":"build","mode":"primary","builtIn":true}],"models":{"hasProviders":true,"models":[{"provider":"acme","model":"quill","isAvailable":true}]},"defaultAgent":"build","defaultModel":"acme/quill"}}` + "\n")
	cfg, err := plugin.ParseConfiguration(line)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := cfg.Values[plugin.CatalogLaunchKey]; exists {
		t.Fatal("catalog leaked into settings")
	}
	if string(cfg.Values["project"]) != `"/srv/repo"` || string(cfg.Values["token"]) != `"s3cret"` {
		t.Fatalf("settings: %v", cfg.Values)
	}
	if cfg.Catalog == nil {
		t.Fatal("missing catalog")
	}
	if cfg.Catalog.DefaultAgent != "build" || cfg.Catalog.DefaultModel != "acme/quill" {
		t.Fatalf("defaults: %+v", cfg.Catalog)
	}
	if len(cfg.Catalog.Agents) != 1 || cfg.Catalog.Agents[0].Name != "build" || !cfg.Catalog.Agents[0].BuiltIn {
		t.Fatalf("agents: %+v", cfg.Catalog.Agents)
	}
	if cfg.Catalog.Models == nil || !cfg.Catalog.Models.HasProviders || len(cfg.Catalog.Models.Models) != 1 || cfg.Catalog.Models.Models[0].Provider != "acme" {
		t.Fatalf("models: %+v", cfg.Catalog.Models)
	}
}

func TestParseConfigurationWithoutCatalog(t *testing.T) {
	// Opted-out plugins and opted-in plugins whose platform was unreachable
	// receive settings only; the section is simply absent.
	cfg, err := plugin.ParseConfiguration([]byte(`{"project":"/srv/repo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Catalog != nil {
		t.Fatalf("unexpected catalog: %+v", cfg.Catalog)
	}
	if cfg.Catalog != nil || len(cfg.Values) != 1 || string(cfg.Values["project"]) != `"/srv/repo"` {
		t.Fatalf("values: %v", cfg.Values)
	}
	if _, err := plugin.ParseConfiguration([]byte(`{`)); err == nil {
		t.Fatal("accepted invalid JSON")
	}
	if _, err := plugin.ParseConfiguration([]byte(`{"catalog":{"agents":"not-a-list"}}`)); err == nil {
		t.Fatal("accepted invalid catalog")
	}
}

func TestParseConfigurationOptedOutCatalogSetting(t *testing.T) {
	// Setting values are never JSON objects, so an opted-out plugin's own
	// "catalog" setting stays an ordinary value.
	for _, raw := range []string{`"weekly"`, `["a","b"]`, `true`, `3`} {
		cfg, err := plugin.ParseConfiguration([]byte(`{"catalog":` + raw + `}`))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if cfg.Catalog != nil || string(cfg.Values[plugin.CatalogLaunchKey]) != raw {
			t.Fatalf("%s: %+v", raw, cfg)
		}
	}
}

func TestLaunchCatalogAliasSharesHostType(t *testing.T) {
	// The SDK reader must agree with the host on the wire shape.
	data, err := json.Marshal(plugin.LaunchCatalog{Agents: []plugin.CatalogAgent{{Name: "build"}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"agents":[{"name":"build"}]}` {
		t.Fatalf("wire shape: %s", data)
	}
}
