package protocol

import (
	"context"
	"encoding/json"
)

// Config is the host-owned, non-secret configuration admitted with a
// conversation message or session run. Providers cannot choose these values.
type Config struct {
	Project string
	Values  map[string]json.RawMessage
}

// unaryCall authorizes pluginID with allowed, sends payload as the call built
// by request inside that critical section, and awaits the single result. The
// conversation.v1 and session.v1 callbacks are unary, even though the
// transport streams.
func unaryCall(
	ctx context.Context,
	authorize func(context.Context, string, func(Description, []string, Config) error) error,
	call ActionCall,
	pluginID string,
	allowed func(Description, []string) error,
	payload any,
	request func(Description) Call,
) (json.RawMessage, error) {
	var replies <-chan Reply
	if err := authorize(ctx, pluginID, func(d Description, grants []string, _ Config) error {
		if err := allowed(d, grants); err != nil {
			return err
		}
		params, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		deadline, _ := ctx.Deadline()
		req := request(d)
		req.DeadlineUnixMS, req.Params = deadline.UnixMilli(), params
		replies, err = call(ctx, pluginID, req)
		return err
	}); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r, ok := <-replies:
		switch {
		case !ok:
			return nil, ErrUnavailable
		case r.Err != nil:
			return nil, r.Err
		case r.Message.Type != TypeResult || r.Message.Result == nil:
			return nil, ErrInvalidMessage
		case r.Message.Result.Error != nil:
			return nil, r.Message.Result.Error
		}
		return r.Message.Result.Value, nil
	}
}
