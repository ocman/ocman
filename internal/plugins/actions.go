package plugins

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// ActionCapability is action.v1; capability names and versions remain separate
// in the protocol kernel.
var ActionCapability = Capability{Name: "action", Version: Version{Major: 1}}

type ActionDescriptor struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	Placement      string   `json:"placement"` // global, project, session
	RequiredGrants []string `json:"requiredGrants,omitempty"`
	Surfaces       []string `json:"surfaces"`               // v1: command-palette
	Confirmation   string   `json:"confirmation,omitempty"` // Host-rendered plain text; empty means none.
}

// Context contains opaque identities, never application objects, paths, URLs,
// transcript text, or arbitrary selected content. Route is a core route name.
type ActionContext struct {
	OwnerID   string            `json:"ownerId,omitempty"`
	ProjectID string            `json:"projectId,omitempty"`
	SessionID string            `json:"sessionId,omitempty"`
	Route     string            `json:"route,omitempty"`
	Selection []ActionSelection `json:"selection,omitempty"`
}

type ActionSelection struct {
	Kind string `json:"kind"` // project or session
	ID   string `json:"id"`
}

// ActionRequest is host-side only. Confirmation tokens never reach plugins.
type ActionRequest struct {
	OwnerID           string        `json:"ownerId,omitempty"` // Plugin installation owner; context owner may differ for hub scope.
	PluginID          string        `json:"pluginId"`
	ActionID          string        `json:"actionId"`
	OperationID       string        `json:"operationId"`
	Placement         string        `json:"placement"`
	Surface           string        `json:"surface"`
	Context           ActionContext `json:"context"`
	ConfirmationToken string        `json:"confirmationToken,omitempty"`
}

type ActionInvocation struct {
	ActionID string        `json:"actionId"`
	Context  ActionContext `json:"context"`
}

type ActionConfirmation struct {
	Text      string `json:"text"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expiresAt"`
}

type ActionResponse struct {
	Results      []ActionResult      `json:"results,omitempty"`
	Confirmation *ActionConfirmation `json:"confirmation,omitempty"`
	Error        *WireError          `json:"error,omitempty"`
}

// ActionResult is a closed tagged union. Data is accepted only from the plugin
// for artifacts; the broker replaces it with a random downloadable Handle.
// All labels and notice text must be rendered as text, never HTML or markdown.
type ActionResult struct {
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Label  string `json:"label,omitempty"`
	URL    string `json:"url,omitempty"`
	Target string `json:"target,omitempty"`
	Data   []byte `json:"data,omitempty"`
	Handle string `json:"handle,omitempty"`
}

var actionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func plainText(s string) bool { return validText(s) && !strings.ContainsAny(s, "<>") }
func placement(s string) bool { return slices.Contains([]string{"global", "project", "session"}, s) }
func coreRoute(s string) bool {
	return slices.Contains([]string{"sessions", "projects", "project", "session", "settings", "inbox", "routines", "factory", "analytics"}, s)
}

func (d Description) validateActions() error {
	if len(d.Actions) > 128 {
		return ErrInvalidMessage
	}
	if len(d.Actions) > 0 && !slices.ContainsFunc(d.Capabilities, func(c Capability) bool { return c.Name == "action" && c.Version.Major == 1 }) {
		return ErrInvalidMessage
	}
	seen := map[string]bool{}
	for _, a := range d.Actions {
		if !validName(a.ID) || seen[a.ID] || !plainText(a.Label) || !placement(a.Placement) ||
			(a.Confirmation != "" && !plainText(a.Confirmation)) || len(a.Surfaces) != 1 || a.Surfaces[0] != "command-palette" {
			return ErrInvalidMessage
		}
		seen[a.ID] = true
		grants := map[string]bool{}
		for _, g := range a.RequiredGrants {
			if !slices.Contains([]string{"context.owner", "context.project", "context.session", "context.route", "context.selection"}, g) ||
				!slices.Contains(d.RequestedGrants, g) || grants[g] {
				return ErrInvalidMessage
			}
			grants[g] = true
		}
	}
	return nil
}

