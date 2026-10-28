package protocol

import (
	"context"
	"slices"
	"time"
)

const (
	// ConversationStartTimeout bounds inbound delivery, which may have to
	// launch the project's opencode instance before the first prompt.
	ConversationStartTimeout = 2 * time.Minute
	// ConversationReplyTimeout bounds one outbound provider post, which must
	// not hold a plugin concurrency slot for long.
	ConversationReplyTimeout = 30 * time.Second
)

// ConversationAuthorization must read current enablement, grants and the one
// configured project in the same critical section as revocation, exactly like
// ActionAuthorization. The third callback argument is that configured project
// directory. Work already admitted cannot be recalled on revocation.
type ConversationAuthorization func(ctx context.Context, pluginID string, use func(Description, []string, Config) error) error

// ConversationStart hands a normalized message to the host's session
// orchestration. dir is the authorized project; a plugin never chooses it. The
// whole message crosses this seam because the host owns both the durable
// account/thread mapping and event deduplication, and both need it.
type ConversationStart func(ctx context.Context, pluginID string, config Config, message ConversationMessage) error

// ConversationBroker is the only way a conversation plugin reaches a session,
// and the only way a completed reply reaches a provider. It holds no state: a
// message either authorizes into the configured project or is denied.
type ConversationBroker struct {
	authorize ConversationAuthorization
	start     ConversationStart
	call      ActionCall
}

func NewConversationBroker(authorize ConversationAuthorization, start ConversationStart, call ActionCall) *ConversationBroker {
	return &ConversationBroker{authorize: authorize, start: start, call: call}
}

// ConversationAllowed fails closed on an undeclared capability or a missing
// grant, so an enabled plugin without the approved grant reaches nothing. It is
// exported so the host can ask the same question before it schedules work,
// rather than discovering the denial by attempting it.
func ConversationAllowed(d Description, grants []string) error {
	if !hasConversation(d.Capabilities) {
		return &WireError{Category: ErrorNotFound}
	}
	if !slices.Contains(grants, ConversationSessionGrant) {
		return &WireError{Category: ErrorPermissionDenied}
	}
	return nil
}

// Deliver authorizes a plugin-initiated normalized message and starts the turn.
// The host's start hook runs outside the authorization critical section: it
// creates sessions and launches processes, which must not block plugin
// management. Admission is the point-in-time check, as with actions.
func (b *ConversationBroker) Deliver(ctx context.Context, pluginID string, event Event) error {
	if event.Capability != ConversationCapability.Name || event.Name != ConversationMessageEvent {
		return &WireError{Category: ErrorNotFound}
	}
	message, err := DecodeConversationMessage(event.Data)
	if err != nil {
		return &WireError{Category: ErrorInvalidArgument}
	}
	ctx, cancel := context.WithTimeout(ctx, ConversationStartTimeout)
	defer cancel()
	var config Config
	if err := b.authorize(ctx, pluginID, func(d Description, grants []string, admitted Config) error {
		if err := ConversationAllowed(d, grants); err != nil {
			return err
		}
		if admitted.Project == "" || !ConversationProjectAllowed(admitted.Project, message.Project) {
			return &WireError{Category: ErrorPermissionDenied}
		}
		config = admitted
		return nil
	}); err != nil {
		return err
	}
	return b.start(ctx, pluginID, config, message)
}

// Reply posts one completed assistant turn back to the originating thread.
// operationID is the host's idempotency key; the call adapter reserves it so a
// repeated idle edge cannot post the same reply twice.
func (b *ConversationBroker) Reply(ctx context.Context, pluginID, operationID string, reply ConversationReply) error {
	if reply.Validate() != nil || !validText(operationID) {
		return &WireError{Category: ErrorInvalidArgument}
	}
	ctx, cancel := context.WithTimeout(ctx, ConversationReplyTimeout)
	defer cancel()
	_, err := unaryCall(ctx, b.authorize, b.call, pluginID, ConversationAllowed, reply, func(Description) Call {
		return Call{
			OperationID: operationID, Capability: ConversationCapability.Name, Version: ConversationCapability.Version,
			Method: ConversationReplyMethod,
		}
	})
	return err
}
