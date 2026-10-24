package claudeprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudecli"
)

// The ocman plugin lives in a local marketplace of the same name (AD-11);
// PluginID is how the CLI and settings.json's enabledPlugins name it.
const (
	marketplaceName = "ocman"
	PluginID        = "ocman@" + marketplaceName
)

// WritePlugin lays out a local marketplace in dir holding ocman's plugin
// and returns the plugin's directory. Launched sessions load it with
// --plugin-dir; the user may also install it at user scope. Hooks only run
// in managed processes. hook is the command prefix each hook runs with its
// event name appended; empty writes no hooks.
func WritePlugin(dir string, skills map[string][]byte, hook string) (string, error) {
	plugin := filepath.Join(dir, "ocman")
	content := map[string][]byte{}
	for name, body := range skills {
		content[filepath.Join(plugin, "skills", name, "SKILL.md")] = body
	}
	hooks := filepath.Join(plugin, "hooks", "hooks.json")
	if hook != "" {
		content[hooks] = hooksJSON(hook)
	} else if err := os.Remove(hooks); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	// Claude runs a cached copy of an installed plugin and refreshes it only
	// when the version changes, so the version follows the plugin's content.
	files := map[string][]byte{
		filepath.Join(dir, ".claude-plugin", "marketplace.json"): []byte(`{"name":"` + marketplaceName + `","owner":{"name":"ocman"},` +
			`"plugins":[{"name":"ocman","source":"./ocman","description":"ocman skills, session status and history hooks"}]}` + "\n"),
		filepath.Join(plugin, ".claude-plugin", "plugin.json"): []byte(`{"name":"ocman","version":"` + contentVersion(plugin, content) +
			`","description":"ocman skills, session status and history hooks"}` + "\n"),
	}
	maps.Copy(files, content)
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return "", err
		}
	}
	return plugin, nil
}

func hooksJSON(hook string) []byte {
	type command struct {
		Type    string `json:"type"`
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	type matcher struct {
		Matcher string    `json:"matcher,omitempty"`
		Hooks   []command `json:"hooks"`
	}
	all := map[string][]matcher{}
	for _, event := range append(slices.Clone(HookEvents), HistoryHookEvents...) {
		if all[event] != nil {
			continue
		}
		// Status-only hooks check before executing ocman, whose installed path
		// may no longer exist. History hooks run in every session; ocman itself
		// posts status only when managed. A missing binary stays silent.
		cmd := `if [ "${OCMAN_MANAGED:-}" = "1" ]; then ` + hook + " " + event + "; fi"
		if slices.Contains(HistoryHookEvents, event) {
			cmd = hook + " " + event + " 2>/dev/null || true"
		}
		m := matcher{Hooks: []command{{Type: "command", Command: cmd, Timeout: 5}}}
		if strings.HasSuffix(event, "ToolUse") {
			m.Matcher = "*"
		}
		all[event] = []matcher{m}
	}
	data, _ := json.MarshalIndent(map[string]any{"hooks": all}, "", "  ")
	return append(data, '\n')
}

// HookCommand is the hook command prefix for the ocman binary exe. Status
// posts to the loopback listener at addr; history appends to journal. Either
// may be empty.
func HookCommand(exe, addr, journal string) string {
	cmd := `"` + exe + `" hook`
	if addr != "" {
		cmd += " --addr " + addr
	}
	if journal != "" {
		cmd += " --journal '" + strings.ReplaceAll(journal, "'", `'\''`) + "'"
	}
	return cmd
}

func contentVersion(root string, files map[string][]byte) string {
	paths := slices.Sorted(maps.Keys(files))
	h := sha256.New()
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(files[path]))
		h.Write(files[path])
	}
	return "1.0.0-" + hex.EncodeToString(h.Sum(nil))[:12]
}

// PluginVersion reads the version WritePlugin last wrote to marketplace dir.
func PluginVersion(marketplace string) string {
	var manifest struct{ Version string }
	data, err := os.ReadFile(filepath.Join(marketplace, "ocman", ".claude-plugin", "plugin.json"))
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return manifest.Version
}

