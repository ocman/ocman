package opencodeconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv2"
)

// OpenCode v2 keeps MCP servers under mcp.servers.<name>. It still reads
// v1's flat mcp.<name> entries, but converts them field by field and drops
// codemode, so only an entry in the v2 layout can turn Code Mode off.
// The two layouts may share one "mcp" object.

// v2Server is the subset of a v2 MCP entry ocman manages.
type v2Server struct {
	Type     string `json:"type"`
	URL      string `json:"url"`
	Disabled *bool  `json:"disabled,omitempty"`
	Codemode *bool  `json:"codemode,omitempty"`
}

// current reports whether the entry is enabled with Code Mode off.
func (s v2Server) current() bool {
	return s.Type == "remote" && (s.Disabled == nil || !*s.Disabled) &&
		s.Codemode != nil && !*s.Codemode
}

// object decodes raw as a JSON object, treating absent or null as empty.
func object(raw json.RawMessage, name string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if raw == nil {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%w: %s is not an object: %w", ErrNotEditable, name, err)
	}
	if out == nil {
		out = map[string]json.RawMessage{}
	}
	return out, nil
}

func v2Entry(doc map[string]json.RawMessage) (v2Server, bool) {
	mcp, err := object(doc["mcp"], `"mcp"`)
	if err != nil {
		return v2Server{}, false
	}
	servers, err := object(mcp["servers"], `"mcp.servers"`)
	if err != nil {
		return v2Server{}, false
	}
	raw, ok := servers[ServerName]
	if !ok {
		return v2Server{}, false
	}
	var entry v2Server
	if err := json.Unmarshal(raw, &entry); err != nil {
		return v2Server{}, false
	}
	return entry, true
}

// checkV2 fills st from the entry OpenCode v2 actually loads. A leftover
// flat entry is not configured: it would shadow or duplicate ours.
func checkV2(doc map[string]json.RawMessage, st *Status) {
	legacy, hasLegacy := mcpEntry(doc)
	entry, ok := v2Entry(doc)
	switch {
	case ok:
		st.CurrentURL = entry.URL
	case hasLegacy:
		st.CurrentURL = legacy.URL
	}
	st.Configured = ok && !hasLegacy && entry.URL == st.WantURL && entry.current()
}

// setV2Entry writes ocman's entry to mcp.servers with Code Mode off and
// removes any flat v1 entry, leaving every other server untouched.
func setV2Entry(doc map[string]json.RawMessage, url string, disabled bool) error {
	mcp, err := object(doc["mcp"], `"mcp"`)
	if err != nil {
		return err
	}
	servers, err := object(mcp["servers"], `"mcp.servers"`)
	if err != nil {
		return err
	}
	raw := servers[ServerName]
	if raw == nil {
		raw = mcp[ServerName]
	}
	fields, err := object(raw, `"mcp ocman entry"`)
	if err != nil {
		return err
	}
	// Keep connection options (including unknown future options) during
	// startup repair. Only the layout and the fields we own change.
	delete(fields, "enabled")
	fields["type"] = json.RawMessage(`"remote"`)
	fields["url"], err = json.Marshal(url)
	if err != nil {
		return err
	}
	fields["disabled"] = json.RawMessage("false")
	if disabled {
		fields["disabled"] = json.RawMessage("true")
	}
	fields["codemode"] = json.RawMessage("false")
	entry, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	servers[ServerName] = entry
	delete(mcp, ServerName)
	if mcp["servers"], err = json.Marshal(servers); err != nil {
		return err
	}
	doc["mcp"], err = json.Marshal(mcp)
	return err
}

// EnsureV2Layout keeps an existing ocman entry usable under OpenCode v2:
// it moves the entry to the v2 layout with Code Mode off, keeping its URL
// and enabled state. It never adds an entry, since installing stays the
// user's choice, and does nothing under v1 or when the entry is current.
// It returns the backup path when it rewrote the file.
func EnsureV2Layout() (string, error) {
	if !ocv2.InstalledV2() {
		return "", nil
	}
	path, err := Path()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // path is ocman's own config location
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if filepath.Ext(path) == ".jsonc" {
		return "", fmt.Errorf("%w: %s is JSONC", ErrNotEditable, path)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotEditable, err)
	}
	legacy, hasLegacy := mcpEntry(doc)
	entry, ok := v2Entry(doc)
	switch {
	case ok && !hasLegacy && entry.Codemode != nil && !*entry.Codemode:
		return "", nil
	case ok:
	case hasLegacy:
		disabled := !enabled(legacy)
		entry = v2Server{URL: legacy.URL, Disabled: &disabled}
	default:
		return "", nil
	}
	if err := setV2Entry(doc, entry.URL, entry.Disabled != nil && *entry.Disabled); err != nil {
		return "", err
	}
	return writeDoc(path, raw, doc)
}
