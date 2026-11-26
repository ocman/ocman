package plugin

import (
	"context"
	"encoding/json"
)

// StreamingSessionHandler handles session 1.8 previews and ordinary durable
// completions. Acknowledge previews quickly; they are never retried by the host.
// It also handles completed-only delivery on older hosts.
func StreamingSessionHandler(completed func(context.Context, string, SessionCompleted) error, partial func(context.Context, SessionPartial) error) Handler {
	finish := SessionHandler(completed)
	return func(ctx context.Context, call Call, emit func(json.RawMessage) error) (json.RawMessage, error) {
		if call.Method != SessionPartialMethod {
			return finish(ctx, call, emit)
		}
		if call.Capability != StreamingSessionCapability.Name || call.Version.Major != 1 || call.Version.Minor < StreamingSessionCapability.Version.Minor || partial == nil {
			return nil, Failure(ErrorNotFound)
		}
		var p SessionPartial
		if json.Unmarshal(call.Params, &p) != nil || p.Validate() != nil {
			return nil, Failure(ErrorInvalidArgument)
		}
		if err := partial(ctx, p); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	}
}
