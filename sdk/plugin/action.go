package plugin

import (
	"context"
	"encoding/json"
	"reflect"
)

// ActionHandler handles unary action.v1 calls. The host remains responsible for
// current user grants, confirmation, context minimization and idempotency.
// This adapter rejects context not declared by the action, validates results,
// and never emits chunks. It does not infer approval from requested grants.
func ActionHandler(description Description, invoke func(context.Context, Call, ActionInvocation) ([]ActionResult, error)) Handler {
	return func(ctx context.Context, call Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if description.Validate() != nil || invoke == nil {
			return nil, Failure(ErrorInternal)
		}
		if call.Capability != "action" || call.Version != (Version{Major: 1}) || call.Method != "invoke" {
			return nil, Failure(ErrorNotFound)
		}
		var invocation ActionInvocation
		if json.Unmarshal(call.Params, &invocation) != nil || invocation.Context.Validate() != nil {
			return nil, Failure(ErrorInvalidArgument)
		}
		var action *ActionDescriptor
		for i := range description.Actions {
			if description.Actions[i].ID == invocation.ActionID {
				action = &description.Actions[i]
				break
			}
		}
		if action == nil {
			return nil, Failure(ErrorNotFound)
		}
		if len(invocation.Context.Selection) == 0 {
			invocation.Context.Selection = nil
		}
		allowed := action.Minimize(invocation.Context)
		if !reflect.DeepEqual(allowed, invocation.Context) {
			return nil, Failure(ErrorPermissionDenied)
		}
		results, err := invoke(ctx, call, invocation)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(struct {
			Results []ActionResult `json:"results"`
		}{results})
		if err != nil {
			return nil, Failure(ErrorInternal)
		}
		if _, err := DecodeActionResults(data); err != nil {
			return nil, Failure(ErrorInternal)
		}
		return data, nil
	}
}
