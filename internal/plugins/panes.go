package plugins

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var PaneCapability = Capability{Name: "pane", Version: Version{Major: 1}}

const PaneProjectGrant = "pane.project"

// PaneDescriptor declares a read-only, host-rendered project tree. No plugin
// markup, scripts or stylesheet is loaded into the browser.
type PaneDescriptor struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type PaneRequest struct {
	PluginID  string `json:"pluginId"`
	PaneID    string `json:"paneId"`
	OwnerID   string `json:"ownerId"`
	Directory string `json:"directory"`
}

type PaneRead struct {
	PaneID    string `json:"paneId"`
	Directory string `json:"directory"`
}

type TreeNode struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ParentID string `json:"parentId,omitempty"`
	Status   string `json:"status,omitempty"`
	Badge    string `json:"badge,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

type PaneTree struct {
	Available bool       `json:"available"`
	Nodes     []TreeNode `json:"nodes,omitempty"`
	Warning   bool       `json:"warning,omitempty"`
}

func (d Description) validatePanes() error {
	hasPane := slices.ContainsFunc(d.Capabilities, func(c Capability) bool { return c.Name == "pane" })
	if !hasPane && len(d.Panes) > 0 || len(d.Panes) > 16 {
		return ErrInvalidMessage
	}
	if hasPane && (d.Scope != ScopeOwner || !slices.Contains(d.RequestedGrants, PaneProjectGrant) || len(d.Panes) == 0) {
		return ErrInvalidMessage
	}
	seen := map[string]bool{}
	for _, p := range d.Panes {
		if !validName(p.ID) || !validText(p.Label) || seen[p.ID] {
			return ErrInvalidMessage
		}
		seen[p.ID] = true
	}
	return nil
}

func (r PaneRequest) Validate() error {
	if !pluginID.MatchString(r.PluginID) || len(r.PluginID) > 253 || !validName(r.PaneID) || !validText(r.OwnerID) ||
		!(PaneRead{PaneID: r.PaneID, Directory: r.Directory}).Valid() {
		return ErrInvalidMessage
	}
	return nil
}

func (r PaneRead) Valid() bool {
	return validName(r.PaneID) && filepath.IsAbs(r.Directory) && len(r.Directory) <= 4096 &&
		utf8.ValidString(r.Directory) && !strings.ContainsFunc(r.Directory, unicode.IsControl)
}

func (d Description) PaneAllowed(id string, grants []string) bool {
	return d.Scope == ScopeOwner && slices.ContainsFunc(d.Capabilities, func(c Capability) bool {
		return c.Name == PaneCapability.Name && c.Version.Major == PaneCapability.Version.Major
	}) &&
		slices.Contains(grants, PaneProjectGrant) &&
		slices.ContainsFunc(d.Panes, func(p PaneDescriptor) bool { return p.ID == id })
}

func DecodePaneRead(data []byte) (PaneRead, error) {
	var r PaneRead
	if decodePaneJSON(data, &r) != nil || !r.Valid() {
		return r, ErrInvalidMessage
	}
	return r, nil
}

func DecodePaneTree(data []byte) (PaneTree, error) {
	var tree PaneTree
	if decodePaneJSON(data, &tree) != nil || len(tree.Nodes) > 2000 || !tree.Available && (len(tree.Nodes) > 0 || tree.Warning) {
		return PaneTree{}, ErrInvalidMessage
	}
	parents := make(map[string]string, len(tree.Nodes))
	for _, n := range tree.Nodes {
		if !paneText(n.ID, 128, false) || !paneText(n.Title, 4096, false) || !paneText(n.ParentID, 128, true) ||
			!paneText(n.Badge, 128, true) || !paneText(n.Kind, 128, true) ||
			!slices.Contains([]string{"", "open", "in_progress", "blocked", "deferred", "closed"}, n.Status) {
			return PaneTree{}, ErrInvalidMessage
		}
		if _, exists := parents[n.ID]; exists {
			return PaneTree{}, ErrInvalidMessage
		}
		parents[n.ID] = n.ParentID
	}
	for id := range parents {
		seen := map[string]bool{}
		for current, depth := id, 0; current != ""; current, depth = parents[current], depth+1 {
			if seen[current] || depth > 64 {
				return PaneTree{}, ErrInvalidMessage
			}
			seen[current] = true
		}
	}
	return tree, nil
}

func paneText(value string, limit int, optional bool) bool {
	return (optional || value != "") && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func decodePaneJSON(data []byte, out any) error {
	if len(data) > MaxMessageBytes || checkJSON(data) != nil || checkFields(data, reflect.TypeOf(out).Elem()) != nil {
		return ErrInvalidMessage
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInvalidMessage
	}
	return nil
}
