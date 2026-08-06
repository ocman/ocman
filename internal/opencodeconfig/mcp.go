// Package opencodeconfig inspects and updates OpenCode's global config
// file, so ocman can register itself as an MCP server without the user
// hand-editing JSON.
//
// Scope is deliberately narrow: read the global config, report whether
// the ocman MCP entry points at the right URL, and write that one entry.
// Nothing else in the file is interpreted.
package opencodeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ServerName is the key ocman owns under the config's "mcp" object.
const ServerName = "ocman"

// backupStamp is the suffix format for the pre-write copy. It carries a
// time as well as a date so installing twice in one day can't overwrite
// the pristine original with an already-modified file.
const backupStamp = "2006-01-02T150405"

// ErrNotEditable is returned when the config exists but ocman won't
// rewrite it (JSONC comments would be lost, or it isn't a JSON object).
// Callers should surface the message and fall back to manual setup.
var ErrNotEditable = errors.New("opencode config cannot be edited automatically")

// Status describes whether ocman is registered as an MCP server.
type Status struct {
	// Path is the config file ocman would read/write. It may not exist
	// yet, in which case Install creates it.
	Path string `json:"path"`
	// Configured is true when the ocman entry exists, is enabled, and
	// points at WantURL.
	Configured bool `json:"configured"`
	// CurrentURL is the URL already configured for the ocman entry, if
	// any. Non-empty with Configured=false means the entry is stale —
	// typically pointing at an old port.
	CurrentURL string `json:"currentUrl,omitempty"`
	// WantURL is the URL ocman would write.
	WantURL string `json:"wantUrl"`
	// Editable is false when Install would refuse (see ErrNotEditable);
	// Reason then explains why, for display.
	Editable bool   `json:"editable"`
	Reason   string `json:"reason,omitempty"`
}

// Path returns the global OpenCode config file ocman should manage.
//
// Resolution mirrors OpenCode's own: OPENCODE_CONFIG wins outright,
// otherwise it's $XDG_CONFIG_HOME/opencode/opencode.json (with the
// ~/.config fallback). An existing opencode.jsonc is preferred over a
// missing opencode.json so we report on the file OpenCode actually
// reads — Install then refuses it rather than stripping its comments.
func Path() (string, error) {
	if custom := os.Getenv("OPENCODE_CONFIG"); custom != "" {
		return custom, nil
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configHome = filepath.Join(home, ".config")
	}
	dir := filepath.Join(configHome, "opencode")
	plain := filepath.Join(dir, "opencode.json")
	if _, err := os.Stat(plain); err == nil {
		return plain, nil
	}
	jsonc := filepath.Join(dir, "opencode.jsonc")
	if _, err := os.Stat(jsonc); err == nil {
		return jsonc, nil
	}
	return plain, nil
}

// Check reports whether the ocman MCP entry is present and current.
// wantURL is the endpoint ocman wants registered (see
// Server.mcpServerURL). A missing config file is not an error: it just
// means "not configured, and Install will create it".
func Check(wantURL string) (Status, error) {
	path, err := Path()
	if err != nil {
		return Status{}, err
	}
	st := Status{Path: path, WantURL: wantURL, Editable: true}

	raw, err := os.ReadFile(path) //nolint:gosec // path is ocman's own config location
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		st.Editable, st.Reason = false, err.Error()
		return st, nil
	}
	if filepath.Ext(path) == ".jsonc" {
		st.Editable = false
		st.Reason = "config is JSONC; ocman won't rewrite it because comments would be lost"
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		// Comments in a .json file land here too. Either way we can
		// neither trust our reading of it nor safely rewrite it.
		st.Editable = false
		if st.Reason == "" {
			st.Reason = "config is not plain JSON (comments?); ocman won't rewrite it"
		}
		return st, nil
	}
	entry, ok := mcpEntry(doc)
	if !ok {
		return st, nil
	}
	st.CurrentURL = entry.URL
	st.Configured = entry.URL == wantURL && enabled(entry)
	return st, nil
}

// Install writes the ocman MCP entry into the global config and returns
// the path of the backup it took first (empty when there was no file to
// back up). Everything else in the file is preserved as-is.
func Install(wantURL string) (string, error) {
	st, err := Check(wantURL)
	if err != nil {
		return "", err
	}
	if !st.Editable {
		return "", fmt.Errorf("%w: %s", ErrNotEditable, st.Reason)
	}

	doc := map[string]json.RawMessage{}
	var mode os.FileMode = 0o644
	backup := ""

	raw, err := os.ReadFile(st.Path) //nolint:gosec // path is ocman's own config location
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &doc); err != nil {
			return "", fmt.Errorf("%w: %w", ErrNotEditable, err)
		}
		if info, err := os.Stat(st.Path); err == nil {
			mode = info.Mode().Perm()
		}
		// Back up the *original* bytes before touching anything, so a
		// bad merge is always recoverable by hand.
		backup = backupPath(st.Path, time.Now())
		if err := os.WriteFile(backup, raw, mode); err != nil {
			return "", fmt.Errorf("writing backup %s: %w", backup, err)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(st.Path), 0o755); err != nil {
			return "", err
		}
		if _, ok := doc["$schema"]; !ok {
			doc["$schema"] = json.RawMessage(`"https://opencode.ai/config.json"`)
		}
	default:
		return "", err
	}

	if err := setMCPEntry(doc, wantURL); err != nil {
		return "", err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(st.Path, append(out, '\n'), mode); err != nil {
		return "", err
	}
	return backup, nil
}

// backupPath returns "<dir>/opencode.<stamp>-backup.json" regardless of
// the original extension, so a .jsonc original still backs up to an
// obvious sibling.
func backupPath(path string, now time.Time) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	base = base[:len(base)-len(filepath.Ext(base))]
	return filepath.Join(dir, fmt.Sprintf("%s.%s-backup.json", base, now.Format(backupStamp)))
}

// mcpServer is the subset of an MCP entry ocman cares about.
type mcpServer struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

// enabled treats a missing "enabled" as true, matching OpenCode, which
// only skips a server when it's explicitly disabled.
func enabled(s mcpServer) bool { return s.Enabled == nil || *s.Enabled }

func mcpEntry(doc map[string]json.RawMessage) (mcpServer, bool) {
	rawMCP, ok := doc["mcp"]
	if !ok {
		return mcpServer{}, false
	}
	var servers map[string]mcpServer
	if err := json.Unmarshal(rawMCP, &servers); err != nil {
		return mcpServer{}, false
	}
	entry, ok := servers[ServerName]
	return entry, ok
}

// setMCPEntry replaces doc["mcp"]["ocman"], leaving any other server
// definitions (and their unknown fields) untouched.
func setMCPEntry(doc map[string]json.RawMessage, url string) error {
	servers := map[string]json.RawMessage{}
	if rawMCP, ok := doc["mcp"]; ok {
		if err := json.Unmarshal(rawMCP, &servers); err != nil {
			return fmt.Errorf("%w: \"mcp\" is not an object: %w", ErrNotEditable, err)
		}
	}
	entry, err := json.Marshal(map[string]interface{}{
		"type":    "remote",
		"url":     url,
		"enabled": true,
	})
	if err != nil {
		return err
	}
	servers[ServerName] = entry
	merged, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	doc["mcp"] = merged
	return nil
}

// writeFileAtomic writes via a temp file in the same directory and
// renames, so a crash mid-write can't leave a truncated config.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(name) // no-op once the rename succeeded
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
