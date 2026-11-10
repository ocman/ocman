package remote

import (
	"context"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ConnectionPlatformMetadata separates owner routing keys from model kinds.
type ConnectionPlatformMetadata struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"displayName"`
	Default     bool   `json:"default,omitempty"`
	Selectable  bool   `json:"selectable"`
	// Physical locator is authenticated backend metadata, never browser data.
	SourceKey  string   `json:"sourceKey,omitempty"`
	SourceKeys []string `json:"sourceKeys,omitempty"`
}

func (s *Server) ConnectionPlatforms(ctx context.Context, _ *pb.CallReq) (*pb.JsonResp, error) {
	entries := []ConnectionPlatformMetadata{}
	for _, entry := range s.registry.Entries() {
		if owner, _ := platforms.SplitPlatformID(string(entry.Key)); owner != "" {
			continue
		}
		metadata := ConnectionPlatformMetadata{ID: string(entry.Key), Kind: string(entry.Platform.ID()), DisplayName: entry.Platform.DisplayName(), Selectable: entry.Key == entry.Platform.ID()}
		if reader, ok := entry.Platform.(interface {
			HistoryIdentity(context.Context) (platforms.HistoryIdentity, error)
		}); ok {
			source, err := reader.HistoryIdentity(ctx)
			if err != nil {
				return nil, err
			}
			if source.Platform != metadata.Kind {
				return nil, fmt.Errorf("connection physical kind mismatch")
			}
			metadata.SourceKey = source.SourceKey
		}
		if reader, ok := entry.Platform.(interface{ ReadSourceKeys() []string }); ok {
			metadata.SourceKeys = append([]string(nil), reader.ReadSourceKeys()...)
		}
		if err := validateConnectionSources(metadata); err != nil {
			return nil, err
		}
		entries = append(entries, metadata)
	}
	if h, ok := s.host.(hostsvc.ConnectionHost); ok {
		inventory, err := h.InventoryPlatformConnections(ctx)
		if err != nil {
			return nil, err
		}
		for i, entry := range entries {
			for _, c := range inventory.Connections {
				if c.ID == entry.ID {
					entries[i].DisplayName = c.Name
					entries[i].Default = c.IsDefault
					entries[i].Selectable = c.Enabled && c.Status == "ready"
					break
				}
			}
		}
	}
	return jsonResp(entries, nil)
}

// Named accounts never inherit legacy first-kind/first-entry fallback.
func (c *RemoteConn) creationPlatform(preferred string) string {
	entries := c.ConnectionPlatforms()
	named := false
	for _, entry := range entries {
		if entry.ID != entry.Kind {
			named = true
			break
		}
	}
	if !named {
		return selectPlatform(c.Platforms(), preferred)
	}
	for _, entry := range entries {
		if entry.ID == preferred {
			if entry.Selectable {
				return entry.ID
			}
			return ""
		}
	}
	if strings.HasPrefix(preferred, "cnx_") {
		return ""
	}
	for _, entry := range entries {
		if entry.Selectable && entry.Default {
			return entry.ID
		}
	}
	return ""
}

func loadConnectionPlatforms(ctx context.Context, client pb.OcmanClient, keys []string) ([]ConnectionPlatformMetadata, error) {
	response, err := client.Call(ctx, &pb.CallReq{Method: "ConnectionPlatforms"})
	if status.Code(err) == codes.Unimplemented {
		result := make([]ConnectionPlatformMetadata, 0, len(keys))
		for _, key := range keys {
			if strings.HasPrefix(key, "cnx_") {
				return nil, fmt.Errorf("named remote connection metadata unavailable")
			}
			result = append(result, ConnectionPlatformMetadata{ID: key, Kind: key, DisplayName: platforms.DisplayName(platforms.ID(key))})
		}
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	var result []ConnectionPlatformMetadata
	if err := unmarshalJSON(response.GetPayload(), &result); err != nil {
		return nil, err
	}
	if len(result) != len(keys) {
		return nil, fmt.Errorf("remote connection inventory changed during handshake")
	}
	seen := map[string]bool{}
	for i, entry := range result {
		owner, _ := platforms.SplitPlatformID(entry.ID)
		if entry.ID != keys[i] || entry.Kind == "" || owner != "" || seen[entry.ID] {
			return nil, fmt.Errorf("invalid remote connection metadata")
		}
		seen[entry.ID] = true
		if err := validateConnectionSources(entry); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (c *RemoteConn) ConnectionPlatforms() []ConnectionPlatformMetadata {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.connectionPlatforms != nil {
		return copyConnectionPlatforms(c.connectionPlatforms)
	}
	result := make([]ConnectionPlatformMetadata, 0, len(c.platforms))
	for _, key := range c.platforms {
		result = append(result, ConnectionPlatformMetadata{ID: key, Kind: key, DisplayName: platforms.DisplayName(platforms.ID(key))})
	}
	return result
}
