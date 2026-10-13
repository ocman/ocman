package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The ocman plugin lives in a local marketplace of the same name (AD-11);
// PluginID is how the CLI and settings.json's enabledPlugins name it.
const (
	marketplaceName = "ocman"
	PluginID        = "ocman@" + marketplaceName
)

// WritePlugin lays out a local marketplace in dir holding ocman's plugin
// and returns the plugin's directory. Launched sessions load it with
// --plugin-dir; the user may install it at user scope for live status of
// terminal sessions. hook is the command prefix each hook runs with its
// event name appended; empty writes no hooks.
func WritePlugin(dir string, skills map[string][]byte, hook string) (string, error) {
	plugin := filepath.Join(dir, "ocman")
	files := map[string][]byte{
		filepath.Join(dir, ".claude-plugin", "marketplace.json"): []byte(`{"name":"` + marketplaceName + `","owner":{"name":"ocman"},` +
			`"plugins":[{"name":"ocman","source":"./ocman","description":"ocman skills and session status hooks"}]}` + "\n"),
		filepath.Join(plugin, ".claude-plugin", "plugin.json"): []byte(`{"name":"ocman","version":"1.0.0","description":"ocman skills and session status hooks"}` + "\n"),
	}
	for name, content := range skills {
		files[filepath.Join(plugin, "skills", name, "SKILL.md")] = content
	}
	hooks := filepath.Join(plugin, "hooks", "hooks.json")
	if hook != "" {
		files[hooks] = hooksJSON(hook)
	} else if err := os.Remove(hooks); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
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
	for _, event := range HookEvents {
		m := matcher{Hooks: []command{{Type: "command", Command: hook + " " + event, Timeout: 5}}}
		if strings.HasSuffix(event, "ToolUse") {
			m.Matcher = "*"
		}
		all[event] = []matcher{m}
	}
	data, _ := json.MarshalIndent(map[string]any{"hooks": all}, "", "  ")
	return append(data, '\n')
}

// HookCommand is the hook command prefix for the ocman binary exe posting
// to the loopback listener at addr.
func HookCommand(exe, addr string) string {
	return `"` + exe + `" hook --addr ` + addr
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
	if o.ConfigEnv == nil {
		return nil
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") })
	if *o.ConfigEnv != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+*o.ConfigEnv)
	}
	return env
}
