package plugin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ocman/ocman/sdk/protocol"
)

// SessionToolHandler serves only validated tool callbacks, not completions.
func SessionToolHandler(run func(context.Context, string, SessionToolCall) (json.RawMessage, error)) Handler {
	return func(ctx context.Context, call Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if call.Capability != "session" || call.Version != ToolSessionCapability.Version || call.Method != SessionToolMethod {
			return nil, Failure(ErrorNotFound)
		}
		var params SessionToolCall
		if run == nil || json.Unmarshal(call.Params, &params) != nil || params.Validate() != nil {
			return nil, Failure(ErrorInvalidArgument)
		}
		raw, err := run(ctx, call.OperationID, params)
		if err == nil && (len(raw) > protocol.SessionToolMaxBytes || !json.Valid(raw)) {
			return nil, Failure(ErrorInvalidArgument)
		}
		return raw, err
	}
}

func NewSessionStop(operationID, toolContext string) (Call, error) {
	params := protocol.SessionStopParams{Context: toolContext}
	if params.Validate() != nil {
		return Call{}, protocol.ErrInvalidMessage
	}
	raw, _ := json.Marshal(params)
	call := Call{ID: 1, OperationID: operationID, Capability: "session", Version: ToolSessionCapability.Version, Method: protocol.SessionStopMethod, DeadlineUnixMS: time.Now().Add(30 * time.Second).UnixMilli(), Params: raw}
	if err := (Envelope{Type: TypeCall, Call: &call}).Validate(); err != nil {
		return Call{}, err
	}
	return call, nil
}
