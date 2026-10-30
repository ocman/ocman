package plugin

import (
	"encoding/json"
	"time"

	"github.com/ocman/ocman/sdk/protocol"
)

func NewSessionSend(operationID string, params SessionSendParams) (Call, error) {
	if err := params.Validate(); err != nil {
		return Call{}, err
	}
	data, err := json.Marshal(params)
	if err != nil {
		return Call{}, err
	}
	call := Call{ID: 1, OperationID: operationID, Capability: SessionCapability.Name,
		Version: FollowupSessionCapability.Version, Method: SessionSendMethod,
		DeadlineUnixMS: time.Now().Add(protocol.SessionRunTimeout).UnixMilli(), Params: data}
	return call, (Envelope{Type: TypeCall, Call: &call}).Validate()
}
