package protocol

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// StreamingSessionCapability opts into best-effort text previews for owned sessions.
var StreamingSessionCapability = Capability{Name: "session", Version: Version{Major: 1, Minor: 8}}

const SessionPartialMethod = "partial"
const SessionMaxPartialBytes = 32 << 10

// SessionPartial replaces the previous preview for the same session and turn.
// Completed remains authoritative; a preview need not be a prefix of it.
type SessionPartial struct {
	SessionID string `json:"sessionId"`
	TurnID    string `json:"turnId"`
	Text      string `json:"text"`
}

func (p SessionPartial) Validate() error {
	if !validText(p.SessionID) || !validText(p.TurnID) || len(p.Text) > SessionMaxPartialBytes || !validSessionReplyText(p.Text) {
		return ErrInvalidMessage
	}
	return nil
}

func validSessionReplyText(text string) bool {
	return strings.TrimSpace(text) != "" && utf8.ValidString(text) && !strings.ContainsFunc(text, func(r rune) bool {
		return r != '\n' && r != '\r' && r != '\t' && (r < 0x20 || r == 0x7f)
	})
}

func sessionVersion(d Description) Version {
	for _, c := range d.Capabilities {
		if c.Name == SessionCapability.Name && c.Version.Major == 1 {
			return Version{Major: 1, Minor: min(c.Version.Minor, StreamingSessionCapability.Version.Minor)}
		}
	}
	return SessionCapability.Version
}

// SessionPartialAllowed applies all existing session grants, plus the opt-in.
func SessionPartialAllowed(d Description, grants []string) error {
	if err := SessionAllowed(d, grants); err != nil {
		return err
	}
	if sessionVersion(d).Minor < StreamingSessionCapability.Version.Minor {
		return &WireError{Category: ErrorNotFound}
	}
	return nil
}

// Partial attempts a preview once. The host must prove session ownership
// before calling this broker, just as it does for Completed.
func (b *SessionBroker) Partial(ctx context.Context, pluginID, operationID string, partial SessionPartial) error {
	if partial.Validate() != nil || !validText(operationID) {
		return &WireError{Category: ErrorInvalidArgument}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	value, err := unaryCall(ctx, b.authorize, b.call, pluginID, SessionPartialAllowed, partial, func(d Description) Call {
		return Call{OperationID: operationID, Capability: SessionCapability.Name, Version: sessionVersion(d), Method: SessionPartialMethod}
	})
	return sessionAck(value, err)
}

func sessionAck(value json.RawMessage, err error) error {
	if err != nil {
		return err
	}
	var ack map[string]json.RawMessage
	if json.Unmarshal(value, &ack) != nil || ack == nil || len(ack) != 0 {
		return ErrInvalidMessage
	}
	return nil
}
