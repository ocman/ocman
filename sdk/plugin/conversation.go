package plugin

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/sdk/protocol"
)

// Aliases and constants for conversation.v1, versioned independently of action.v1.
type (
	ConversationMessage = protocol.ConversationMessage
	ConversationReply   = protocol.ConversationReply
)

const (
	ConversationSessionGrant   = protocol.ConversationSessionGrant
	ConversationProjectSetting = protocol.ConversationProjectSetting
	ConversationMessageEvent   = protocol.ConversationMessageEvent
	ConversationReplyMethod    = protocol.ConversationReplyMethod
)

// ConversationCapability is conversation.v1.
var ConversationCapability = protocol.ConversationCapability

// NewConversationMessage builds a validated plugin to host event carrying one
// normalized inbound message. Validating here keeps an unvalidated provider
// payload from reaching the wire and failing the whole stream.
func NewConversationMessage(message ConversationMessage) (Event, error) {
	if err := message.Validate(); err != nil {
		return Event{}, err
	}
	data, err := json.Marshal(message)
	if err != nil {
		return Event{}, err
	}
	return Event{Capability: ConversationCapability.Name, Name: ConversationMessageEvent, Data: data}, nil
}

// ConversationHandler serves unary conversation.v1 reply calls. The host owns
// enablement, grants and the approved project; this adapter only checks the
// frame and hands over a validated reply.
//
// The operation id is passed through because the host's delivery is
// at-least-once: it is stable across retries of the same reply, so a provider
// adapter can recognize a repeat and avoid posting a second visible message.
func ConversationHandler(reply func(ctx context.Context, operationID string, r ConversationReply) error) Handler {
	return func(ctx context.Context, call Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if reply == nil {
			return nil, Failure(ErrorInternal)
		}
		if call.Capability != ConversationCapability.Name || call.Version.Major != ConversationCapability.Version.Major || call.Method != ConversationReplyMethod {
			return nil, Failure(ErrorNotFound)
		}
		var r ConversationReply
		if json.Unmarshal(call.Params, &r) != nil || r.Validate() != nil {
			return nil, Failure(ErrorInvalidArgument)
		}
		if err := reply(ctx, call.OperationID, r); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	}
}
