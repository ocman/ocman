package remote

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
)

// Refresh mutable account intent on the existing authenticated transport. Source
// proof belongs to the immutable adapters, so source changes require reconnect.
func (c *RemoteConn) refreshConnectionMetadata(ctx context.Context, transport *grpc.ClientConn, client pb.OcmanClient, owner string, keys []string) error {
	next, err := loadConnectionPlatforms(ctx, client, keys)
	if err != nil {
		return err
	}
	previous := c.ConnectionPlatforms()
	if err := validateConnectionSourceContinuity(previous, next); err != nil {
		return err
	}
	if len(previous) != len(next) {
		return fmt.Errorf("remote source inventory changed")
	}
	for i, entry := range next {
		before := previous[i]
		oldKeys, newKeys := append([]string(nil), before.SourceKeys...), append([]string(nil), entry.SourceKeys...)
		slices.Sort(oldKeys)
		slices.Sort(newKeys)
		if before.ID != entry.ID || before.Kind != entry.Kind || before.SourceKey != entry.SourceKey || !slices.Equal(oldKeys, newKeys) {
			return fmt.Errorf("remote source proof changed; reconnect required")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != transport || c.client == nil || c.remoteID != owner || c.health != HealthConnected {
		return platforms.ErrRemoteOffline
	}
	c.connectionPlatforms = copyConnectionPlatforms(next)
	c.lastSeen = time.Now()
	return nil
}

func (c *RemoteConn) markMetadataOffline(transport *grpc.ClientConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == transport {
		c.health = HealthOffline
	}
}