// InstallPlugin registers the local marketplace and installs the plugin at
// user scope through the CLI (FR-6); ocman never edits ~/.claude itself.
func InstallPlugin(ctx context.Context, cli, marketplace string) error {
	// Registering twice fails; the install below reports a real problem.
	added := runCLI(ctx, cli, "plugin", "marketplace", "add", marketplace)
	if err := runCLI(ctx, cli, "plugin", "install", PluginID, "--scope", "user"); err != nil {
		return errors.Join(added, err)
	}
	return nil
}

// InstalledVersion is the plugin version Claude cached at user scope, or
// empty when it is not installed there.
func InstalledVersion(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, "plugins", "installed_plugins.json"))
	if err != nil {
		return ""
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope   string `json:"scope"`
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &installed) != nil {
		return ""
	}
	for _, entry := range installed.Plugins[PluginID] {
		if entry.Scope == "user" {
			return entry.Version
		}
	}
	return ""
}

// EnsurePlugin keeps ocman's plugin installed at user scope and refreshes
// Claude's cached copy when marketplace holds a different version, so hook
// changes reach sessions started in a terminal. It reports what it did.
func EnsurePlugin(ctx context.Context, cli, configDir, marketplace string) (string, error) {
	want := PluginVersion(marketplace)
	if want == "" {
		return "", fmt.Errorf("ocman's Claude plugin has not been written")
	}
	if !PluginInstalled(configDir) {
		return "installed", InstallPlugin(ctx, cli, marketplace)
	}
	if InstalledVersion(configDir) == want {
		return "", nil
	}
	if err := runCLI(ctx, cli, "plugin", "marketplace", "update", marketplaceName); err != nil {
		return "", err
	}
	return "updated", runCLI(ctx, cli, "plugin", "update", PluginID, "--scope", "user")
}

// UninstallPlugin removes the plugin and its marketplace through the CLI.
func UninstallPlugin(ctx context.Context, cli string) error {
	if err := runCLI(ctx, cli, "plugin", "uninstall", PluginID, "--scope", "user"); err != nil {
		return err
	}
	return runCLI(ctx, cli, "plugin", "marketplace", "remove", marketplaceName)
}

// PluginInstalled reports whether the user's settings enable the plugin.
func PluginInstalled(configDir string) bool {
	data, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		return false
	}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	return json.Unmarshal(data, &settings) == nil && settings.EnabledPlugins[PluginID]
}

func runCLI(ctx context.Context, cli string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("claude %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// MCPConfig is the --mcp-config JSON pointing claude at ocman's loopback MCP
// listener; empty when that listener is disabled.
func MCPConfig(addr string) string {
	if addr == "" {
		return ""
	}
	return `{"mcpServers":{"ocman":{"type":"http","url":"http://` + addr + `/mcp"}}}`
}

// launchConfig is what the dashboard publishes for supervisors launched by
// an earlier dashboard process: values bound after they started.
type launchConfig struct {
	MCPAddr string `json:"mcpAddr"`
}

// WriteLaunchConfig records the dashboard's bound MCP listener for every
// project supervisor's next child (Options.LaunchPath).
func WriteLaunchConfig(path, mcpAddr string) error {
	data, _ := json.Marshal(launchConfig{MCPAddr: mcpAddr})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path+".tmp", data, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// mcpConfig is the --mcp-config for a child spawned now.
func (o Options) mcpConfig() string {
	if o.LaunchPath != "" {
		var c launchConfig
		if data, err := os.ReadFile(o.LaunchPath); err == nil && json.Unmarshal(data, &c) == nil && c.MCPAddr != "" {
			return MCPConfig(c.MCPAddr)
		}
	}
	return o.MCPConfig
}

// childEnv is a claude child's environment: this process's, with the
// dashboard's CLAUDE_CONFIG_DIR when one was carried (nil inherits).
func (o Options) childEnv() []string {
	return claudecli.Environment(o.ConfigEnv, "")
}
