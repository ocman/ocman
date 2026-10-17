package plugins

import (
	"bytes"
	"encoding/json"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// SessionCapability is session.v1, versioned independently of action.v1 and
// conversation.v1. It carries exactly two moves: a plugin-initiated run that
// starts a managed session in the plugin's one configured project, and a
// host-initiated completed turn. The plugin never names a directory: the
// approved project is the only one session.v1 can reach.
var SessionCapability = Capability{Name: "session", Version: Version{Major: 1}}

const (
	// SessionRunMethod routes the plugin-initiated run request. It is the
	// plugin-to-host call method.
	SessionRunMethod = "run"
	// SessionCompletedMethod is the host-to-plugin call method carrying one
	// completed assistant turn.
	SessionCompletedMethod = "completed"
	// SessionRunGrant authorizes starting and driving a session in the
	// plugin's one configured project. It is the only session grant in v1.
	SessionRunGrant = "session.run"
	// SessionProjectSetting names the required setting holding that one
	// project — the same key conversation.v1 uses, so one approved
	// configuration serves both capabilities.
	SessionProjectSetting = "project"
	// SessionMaxPromptBytes bounds the run prompt well below the envelope
	// limit, leaving room for the surrounding frame.
	SessionMaxPromptBytes = 64 << 10
	// SessionMaxTextBytes bounds one completed turn's text, the same bound
	// conversation.v1 puts on a reply.
	SessionMaxTextBytes = 64 << 10
)

// SessionRunParams are the closed parameters of a run: the prompt plus the
// three selections the host passes through to the session, and an optional
// subpath of the approved project. There is no directory parameter: the
// approved project is always the base, and a subpath can only reach inside it.
type SessionRunParams struct {
	Prompt            string `json:"prompt"`
	Agent             string `json:"agent,omitempty"`
	Model             string `json:"model,omitempty"`
	PermissionProfile string `json:"permissionProfile,omitempty"`
	Subpath           string `json:"subpath,omitempty"`
}

// SessionRunResult is returned as soon as the session exists and the prompt is
// queued; the turn itself completes later through the completed call.
type SessionRunResult struct {
	SessionID string `json:"sessionId"`
}

// SessionCompleted is one completed assistant turn, text only in v1, keyed by
// the session it came from.
type SessionCompleted struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`
}

// Validate requires a real prompt and host-passable selections. The prompt
// allows newlines and tabs, unlike validText, because prompts are multi-line;
// other control characters are rejected, as in conversation.v1.
func (p SessionRunParams) Validate() error {
	if p.Prompt == "" || len(p.Prompt) > SessionMaxPromptBytes || !utf8.ValidString(p.Prompt) || strings.TrimSpace(p.Prompt) == "" ||
		strings.ContainsFunc(p.Prompt, func(r rune) bool {
			return r != '\n' && r != '\r' && r != '\t' && (r < 0x20 || r == 0x7f)
		}) {
		return ErrInvalidMessage
	}
	if (p.Agent != "" && !validText(p.Agent)) || (p.Model != "" && !validText(p.Model)) ||
		(p.PermissionProfile != "" && !ValidPermissionProfileName(p.PermissionProfile)) {
		return ErrInvalidMessage
	}
	return nil
}

// ValidPermissionProfileName is shared by profile mutations and run consumers.
func ValidPermissionProfileName(name string) bool { return validText(name) }

func (c SessionCompleted) Validate() error {
	if !validText(c.SessionID) || !conversationText(c.Text) {
		return ErrInvalidMessage
	}
	return nil
}

// DecodeSessionRunParams rejects unknown fields so a later minor cannot smuggle
// unvalidated content past this contract, exactly as the message decoder does.
func DecodeSessionRunParams(data json.RawMessage) (SessionRunParams, error) {
	if len(data) > MaxMessageBytes || checkJSON(data) != nil {
		return SessionRunParams{}, ErrInvalidMessage
	}
	var p SessionRunParams
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF {
		return SessionRunParams{}, ErrInvalidMessage
	}
	return p, p.Validate()
}

func hasSession(caps []Capability) bool {
	return slices.ContainsFunc(caps, func(c Capability) bool {
		return c.Name == SessionCapability.Name && c.Version.Major == SessionCapability.Version.Major
	})
}

// validateSession enforces the capability's declaration contract, mirroring
// validateConversation: a session plugin must run at owner scope, request the
// run grant, and declare the single required, non-secret project setting the
// user approves in Settings.
func (d Description) validateSession() error {
	if !hasSession(d.Capabilities) {
		return nil
	}
	// Sessions drive the machine the plugin is installed on with that
	// machine's approval state; a hub or global scope would let a second
	// machine start turns through a project it never approved.
	if d.Scope != ScopeOwner {
		return ErrInvalidMessage
	}
	if !slices.Contains(d.RequestedGrants, SessionRunGrant) {
		return ErrInvalidMessage
	}
	for _, s := range d.Settings {
		if s.Key != SessionProjectSetting {
			continue
		}
		if s.Type != "string" || !s.Required || s.Secret || s.List {
			return ErrInvalidMessage
		}
		return nil
	}
	return ErrInvalidMessage
}

// SessionDirectoryFor resolves the session's directory: always the approved
// project, optionally joined with a relative subpath inside it. Traversal and
// absolute subpaths fail closed; the caller-supplied string can never name a
// directory outside the approved project.
func SessionDirectoryFor(project, subpath string) (string, bool) {
	dir := filepath.Clean(project)
	if subpath == "" {
		return dir, true
	}
	if filepath.IsAbs(subpath) || strings.HasPrefix(subpath, "/") || strings.HasPrefix(subpath, `\`) {
		return "", false
	}
	cleaned := path.Clean(filepath.ToSlash(subpath))
	if slices.Contains(strings.Split(strings.ReplaceAll(subpath, `\`, "/"), "/"), "..") || strings.Contains(subpath, `\`) || strings.Contains(subpath, ":") {
		return "", false
	}
	joined, ok := withinDirectory(dir, filepath.FromSlash(cleaned))
	if !ok {
		return "", false
	}
	return joined, true
}

// withinDirectory is the belt-and-braces containment proof: joining a cleaned
// relative path cannot escape, and this re-checks the result instead of
// trusting that reasoning.
func withinDirectory(dir, rel string) (string, bool) {
	if rel == "." {
		return dir, true
	}
	joined := filepath.Join(dir, rel)
	return joined, joined == dir || strings.HasPrefix(joined, dir+string(filepath.Separator))
}
