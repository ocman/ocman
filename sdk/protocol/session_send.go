package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
)

var FollowupSessionCapability = Capability{Name: "session", Version: Version{Major: 1, Minor: 5}}

const SessionSendMethod = "send"
const SessionSendGrant = "session.send"

// After is the opaque operation ID of a completed call from this session.
// No directory, model, agent or permission override can be supplied.
type SessionSendParams struct {
	After  string `json:"after"`
	Prompt string `json:"prompt"`
}

func (p SessionSendParams) Validate() error {
	if !validText(p.After) {
		return ErrInvalidMessage
	}
	return (SessionRunParams{Prompt: p.Prompt}).Validate()
}

func (b *SessionBroker) send(ctx context.Context, pluginID string, call Call) (SessionRunResult, error) {
	if call.Capability != SessionCapability.Name || call.Version.Major != 1 || call.Version.Minor < 5 || call.Version.Minor > AssociatedSessionCapability.Version.Minor {
		return SessionRunResult{}, &WireError{Category: ErrorNotFound}
	}
	var params SessionSendParams
	d := json.NewDecoder(bytes.NewReader(call.Params))
	d.DisallowUnknownFields()
	if !validText(call.OperationID) || len(call.Params) > MaxMessageBytes || checkJSON(call.Params) != nil || d.Decode(&params) != nil || d.Decode(new(any)) != io.EOF || params.Validate() != nil {
		return SessionRunResult{}, &WireError{Category: ErrorInvalidArgument}
	}
	ctx, cancel := context.WithTimeout(ctx, SessionRunTimeout)
	defer cancel()
	if err := b.authorize(ctx, pluginID, func(d Description, grants []string, _ Config) error {
		if err := SessionAllowed(d, grants); err != nil {
			return err
		}
		if !slices.Contains(d.RequestedGrants, SessionSendGrant) || !slices.Contains(grants, SessionSendGrant) || !slices.ContainsFunc(d.Capabilities, func(c Capability) bool { return c.Name == "session" && c.Version.Major == 1 && c.Version.Minor >= 5 }) {
			return &WireError{Category: ErrorPermissionDenied}
		}
		return nil
	}); err != nil {
		return SessionRunResult{}, err
	}
	if b.Continue == nil {
		return SessionRunResult{}, &WireError{Category: ErrorUnavailable}
	}
	id, err := b.Continue(ctx, pluginID, params, call.OperationID)
	if err != nil {
		return SessionRunResult{}, err
	}
	if id == "" {
		return SessionRunResult{}, ErrInvalidMessage
	}
	return SessionRunResult{SessionID: id}, nil
}
