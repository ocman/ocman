package codextransport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
)

func TestCodexNativeConnectionInspection(t *testing.T) {
	// Effective defaults from a clean Codex 0.164.0 config/read with empty layers.
	defaults := map[string]any{
		"chatgpt_base_url":            "https://chatgpt.com/backend-api/",
		"cli_auth_credentials_store":  "file",
		"mcp_oauth_credentials_store": "auto",
		"features": map[string]any{
			"api_key_cyber_access_programs": true,
			"api_key_model_discovery":       true,
			"auth_elicitation":              true,
		},
	}
	for _, tc := range []struct {
		name, version, mode      string
		config, userConfig       map[string]any
		logged, ready, supported bool
	}{
		{"subscription", "0.164.0", "chatgpt", defaults, nil, true, true, true},
		{"logged-out", "0.164.0", "chatgpt", defaults, nil, false, false, true},
		{"unsupported", "0.163.0", "chatgpt", defaults, nil, true, false, false},
		{"unknown-version", "0.166.0", "chatgpt", defaults, nil, true, false, false},
		{"keyring", "0.164.0", "chatgpt", map[string]any{"cli_auth_credentials_store": "keyring"}, nil, true, false, false},
		{"profile", "0.164.0", "chatgpt", map[string]any{"profile": "owner"}, nil, true, false, false},
		{"provider", "0.164.0", "chatgpt", map[string]any{"model_provider": "other"}, nil, true, false, false},
		{"credentials", "0.164.0", "chatgpt", map[string]any{"model_providers": map[string]any{"openai": map[string]any{"env_key": "OWNER_SECRET"}}}, nil, true, false, false},
		{"api-key", "0.164.0", "apiKey", defaults, nil, true, false, false},
		{"custom-endpoint", "0.164.0", "chatgpt", map[string]any{"chatgpt_base_url": "https://other.example.invalid"}, nil, true, false, false},
		{"explicit-endpoint", "0.164.0", "chatgpt", defaults, map[string]any{"chatgpt_base_url": "https://chatgpt.com/backend-api/"}, true, false, false},
		{"explicit-auth-feature", "0.164.0", "chatgpt", defaults, map[string]any{"features": map[string]any{"auth_elicitation": true}}, true, false, false},
		{"nondefault-oauth-store", "0.164.0", "chatgpt", map[string]any{"mcp_oauth_credentials_store": "keyring"}, nil, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := filepath.EvalSymlinks(t.TempDir())
			s, pipes := codextest.New(t)
			s.Handle("initialize", func(json.RawMessage) (any, error) {
				// The recorded native protocol reports the client agent here,
				// not the installed native version. Never use it for gating.
				return codexappserver.InitializeResponse{UserAgent: "ocman/test", CodexHome: home}, nil
			})
			s.Handle("config/read", func(raw json.RawMessage) (any, error) {
				var params map[string]any
				_ = json.Unmarshal(raw, &params)
				if params["includeLayers"] != true {
					t.Error("configuration layers not inspected")
				}
				userConfig := tc.userConfig
				if userConfig == nil {
					userConfig = map[string]any{}
				}
				return map[string]any{"config": tc.config, "layers": []any{
					map[string]any{"name": map[string]any{"type": "user", "file": filepath.Join(home, "config.toml")}, "config": userConfig},
					map[string]any{"name": map[string]any{"type": "system"}, "config": map[string]any{}},
				}}, nil
			})
			s.Handle("account/read", func(raw json.RawMessage) (any, error) {
				var params map[string]any
				_ = json.Unmarshal(raw, &params)
				if value, ok := params["refreshToken"]; !ok || value != false {
					t.Error("account inspection may refresh credentials")
				}
				var account any
				if tc.logged {
					account = map[string]any{"type": tc.mode, "email": "person@example.invalid", "planType": "plus"}
				}
				return map[string]any{"account": account, "requiresOpenaiAuth": true}, nil
			})
			got, err := inspectCodexConnection(t.Context(), home, "codex-cli "+tc.version, func(ctx context.Context, directory string) (*codexappserver.Client, error) {
				if directory == home || !filepath.IsAbs(directory) {
					t.Error("inspection did not use a neutral directory")
				}
				return codexappserver.Attach(ctx, pipes.Reader, pipes.Writer, codexappserver.Options{})
			})
			if err != nil || got.Ready != tc.ready || got.IsolationSupported != tc.supported || got.Message == "" {
				t.Fatalf("inspection: %+v %v", got, err)
			}
			if got.Ready && (got.AccountLabel != "person@example.invalid" || got.AuthMode != "chatgpt") {
				t.Fatalf("identity: %+v", got)
			}
			for _, call := range s.Calls() {
				if call.Method != "initialize" && call.Method != "config/read" && call.Method != "account/read" {
					t.Fatalf("active native call %s", call.Method)
				}
			}
		})
	}
}

func TestCodexInspectionLayerPolicy(t *testing.T) {
	home := "/accounts/work"
	for _, raw := range []string{
		`{"config":{},"layers":null}`,
		`{"config":{},"layers":[{"name":{"type":"mdm"},"config":{}}]}`,
		`{"config":{},"layers":[{"name":{"type":"project"},"config":{}}]}`,
		`{"config":{},"layers":[{"name":{"type":"user","file":"/accounts/other/config.toml"},"config":{}}]}`,
		`{"config":{},"layers":[{"name":{"type":"user","file":"/accounts/work/config.toml","profile":"owner"},"config":{}}]}`,
		`{"config":{},"layers":[{"name":{"type":"system"},"config":{"experimental_bearer_token":"secret"}}]}`,
	} {
		var config codexInspectionConfig
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatal(err)
		}
		if config.supported(home) {
			t.Fatal("unsupported native layer accepted")
		}
	}
}

func TestCodexStandaloneInspectionUsesFilteredNativeAccount(t *testing.T) {
	r := newRig(t)
	bin := t.TempDir()
	if err := os.Symlink(r.exe, filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_CODEX_INSPECTION", "1")
	t.Setenv("FAKE_CODEX_CONNECTION_ACCOUNT", "1")
	t.Setenv("CODEX_HOME", "/inherited-owner")
	t.Setenv("OPENAI_API_KEY", "owner-secret")
	t.Setenv("CODEX_ACCESS_TOKEN", "owner-secret")
	home, _ := filepath.EvalSymlinks(t.TempDir())
	got, err := InspectConfiguration(t.Context(), home)
	if err != nil || !got.Ready || !got.IsolationSupported || got.AccountLabel != "person@example.invalid" {
		t.Fatalf("standalone inspection: %+v %v", got, err)
	}
}
