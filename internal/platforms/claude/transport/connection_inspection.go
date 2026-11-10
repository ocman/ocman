package claudecode

import (
	"context"
	"encoding/json"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platforms"
	claudecli "github.com/ocman/ocman/internal/platforms/claude/launch"
)

var claudeInspectionVersion = regexp.MustCompile(`^2\.1\.([0-9]+)(?: \(Claude Code\))?$`)

// InspectConfiguration checks a prepared account before it is saved. It creates
// no runtime, ownership rows or native credentials.
func InspectConfiguration(ctx context.Context, directory string) (platforms.ConnectionInspection, error) {
	cli, err := exec.LookPath("claude")
	if err != nil {
		return platforms.ConnectionInspection{Message: "Install a supported Claude CLI before checking this account."}, nil
	}
	p := &claudePlatform{cli: cli, configDir: directory, connectionAccount: true}
	return p.InspectConnection(ctx)
}

// InspectConnection checks native authentication, never subscription usage.
func (p *claudePlatform) InspectConnection(ctx context.Context) (platforms.ConnectionInspection, error) {
	out := platforms.ConnectionInspection{Message: "Named Claude account inspection is unavailable for the legacy machine configuration."}
	if !p.connectionAccount {
		return out, nil
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		out.Message = "Named Claude account inspection does not support this owner's native policy store."
		return out, nil
	}
	config, err := local.CanonicalProviderRoot(p.configDir)
	if err != nil || config != p.configDir {
		out.Message = "Claude configuration directory is unavailable or changed."
		return out, nil
	}
	if !claudeInspectionSettings(config) {
		out.Message = "Claude native auth overrides or managed policy cannot be isolated. Remove account/provider helpers, auth environment overrides or ask the owner administrator to review managed policy."
		return out, nil
	}
	// Native auth status initializes .claude.json when absent. Do not turn a
	// readiness check into native setup; native sign-in must initialize it.
	if _, err := os.Stat(filepath.Join(config, ".claude.json")); os.IsNotExist(err) {
		out.Message = "Claude account is not initialized. Sign in natively using this configuration directory, then check again."
		return out, nil
	} else if err != nil {
		out.Message = "Claude native account metadata is unreadable."
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	neutral, err := os.MkdirTemp("", "ocman-claude-auth-")
	if err != nil {
		return out, err
	}
	defer func() { _ = os.RemoveAll(neutral) }()
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, p.cli, args...)
		cmd.Dir, cmd.Env, cmd.WaitDelay = neutral, claudecli.AccountEnvironment(config, neutral), time.Second
		return cmd.Output()
	}
	version, err := run("--version")
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	match := claudeInspectionVersion.FindStringSubmatch(strings.TrimSpace(string(version)))
	if err != nil || len(match) != 2 {
		out.Message = "Unsupported Claude CLI version. Named accounts require Claude Code 2.1.268 or later in the 2.1 release series."
		return out, nil
	}
	patch, _ := strconv.Atoi(match[1])
	if patch < 268 {
		out.Message = "Upgrade Claude Code to 2.1.268 or later to verify its selected configuration directory."
		return out, nil
	}
	out.NativeVersion = "2.1." + match[1]
	data, runErr := run("--setting-sources", "user", "auth", "status", "--json")
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var status struct {
		LoggedIn        *bool  `json:"loggedIn"`
		AuthMethod      string `json:"authMethod"`
		ConfigDirectory string `json:"configDirectory"`
		Email           string `json:"email"`
	}
	if json.Unmarshal(data, &status) != nil || status.LoggedIn == nil || status.ConfigDirectory != config {
		out.Message = "Claude did not verify the selected account directory. Its auth-status response is unsupported."
		return out, nil
	}
	if !*status.LoggedIn && status.AuthMethod == "none" {
		out.IsolationSupported = true
		out.Message = "Sign in to Claude in this configuration directory, then check again."
		return out, nil
	}
	if runErr != nil || !*status.LoggedIn || status.AuthMethod != "claude.ai" {
		out.Message = "Only native Claude subscription authentication is supported for named accounts; API keys, helpers, cloud providers and externally supplied OAuth tokens are not supported."
		return out, nil
	}
	label, err := mail.ParseAddress(status.Email)
	if err != nil || label.Address != status.Email || len(status.Email) > 254 || strings.ContainsAny(status.Email, "\r\n") {
		out.Message = "Claude reported no usable account email. Sign in to the selected subscription account and check again."
		return out, nil
	}
	out.Ready, out.IsolationSupported, out.AccountLabel, out.AuthMode = true, true, label.Address, "claude.ai"
	out.Message = "Native subscription verified. Named mode loads user settings only; project and local Claude settings are disabled."
	return out, nil
}

func claudeInspectionSettings(config string) bool {
	if info, err := os.Lstat(filepath.Join(config, ".credentials.json")); !os.IsNotExist(err) && (err != nil || !info.Mode().IsRegular()) {
		return false
	}
	// Managed settings cannot be disabled by --setting-sources. Do not guess
	// whether a policy file or macOS MDM payload can redirect authentication.
	for _, path := range []string{
		"/Library/Application Support/ClaudeCode/managed-settings.json",
		"/Library/Application Support/ClaudeCode/managed-settings.d",
		"/Library/Managed Preferences/com.anthropic.claudecode.plist",
		"/etc/claude-code/managed-settings.json", "/etc/claude-code/managed-settings.d",
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return false
		}
	}
	if home, err := os.UserHomeDir(); err != nil {
		return false
	} else if _, err := os.Lstat(filepath.Join(home, "Library", "Managed Preferences", "com.anthropic.claudecode.plist")); !os.IsNotExist(err) {
		return false
	}
	for _, name := range []string{"settings.json", ".claude.json", "config.json", "managed-settings.json", "policy-limits.json", "remote-settings.json"} {
		path := filepath.Join(config, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return false
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		var value map[string]any
		if json.Unmarshal(data, &value) != nil || unsafeClaudeAuthSettings(value) {
			return false
		}
		if name == "managed-settings.json" || name == "policy-limits.json" {
			return false
		}
		if name == "remote-settings.json" && len(value) != 0 {
			return false
		}
	}
	return true
}

func unsafeClaudeAuthSettings(value map[string]any) bool {
	for key, item := range value {
		lower := strings.ToLower(key)
		if lower == "oauthaccount" {
			continue
		} // native non-secret login metadata
		if strings.Contains(lower, "apikey") || strings.Contains(lower, "auth") || strings.Contains(lower, "profile") || strings.HasPrefix(lower, "aws") || strings.HasPrefix(lower, "forcelogin") {
			return true
		}
		if lower == "env" {
			env, ok := item.(map[string]any)
			if !ok {
				return true
			}
			for name := range env {
				if strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_") || strings.HasPrefix(name, "AWS_") || strings.HasPrefix(name, "GOOGLE_") || strings.HasPrefix(name, "AZURE_") {
					return true
				}
			}
		}
	}
	return false
}

var _ platforms.ConnectionInspector = (*claudePlatform)(nil)
