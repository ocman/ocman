package remote

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

const canonicalSessionProtocol = 9

func (p *remotePlatform) CanonicalSessionIDs() bool {
	return p.conn.ProtocolVersion() >= canonicalSessionProtocol
}

func (p *remotePlatform) canonicalIdentity(info platforms.SessionIdentity) bool {
	return info.OwnerID == p.conn.RemoteID() && info.Platform == p.base && sessioncatalog.IsID(info.ID) &&
		(info.ParentID == "" || sessioncatalog.IsID(info.ParentID)) && info.NativeID == "" && info.NativeParentID == "" && info.SourceKey != "" && info.CreationState == "ready"
}

type legacyReferenceRequest struct {
	References []string `json:"references"`
}

func (p *remotePlatform) ResolveLegacyReferences(ctx context.Context, refs []string) (map[string]platforms.SessionIdentity, error) {
	if !p.CanonicalSessionIDs() {
		return nil, platforms.ErrUnsupported
	}
	client := p.conn.Client()
	if client == nil {
		return nil, platforms.ErrRemoteOffline
	}
	payload, err := marshalJSON(legacyReferenceRequest{References: refs})
	if err != nil {
		return nil, err
	}
	response, err := client.SessionIdentities(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: payload})
	if err != nil {
		return nil, identityClientError(err)
	}
	var rows map[string]platforms.SessionIdentity
	if err := unmarshalJSON(response.Payload, &rows); err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	seen := map[string]bool{}
	for _, ref := range refs {
		wanted[ref] = true
	}
	for ref, row := range rows {
		if !wanted[ref] || !p.canonicalIdentity(row) || seen[row.ID] {
			return nil, fmt.Errorf("%w: remote legacy-reference identity mismatch", platforms.ErrPlatformUnreachable)
		}
		seen[row.ID] = true
	}
	return rows, nil
}
