package codextransport

import (
	"context"
	"encoding/json"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

var codexInspectionVersion = regexp.MustCompile(`^codex-cli (0\.(?:164|165)\.[0-9]+)$`)

// InspectConfiguration checks a prepared account without persisted connections
// or a supervisor. Only initialize, config/read and account/read are used.
func InspectConfiguration(ctx context.Context, directory string) (platforms.ConnectionInspection, error) {
	cli, err := exec.LookPath("codex")
	if err != nil {
		return platforms.ConnectionInspection{Message: "Install a supported Codex CLI before checking this account."}, nil
	}
	return inspectCodexCLI(ctx, cli, directory)
}

func inspectCodexCLI(ctx context.Context, cli, directory string) (platforms.ConnectionInspection, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	version := exec.CommandContext(ctx, cli, "--version")
	version.Env, version.Dir, version.WaitDelay = connectionEnvironment(os.Environ(), directory), directory, time.Second
	data, err := version.Output()
	if ctx.Err() != nil {
		return platforms.ConnectionInspection{}, ctx.Err()
	}
	if err != nil {
		return platforms.ConnectionInspection{Message: "Codex version inspection failed. Check the installed native CLI."}, nil
	}
	return inspectCodexConnection(ctx, directory, strings.TrimSpace(string(data)), func(ctx context.Context, dir string) (*codexappserver.Client, error) {
		return codexappserver.Start(ctx, codexappserver.Options{CLI: cli, Dir: dir, Env: connectionEnvironment(os.Environ(), directory), Client: codexappserver.ClientInfo{Name: "ocman", Title: "Account inspection", Version: "1"}})
	})
}

func (p *Platform) InspectConnection(ctx context.Context) (platforms.ConnectionInspection, error) {
	if p.mgr == nil || !p.mgr.CanonicalRoots {
		return platforms.ConnectionInspection{Message: "Named Codex inspection is unavailable for the legacy machine configuration."}, nil
	}
	return inspectCodexCLI(ctx, p.cli, p.home)
}

func inspectCodexConnection(ctx context.Context, home, nativeVersion string, start func(context.Context, string) (*codexappserver.Client, error)) (platforms.ConnectionInspection, error) {
	out := platforms.ConnectionInspection{}
	version := codexInspectionVersion.FindStringSubmatch(nativeVersion)
	if len(version) != 2 {
		out.Message = "Unsupported Codex CLI version. Named accounts currently require Codex 0.164.x or 0.165.x."
		return out, nil
	}
	out.NativeVersion = version[1]
	canonical, err := local.CanonicalProviderRoot(home)
	if err != nil || canonical != home {
		out.Message = "Codex account directory is unavailable or changed."
		return out, nil
	}
	for _, name := range []string{"config.toml", "auth.json"} {
		if info, err := os.Lstat(filepath.Join(home, name)); !os.IsNotExist(err) && (err != nil || !info.Mode().IsRegular()) {
			out.Message = "Codex account config and credentials must be regular account-local files, not links or unknown stores."
			return out, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	neutral, err := os.MkdirTemp("", "ocman-codex-auth-")
	if err != nil {
		return out, err
	}
	defer func() { _ = os.RemoveAll(neutral) }()
	c, err := start(ctx, neutral)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Message = "Codex account inspection could not start. Check the installed CLI and selected account directory."
		return out, nil
	}
	defer func() { _ = c.Close() }()
	if c.Info.CodexHome != home {
		out.Message = "Unsupported Codex account protocol or account directory. Named accounts currently require Codex 0.164.x or 0.165.x with native home verification."
		return out, nil
	}
	var config codexInspectionConfig
	err = c.Call(ctx, "config/read", struct {
		IncludeLayers bool `json:"includeLayers"`
	}{true}, &config)
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err != nil || !config.supported(home) {
		out.Message = "Codex configuration cannot isolate this subscription. Use native file credential storage without provider/profile/auth overrides; owner administrators must review managed policy."
		return out, nil
	}
	account, err := c.ReadAccount(ctx)
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err != nil || account.RequiresOpenaiAuth == nil || !*account.RequiresOpenaiAuth {
		out.Message = "Codex returned an unsupported account response. Check the selected native CLI version."
		return out, nil
	}
	out.IsolationSupported = true
	if account.Account == nil {
		out.Message = "Sign in to Codex in this account directory, then check again."
		return out, nil
	}
	if account.Account.Type != "chatgpt" {
		out.IsolationSupported = false
		out.Message = "Only native ChatGPT subscriptions using account-local file credentials are supported; API keys and cloud/provider accounts are unsupported."
		return out, nil
	}
	label, err := mail.ParseAddress(account.Account.Email)
	if err != nil || label.Address != account.Account.Email || len(label.Address) > 254 || strings.ContainsAny(label.Address, "\r\n") {
		out.Message = "Codex reported no usable account email. Sign in to the selected ChatGPT account and check again."
		return out, nil
	}
	out.Ready, out.AccountLabel, out.AuthMode = true, label.Address, "chatgpt"
	out.Message = "Native subscription verified. Named mode rejects project configuration before execution."
	return out, nil
}

type codexInspectionConfig struct {
	Config map[string]any `json:"config"`
	Layers []struct {
		Name struct {
			Type, File string
			Profile    *string
		} `json:"name"`
		Config         map[string]any `json:"config"`
		DisabledReason *string        `json:"disabledReason"`
	} `json:"layers"`
}

func (c codexInspectionConfig) supported(home string) bool {
	if c.Config == nil || c.Layers == nil || unsafeCodexConfig(c.Config) {
		return false
	}
	for _, layer := range c.Layers {
		if layer.DisabledReason != nil {
			continue
		}
		switch layer.Name.Type {
		case "user":
			if layer.Name.File != filepath.Join(home, "config.toml") || layer.Name.Profile != nil {
				return false
			}
		case "system", "packagedDefaults":
		case "sessionFlags":
			if len(layer.Config) != 0 {
				return false
			}
		default:
			return false
		}
		if unsafeCodexConfig(layer.Config) {
			return false
		}
	}
	return true
}

func unsafeCodexConfig(value map[string]any) bool {
	for key, item := range value {
		if item == nil {
			continue
		}
		switch key {
		case "model_provider":
			if item != "openai" {
				return true
			}
		case "cli_auth_credentials_store":
			if item != "file" {
				return true
			}
		case "forced_login_method":
			if item != "chatgpt" {
				return true
			}
		case "profiles", "model_providers":
			if data, _ := json.Marshal(item); string(data) != "{}" {
				return true
			}
		default:
			lower := strings.ToLower(key)
			if strings.Contains(lower, "auth") || strings.Contains(lower, "bearer_token") || strings.Contains(lower, "access_token") || strings.Contains(lower, "refresh_token") || strings.Contains(lower, "id_token") || strings.Contains(lower, "api_key") || strings.Contains(lower, "base_url") || strings.Contains(lower, "profile") || strings.Contains(lower, "env_key") {
				return true
			}
			if nested, ok := item.(map[string]any); ok && unsafeCodexConfig(nested) {
				return true
			}
		}
	}
	return false
}

var _ platforms.ConnectionInspector = (*Platform)(nil)
