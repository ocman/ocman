package plugins

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// ConversationCapability is conversation.v1, versioned independently of
// action.v1. It carries exactly two provider-neutral moves: a plugin-initiated
// normalized inbound message, and a host-initiated completed reply. Provider
// details (Slack channels, tokens, envelopes) stay inside the plugin; core owns
// session creation and orchestration.
var ConversationCapability = Capability{Name: "conversation", Version: Version{Major: 1}}

const (
	// ConversationMessageEvent is the plugin to host event name.
	ConversationMessageEvent = "message"
	// ConversationReplyMethod is the host to plugin call method.
	ConversationReplyMethod = "reply"
	// ConversationSessionGrant authorizes starting and driving a session in
	// the plugin's one configured project. It is the only conversation grant
	// in v1; there is no way to widen it to another project.
	ConversationSessionGrant = "conversation.session"
	// ConversationProjectSetting names the required setting holding that one
	// project. v1 binds one project per plugin, approved through Settings.
	ConversationProjectSetting = "project"
	// ConversationMaxTextBytes bounds both directions well below the envelope
	// limit, leaving room for the surrounding frame.
	ConversationMaxTextBytes = 64 << 10
	// ConversationMaxProjectBytes bounds a plugin's project claim. The claim
	// is only ever compared, never used as a path.
	ConversationMaxProjectBytes = 1024
)

// ConversationMessage is the normalized inbound message. AccountID (the
// provider workspace/account) and ThreadID (the conversation) are opaque
// identities the host only compares and echoes back; together with the plugin
// they key the durable session mapping, so two workspaces with colliding thread
// identities stay isolated. EventID is the provider's stable identifier for this
// delivery, not for this connection attempt: a redelivery of the same event must
// repeat it, which is what makes deduplication survive a restart. Project is an
// optional claim, denied unless it matches the configured project; the directory
// actually used always comes from the configuration.
type ConversationMessage struct {
	AccountID string `json:"accountId"`
	ThreadID  string `json:"threadId"`
	EventID   string `json:"eventId"`
	Text      string `json:"text"`
	Project   string `json:"project,omitempty"`
}

// ConversationReply is one completed assistant turn, text only in v1. It echoes
// the account so a plugin serving several workspaces posts into the right one.
type ConversationReply struct {
	AccountID string `json:"accountId"`
	ThreadID  string `json:"threadId"`
	Text      string `json:"text"`
}

// conversationID matches every opaque provider identity in this capability.
// Compared and echoed only: never parsed, never used as a path.
var conversationID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,127}$`)

// conversationText allows newlines and tabs, unlike validText, because prompts
// and assistant replies are multi-line. Other control characters are rejected.
func conversationText(s string) bool {
	if s == "" || len(s) > ConversationMaxTextBytes || !utf8.ValidString(s) || strings.TrimSpace(s) == "" {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return r != '\n' && r != '\r' && r != '\t' && (r < 0x20 || r == 0x7f)
	})
}

// Validate requires the account and event identities as well as the thread: a
// message without a stable event id cannot be deduplicated, and one without an
// account cannot be attributed to a workspace. Both fail closed rather than
// silently degrading to a weaker guarantee.
func (m ConversationMessage) Validate() error {
	if !conversationID.MatchString(m.AccountID) || !conversationID.MatchString(m.ThreadID) ||
		!conversationID.MatchString(m.EventID) || !conversationText(m.Text) ||
		len(m.Project) > ConversationMaxProjectBytes {
		return ErrInvalidMessage
	}
	return nil
}

func (r ConversationReply) Validate() error {
	if !conversationID.MatchString(r.AccountID) || !conversationID.MatchString(r.ThreadID) || !conversationText(r.Text) {
		return ErrInvalidMessage
	}
	return nil
}

// DecodeConversationMessage rejects unknown fields so a later minor cannot
// smuggle unvalidated provider content past this contract.
func DecodeConversationMessage(data json.RawMessage) (ConversationMessage, error) {
	if len(data) > MaxMessageBytes || checkJSON(data) != nil {
		return ConversationMessage{}, ErrInvalidMessage
	}
	var m ConversationMessage
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF {
		return ConversationMessage{}, ErrInvalidMessage
	}
	return m, m.Validate()
}

func hasConversation(caps []Capability) bool {
	return slices.ContainsFunc(caps, func(c Capability) bool {
		return c.Name == ConversationCapability.Name && c.Version.Major == ConversationCapability.Version.Major
	})
}

// validateConversation enforces the capability's declaration contract: a
// conversation plugin must request the session grant and declare the single
// required, non-secret project setting the user approves in Settings.
func (d Description) validateConversation() error {
	if !hasConversation(d.Capabilities) {
		return nil
	}
	if !slices.Contains(d.RequestedGrants, ConversationSessionGrant) {
		return ErrInvalidMessage
	}
	for _, s := range d.Settings {
		if s.Key != ConversationProjectSetting {
			continue
		}
		if s.Type != "string" || !s.Required || s.Secret {
			return ErrInvalidMessage
		}
		return nil
	}
	return ErrInvalidMessage
}

// ConversationProjectAllowed compares a plugin's optional project claim with
// the one configured project. An empty claim defers to the configuration.
func ConversationProjectAllowed(configured, claimed string) bool {
	return claimed == "" || filepath.Clean(claimed) == filepath.Clean(configured)
}
