package plugin_test

import (
	"testing"

	"github.com/ocman/ocman/sdk/plugin"
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
	// receive settings only; the section is simply absent. Setting values are
	// never JSON objects, so an opted-out plugin's own "catalog" setting stays
	// an ordinary value.
	for _, tc := range []struct {
		line, key, want string
		bad             bool
	}{
		{line: `{"project":"/srv/repo"}`, key: "project", want: `"/srv/repo"`},
		{line: `{"catalog":"weekly"}`, key: plugin.CatalogLaunchKey, want: `"weekly"`},
		{line: `{"catalog":["a","b"]}`, key: plugin.CatalogLaunchKey, want: `["a","b"]`},
		{line: `{"catalog":true}`, key: plugin.CatalogLaunchKey, want: `true`},
		{line: `{"catalog":3}`, key: plugin.CatalogLaunchKey, want: `3`},
		{line: `{`, bad: true},
		{line: `{"catalog":{"agents":"not-a-list"}}`, bad: true},
	} {
		cfg, err := plugin.ParseConfiguration([]byte(tc.line))
		if tc.bad {
			if err == nil {
				t.Fatalf("%s: accepted invalid input", tc.line)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.line, err)
		}
		if cfg.Catalog != nil || len(cfg.Values) != 1 || string(cfg.Values[tc.key]) != tc.want {
			t.Fatalf("%s: %+v", tc.line, cfg)
		}
	}
}
