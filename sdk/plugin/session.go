package plugin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

// Aliases and constants for session.v1, versioned independently of action.v1
// and conversation.v1.
type (
	SessionRunParams = plugins.SessionRunParams
	SessionRunResult = plugins.SessionRunResult
	SessionCompleted = plugins.SessionCompleted
	SessionUsage     = plugins.SessionUsage
)

const (
	SessionRunGrant        = plugins.SessionRunGrant
	SessionProjectGrant    = plugins.SessionProjectGrant
	SessionPluginGrant     = plugins.SessionPluginGrant
	SessionProjectSetting  = plugins.SessionProjectSetting
	SessionRunMethod       = plugins.SessionRunMethod
	SessionCompletedMethod = plugins.SessionCompletedMethod
)

// SessionCapability is session.v1.
var SessionCapability = plugins.SessionCapability

var ProjectSessionCapability = plugins.ProjectSessionCapability
var PluginSessionCapability = plugins.PluginSessionCapability
var ArchivedSessionCapability = plugins.ArchivedSessionCapability

// NewSessionRun builds a validated plugin to host call asking the host to
// start a session in the plugin's one approved project. Validating here keeps
// an unvalidated payload from reaching the wire and failing the whole stream.
// The plugin never names a directory. Call.Version marks the minimum needed by
// these parameters; RunWithRequests selects the negotiated version at admission.
func NewSessionRun(operationID string, params SessionRunParams) (Call, error) {
	if err := params.Validate(); err != nil {
		return Call{}, err
	}
	data, err := json.Marshal(params)
	if err != nil {
		return Call{}, err
	}
	call := Call{ID: 1, OperationID: operationID, Capability: SessionCapability.Name, Version: SessionCapability.Version,
		Method: SessionRunMethod, DeadlineUnixMS: time.Now().Add(plugins.SessionRunTimeout).UnixMilli(), Params: data}
	if params.ProjectID != "" || params.NoTools {
		call.Version = ProjectSessionCapability.Version
	}
	if params.WorkspaceID != "" {
		call.Version = PluginSessionCapability.Version
	}
	if params.Archived {
		call.Version = ArchivedSessionCapability.Version
	}
	if err := (Envelope{Type: TypeCall, Call: &call}).Validate(); err != nil {
		return Call{}, err
	}
	return call, nil
}

// SessionHandler serves unary session.v1 completed calls. The host owns
// enablement, grants and the approved project; this adapter only checks the
// frame and hands over a validated completion.
//
// The operation id is passed through because the host's delivery is
// at-least-once: it is stable across retries of the same completed turn, so a
// connector can recognize a repeat and act on the turn only once.
func SessionHandler(completed func(ctx context.Context, operationID string, c SessionCompleted) error) Handler {
	return func(ctx context.Context, call Call, _ func(json.RawMessage) error) (json.RawMessage, error) {
		if completed == nil {
			return nil, Failure(ErrorInternal)
		}
		if call.Capability != SessionCapability.Name || call.Version.Major != SessionCapability.Version.Major || call.Method != SessionCompletedMethod {
			return nil, Failure(ErrorNotFound)
		}
		var c SessionCompleted
		if json.Unmarshal(call.Params, &c) != nil || c.Validate() != nil {
			return nil, Failure(ErrorInvalidArgument)
		}
		if err := completed(ctx, call.OperationID, c); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	}
}