func (c ActionContext) Validate() error {
	for _, id := range []string{c.OwnerID, c.ProjectID, c.SessionID} {
		if id != "" && !actionID.MatchString(id) {
			return ErrInvalidMessage
		}
	}
	if (c.Route != "" && !coreRoute(c.Route)) || len(c.Selection) > 100 {
		return ErrInvalidMessage
	}
	for _, s := range c.Selection {
		if (s.Kind != "project" && s.Kind != "session") || !actionID.MatchString(s.ID) {
			return ErrInvalidMessage
		}
	}
	return nil
}

func (r ActionRequest) Validate() error {
	if r.OwnerID != "" && !actionID.MatchString(r.OwnerID) {
		return ErrInvalidMessage
	}
	if !pluginID.MatchString(r.PluginID) || len(r.PluginID) > 253 || !validName(r.ActionID) || !actionID.MatchString(r.OperationID) ||
		!placement(r.Placement) || r.Surface != "command-palette" || len(r.ConfirmationToken) > 128 ||
		(r.Placement == "project" && r.Context.ProjectID == "") || (r.Placement == "session" && r.Context.SessionID == "") {
		return ErrInvalidMessage
	}
	return r.Context.Validate()
}

func (a ActionDescriptor) Granted(grants []string) bool {
	for _, g := range a.RequiredGrants {
		if !slices.Contains(grants, g) {
			return false
		}
	}
	return true
}

// Minimize copies only context fields declared by this action. It does not
// check user approval; callers must separately enforce current grants.
func (a ActionDescriptor) Minimize(c ActionContext) ActionContext {
	var out ActionContext
	for _, g := range a.RequiredGrants {
		switch g {
		case "context.owner":
			out.OwnerID = c.OwnerID
		case "context.project":
			out.ProjectID = c.ProjectID
		case "context.session":
			out.SessionID = c.SessionID
		case "context.route":
			out.Route = c.Route
		case "context.selection":
			out.Selection = append([]ActionSelection(nil), c.Selection...)
		}
	}
	return out
}

// DecodeActionResults rejects unknown fields and mixed union variants so neither
// raw plugin JSON nor new executable UI instructions can slip into the browser.
func DecodeActionResults(data json.RawMessage) ([]ActionResult, error) {
	if len(data) > MaxMessageBytes || checkJSON(data) != nil {
		return nil, ErrInvalidMessage
	}
	var body struct {
		Results []ActionResult `json:"results"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF || len(body.Results) == 0 || len(body.Results) > 16 {
		return nil, ErrInvalidMessage
	}
	for _, r := range body.Results {
		remaining := r
		remaining.Kind = ""
		switch r.Kind {
		case "notice":
			if !plainText(r.Text) {
				return nil, ErrInvalidMessage
			}
			remaining.Text = ""
		case "link":
			u, err := url.Parse(r.URL)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || len(r.URL) > 2048 || !plainText(r.Label) {
				return nil, ErrInvalidMessage
			}
			remaining.Label, remaining.URL = "", ""
		case "artifact":
			if !plainText(r.Label) || strings.ContainsAny(r.Label, `/\:`) || r.Label == "." || r.Label == ".." || len(r.Data) == 0 {
				return nil, ErrInvalidMessage
			}
			remaining.Label, remaining.Data = "", nil
		case "navigation":
			// Parameterized routes require core resolution; v1 permits static destinations only.
			if !slices.Contains([]string{"sessions", "projects", "settings", "inbox", "routines"}, r.Target) {
				return nil, ErrInvalidMessage
			}
			remaining.Target = ""
		case "refresh":
			if !slices.Contains([]string{"actions", "projects", "sessions"}, r.Target) {
				return nil, ErrInvalidMessage
			}
			remaining.Target = ""
		default:
			return nil, ErrInvalidMessage
		}
		if remaining.Text != "" || remaining.Label != "" || remaining.URL != "" || remaining.Target != "" || len(remaining.Data) != 0 || remaining.Handle != "" {
			return nil, ErrInvalidMessage
		}
	}
	return body.Results, nil
}
