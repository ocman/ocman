package codextransport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/platforms/codex/appserver/codextest"
)

func TestCodexInspectionEarlyFailures(t *testing.T) {
	for _, tc := range []struct{ name, file, kind, message string }{
		{"missing-home", "", "missing", "unavailable"},
		{"config-link", "config.toml", "link", "regular"},
		{"auth-directory", "auth.json", "directory", "regular"},
		{"start-error", "", "start", "could not start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			switch tc.kind {
			case "missing":
				home = filepath.Join(home, "absent")
			case "link":
				err = os.Symlink(filepath.Join(home, "absent"), filepath.Join(home, tc.file))
			case "directory":
				err = os.Mkdir(filepath.Join(home, tc.file), 0o700)
			}
			if err != nil {
				t.Fatal(err)
			}
			called := false
			got, err := inspectCodexConnection(t.Context(), home, "codex-cli 0.164.0", func(context.Context, string) (*codexappserver.Client, error) {
				called = true
				return nil, errors.New("fake start failure")
			})
			if err != nil || got.Ready || got.IsolationSupported || !strings.Contains(got.Message, tc.message) || called != (tc.kind == "start") {
				t.Fatalf("inspection = %+v, %v, start=%v", got, err, called)
			}
		})
	}
}

func TestCodexInspectionRejectsMalformedNativeResponses(t *testing.T) {
	for _, tc := range []struct {
		name, reportedHome, account, message string
		supported                            bool
	}{
		{"wrong-home", "/another-account", `{}`, "protocol", false},
		{"missing-auth-requirement", "", `{"account":null}`, "unsupported account", false},
		{"false-auth-requirement", "", `{"requiresOpenaiAuth":false}`, "unsupported account", false},
		{"bad-email", "", `{"requiresOpenaiAuth":true,"account":{"type":"chatgpt","email":"Display <person@example.invalid>"}}`, "email", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s, pipes := codextest.New(t)
			s.Handle("initialize", func(json.RawMessage) (any, error) {
				reported := home
				if tc.reportedHome != "" {
					reported = tc.reportedHome
				}
				return codexappserver.InitializeResponse{CodexHome: reported}, nil
			})
			s.Handle("config/read", func(json.RawMessage) (any, error) {
				return map[string]any{"config": map[string]any{}, "layers": []any{}}, nil
			})
			s.Handle("account/read", func(json.RawMessage) (any, error) { return json.RawMessage(tc.account), nil })
			got, err := inspectCodexConnection(t.Context(), home, "codex-cli 0.165.1", func(ctx context.Context, _ string) (*codexappserver.Client, error) {
				return codexappserver.Attach(ctx, pipes.Reader, pipes.Writer, codexappserver.Options{})
			})
			if err != nil || got.Ready || got.IsolationSupported != tc.supported || !strings.Contains(got.Message, tc.message) {
				t.Fatalf("inspection = %+v, %v", got, err)
			}
		})
	}
}

func TestCodexInspectionAdditionalLayerPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		supported bool
	}{
		{"disabled-project", `{"config":{},"layers":[{"name":{"type":"project"},"disabledReason":"ignored","config":{"api_key":"ignored"}}]}`, true},
		{"empty-session", `{"config":{},"layers":[{"name":{"type":"sessionFlags"},"config":{}}]}`, true},
		{"session-overrides", `{"config":{},"layers":[{"name":{"type":"sessionFlags"},"config":{"model":"other"}}]}`, false},
		{"safe-provider", `{"config":{"model_provider":"openai","cli_auth_credentials_store":"file","forced_login_method":"chatgpt","profiles":{},"optional":null,"ui":{"theme":"dark"}},"layers":[]}`, true},
		{"forced-api-key", `{"config":{"forced_login_method":"api"},"layers":[]}`, false},
		{"nested-token", `{"config":{"nested":{"refresh_token":"secret"}},"layers":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c codexInspectionConfig
			if err := json.Unmarshal([]byte(tc.raw), &c); err != nil {
				t.Fatal(err)
			}
			if got := c.supported("/accounts/work"); got != tc.supported {
				t.Fatalf("supported=%v, want %v", got, tc.supported)
			}
		})
	}
}

func TestCodexInspectionCLIUnavailableAndCanceled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got, err := InspectConfiguration(t.Context(), t.TempDir())
	if err != nil || got.Ready || !strings.Contains(got.Message, "Install") {
		t.Fatalf("missing CLI = %+v, %v", got, err)
	}
	p := &Platform{}
	got, err = p.InspectConnection(t.Context())
	if err != nil || !strings.Contains(got.Message, "legacy") {
		t.Fatalf("legacy = %+v, %v", got, err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err = inspectCodexCLI(t.Context(), filepath.Join(home, "missing"), home)
	if err != nil || !strings.Contains(got.Message, "failed") {
		t.Fatalf("version failure = %+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := inspectCodexCLI(ctx, "never-execute", home); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	if _, err := inspectCodexConnection(ctx, home, "codex-cli 0.164.0", func(context.Context, string) (*codexappserver.Client, error) { return nil, context.Canceled }); !errors.Is(err, context.Canceled) {
		t.Fatalf("start cancel = %v", err)
	}
	t.Setenv("TMPDIR", filepath.Join(home, "missing"))
	if _, err := inspectCodexConnection(t.Context(), home, "codex-cli 0.164.0", nil); err == nil {
		t.Fatal("missing temporary root accepted")
	}
}
