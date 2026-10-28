package remote

import (
	"errors"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
)

func (h *remoteHost) decodeWorktreeSession(raw []byte) (*hostsvc.WorktreeSessionResult, error) {
	var out hostsvc.WorktreeSessionResult
	err := unmarshalJSON(raw, &out)
	if h.conn.ProtocolVersion() < canonicalSessionProtocol {
		return &out, err
	}
	if err == nil && !identity.IsSessionID(out.SessionID) {
		err = invalidCanonicalResponse()
	}
	if err != nil {
		id := ""
		if identity.IsSessionID(out.SessionID) {
			id = out.SessionID
		}
		return nil, &platforms.CreationPendingError{SessionID: id, Err: errors.Join(platforms.ErrPlatformUnreachable, err)}
	}
	return &out, nil
}
