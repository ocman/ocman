package remote

import (
	"encoding/json"
	"fmt"
)

// envelope.go holds the JSON marshal/unmarshal helpers used to move rich
// Go types across the gRPC wire as opaque bytes (AD-11). Both ends are
// the same codebase/version, so JSON field-name compatibility is
// guaranteed and the protocol-version handshake guards breaking changes.

// marshalJSON serialises v to JSON bytes for a JsonResp/JsonReq payload.
func marshalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("remote: marshalling payload: %w", err)
	}
	return b, nil
}

// unmarshalJSON deserialises a JsonResp/JsonReq payload into v. An empty
// payload is treated as a no-op (leaves v at its zero value) so callers
// can decode optional/empty bodies safely.
func unmarshalJSON(b []byte, v any) error {
	if len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("remote: unmarshalling payload: %w", err)
	}
	return nil
}
