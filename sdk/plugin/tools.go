package plugin

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ocman/ocman/sdk/protocol"
)

type (
	ToolDescriptor          = protocol.ToolDescriptor
	ToolAnnotations         = protocol.ToolAnnotations
	ToolInvocation          = protocol.ToolInvocation
	ToolContext             = protocol.ToolContext
	ToolConfirmed           = protocol.ToolConfirmed
	ToolResult              = protocol.ToolResult
	ToolConfirmationRequest = protocol.ToolConfirmationRequest
	ToolArgumentError       = protocol.ToolArgumentError
)

var ToolCapability = protocol.ToolCapability

const (
	ToolAgentGrant   = protocol.ToolAgentGrant
	ToolInvokeMethod = protocol.ToolInvokeMethod
)

// ToolFunc serves one validated invocation of a declared tool. Arguments
// already passed the declared schema; domain validation is still the
// plugin's. Return an IsError result for failures the agent should read.
type ToolFunc func(ctx context.Context, operationID string, inv ToolInvocation) (ToolResult, error)

// ToolHandler routes tool.v1 invoke calls by tool name. Calls for other
// capabilities and unknown tools return not_found; an invalid result is
// reported as internal rather than sent.
func ToolHandler(tools map[string]ToolFunc) Handler {
	return func(ctx context.Context, call Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if call.Capability != ToolCapability.Name || call.Version.Major != ToolCapability.Version.Major || call.Method != ToolInvokeMethod {
			return nil, Failure(ErrorNotFound)
		}
		inv, err := protocol.DecodeToolInvocation(call.Params)
		if err != nil {
			return nil, Failure(ErrorInvalidArgument)
		}
		run := tools[inv.Tool]
		if run == nil {
			return nil, Failure(ErrorNotFound)
		}
		result, err := run(ctx, call.OperationID, inv)
		if err != nil {
			return nil, err
		}
		if result.Validate() != nil {
			return nil, errors.New("invalid tool result")
		}
		return json.Marshal(result)
	}
}

// ToolText is a plain successful result.
func ToolText(text string) ToolResult { return ToolResult{Text: text} }

// ToolData is a successful result whose data is v marshalled as an object.
func ToolData(text string, v any) (ToolResult, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Text: text, Data: raw}, nil
}

// ToolError is a domain error the agent can read and act on.
func ToolError(text string) ToolResult { return ToolResult{Text: text, IsError: true} }
