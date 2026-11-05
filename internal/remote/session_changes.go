package remote

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) UseSessionChanges(fn func(context.Context, func(string, string) error) error) *Server {
	s.sessionChanges = fn
	return s
}

func (s *Server) StreamSessionChanges(_ *pb.HelloReq, stream pb.Ocman_StreamSessionChangesServer) error {
	if s.sessionChanges == nil {
		return status.Error(codes.Unimplemented, "session invalidations unavailable")
	}
	return s.sessionChanges(stream.Context(), func(platform, id string) error {
		return stream.Send(&pb.SessionRef{Platform: platform, SessionId: id})
	})
}

func (p *remotePlatform) SetSessionEventSink(sink platforms.SessionEventSink) {
	p.mu.Lock()
	p.sink = sink
	p.mu.Unlock()
}

func (p *remotePlatform) publishSessionChange(id string) {
	if id == "" || (p.CanonicalSessionIDs() && !identity.IsSessionID(id)) {
		return
	}
	p.mu.RLock()
	notify := p.sink.OnSessionBackgroundChanged
	p.mu.RUnlock()
	if notify != nil {
		notify(string(p.ID()), id)
	}
}

// The connection supervisor owns and joins this stream. Old peers retain
// polling; transient stream failures retry without replacing healthy adapters.
func (m *Manager) waitForDropWithSessionChanges(ctx context.Context, mr *managedRemote, adapters []*remotePlatform) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			err := m.receiveSessionChanges(ctx, mr, adapters)
			if status.Code(err) == codes.Unimplemented || !sleepCtx(ctx, reconnectBaseDelay) {
				return
			}
		}
	}()
	mr.conn.WaitForDrop(ctx)
	cancel()
	<-done
}

func (m *Manager) receiveSessionChanges(ctx context.Context, mr *managedRemote, adapters []*remotePlatform) error {
	client := mr.conn.Client()
	if client == nil {
		return platforms.ErrRemoteOffline
	}
	stream, err := client.StreamSessionChanges(ctx, &pb.HelloReq{})
	if err != nil {
		return err
	}
	for {
		change, err := stream.Recv()
		if err != nil {
			return err
		}
		for _, p := range adapters {
			// Never trust a peer-supplied owner prefix or a removed adapter.
			current, ok := m.registry.Get(p.ID())
			if !ok || current != p {
				continue
			}
			if change.Platform == "" && change.SessionId == "" {
				p.mu.RLock()
				ids := make([]string, 0, len(p.lastSess))
				for _, row := range p.lastSess {
					ids = append(ids, row.ID)
				}
				p.mu.RUnlock()
				for _, id := range ids {
					p.publishSessionChange(id)
				}
			} else if change.Platform == p.base {
				p.publishSessionChange(change.SessionId)
			}
		}
	}
}
