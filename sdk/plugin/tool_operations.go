package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/ocman/ocman/sdk/protocol"
)

// HelpOperation is built into every operation tool.
const HelpOperation = "help"

var operationName = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

// ToolOperation is one named operation behind a single operation tool.
// RequiredGrants hide it, including from help, unless all are approved.
type ToolOperation struct {
	Name           string
	Summary        string
	InputSchema    json.RawMessage
	Examples       []json.RawMessage
	RequiredGrants []string
	Mutates        bool
	// Confirm returns nil when the call needs no user confirmation. Otherwise
	// Run is only called once the host attests a confirmation with this key.
	Confirm func(ctx context.Context, call ToolOperationCall) (*ToolConfirmationRequest, error)
	Run     func(ctx context.Context, call ToolOperationCall) (ToolResult, error)
}

// ToolOperationCall is one validated operation call.
type ToolOperationCall struct {
	OperationID  string
	Operation    string
	Arguments    json.RawMessage
	Context      ToolContext
	Confirmation *ToolConfirmed
}

type operationEnvelope struct {
	Operation string          `json:"operation"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type operationSummary struct {
	Name         string `json:"name"`
	Summary      string `json:"summary"`
	Mutates      bool   `json:"mutates"`
	Confirmation string `json:"confirmation,omitempty"`
}

type operationHelp struct {
	operationSummary
	InputSchema json.RawMessage   `json:"inputSchema"`
	Examples    []json.RawMessage `json:"examples"`
}

var helpSchema = json.RawMessage(`{"type":"object","properties":{"operation":{"type":"string","description":"Operation to describe; omit to list operations."}},"additionalProperties":false}`)

// OperationToolSchema is the envelope every operation tool declares.
func OperationToolSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"operation":{"type":"string","minLength":1,"maxLength":64,"description":"Operation name. Call \"help\" to list operations, then help with {\"operation\":NAME} for its argument schema and examples."},"arguments":{"type":"object","description":"Arguments for the operation, as described by help."}},"required":["operation"],"additionalProperties":false}`)
}

// NewOperationTool declares one tool that dispatches named operations through
// the {operation, arguments} envelope, and serves it. help is built in and
// lists only operations the current grants allow.
func NewOperationTool(name, description string, operations []ToolOperation) (ToolDescriptor, ToolFunc, error) {
	byName := map[string]ToolOperation{}
	for _, op := range operations {
		if !operationName.MatchString(op.Name) || op.Name == HelpOperation || byName[op.Name].Name != "" || op.Summary == "" || op.Run == nil ||
			protocol.ValidateToolSchema(op.InputSchema) != nil {
			return ToolDescriptor{}, nil, errors.New("invalid tool operation " + op.Name)
		}
		for _, example := range op.Examples {
			if protocol.ValidateToolArguments(op.InputSchema, example) != nil {
				return ToolDescriptor{}, nil, errors.New("invalid example for tool operation " + op.Name)
			}
		}
		byName[op.Name] = op
	}
	descriptor := ToolDescriptor{Name: name, Description: description, InputSchema: OperationToolSchema()}
	run := func(ctx context.Context, operationID string, inv ToolInvocation) (ToolResult, error) {
		var env operationEnvelope
		if json.Unmarshal(inv.Arguments, &env) != nil {
			return ToolError("arguments must be {\"operation\": NAME, \"arguments\": {...}}"), nil
		}
		if len(env.Arguments) == 0 || string(env.Arguments) == "null" {
			env.Arguments = json.RawMessage(`{}`)
		}
		allowed := func(op ToolOperation) bool {
			return op.Name != "" && !slices.ContainsFunc(op.RequiredGrants, func(g string) bool { return !slices.Contains(inv.Context.Grants, g) })
		}
		if env.Operation == HelpOperation {
			return operationHelpResult(operations, byName, allowed, env.Arguments)
		}
		op := byName[env.Operation]
		if !allowed(op) {
			return ToolError("unknown operation " + env.Operation + "; call operation help to list available operations"), nil
		}
		if err := protocol.ValidateToolArguments(op.InputSchema, env.Arguments); err != nil {
			return ToolError(err.Error() + "; call operation help with {\"operation\":\"" + op.Name + "\"} for the schema"), nil
		}
		call := ToolOperationCall{OperationID: operationID, Operation: op.Name, Arguments: env.Arguments, Context: inv.Context, Confirmation: inv.Confirmation}
		if op.Confirm != nil {
			request, err := op.Confirm(ctx, call)
			if err != nil {
				return ToolResult{}, err
			}
			if request != nil && (inv.Confirmation == nil || inv.Confirmation.Key != request.Key) {
				// The host sees only the envelope schema, so name this
				// operation's write-only values for it to hide.
				request.Redact = boundedRedactions(request.Redact, protocol.ToolWriteOnlyPointers(op.InputSchema, env.Arguments))
				return ToolResult{Confirmation: request}, nil
			}
		}
		return op.Run(ctx, call)
	}
	return descriptor, run, nil
}

func operationHelpResult(operations []ToolOperation, byName map[string]ToolOperation, allowed func(ToolOperation) bool, args json.RawMessage) (ToolResult, error) {
	if err := protocol.ValidateToolArguments(helpSchema, args); err != nil {
		return ToolError(err.Error()), nil
	}
	var request struct {
		Operation string `json:"operation"`
	}
	_ = json.Unmarshal(args, &request)
	summary := func(op ToolOperation) operationSummary {
		s := operationSummary{Name: op.Name, Summary: op.Summary, Mutates: op.Mutates}
		if op.Confirm != nil {
			s.Confirmation = "may-require-user"
		}
		return s
	}
	if request.Operation == "" {
		list := []operationSummary{}
		for _, op := range operations {
			if allowed(op) {
				list = append(list, summary(op))
			}
		}
		return ToolData("Call this tool with {\"operation\":\"help\",\"arguments\":{\"operation\":NAME}} for an operation's schema and examples.", map[string]any{"operations": list})
	}
	op := byName[request.Operation]
	if !allowed(op) {
		return ToolError("unknown operation " + request.Operation + "; call operation help to list available operations"), nil
	}
	examples := op.Examples
	if examples == nil {
		examples = []json.RawMessage{}
	}
	return ToolData("", operationHelp{operationSummary: summary(op), InputSchema: op.InputSchema, Examples: examples})
}

// boundedRedactions adds an operation's write-only pointers under
// /arguments. When they would exceed the host's count or length limits, it
// hides all of the operation's arguments instead, never fewer values.
func boundedRedactions(declared, writeOnly []string) []string {
	if len(writeOnly) == 0 {
		return declared
	}
	out := slices.Clone(declared)
	for _, pointer := range writeOnly {
		out = append(out, "/arguments"+pointer)
	}
	if len(out) <= protocol.MaxToolRedactions && !slices.ContainsFunc(out, func(p string) bool { return len(p) > protocol.MaxToolRedactionBytes }) {
		return out
	}
	coalesced := []string{"/arguments"}
	for _, pointer := range declared {
		if len(coalesced) < protocol.MaxToolRedactions && len(pointer) <= protocol.MaxToolRedactionBytes && pointer != "/arguments" && !strings.HasPrefix(pointer, "/arguments/") {
			coalesced = append(coalesced, pointer)
		}
	}
	return coalesced
}
