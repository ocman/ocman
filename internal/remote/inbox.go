package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/state"
)

type inboxMutation struct {
	ID  string   `json:"id,omitempty"`
	IDs []string `json:"ids,omitempty"`
	// AllRead is only sent by older hubs; keep accepting it.
	AllRead bool `json:"allRead,omitempty"`
}

func (s *Server) InboxItems(ctx context.Context, _ *pb.CallReq) (*pb.JsonResp, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "Inbox store is unavailable")
	}
	items, err := s.inboxStore.ListInboxItems(ctx)
	return jsonResp(s.inboxSessionTitles(ctx, items), err)
}

func (s *Server) ArchivedInboxItems(ctx context.Context, _ *pb.CallReq) (*pb.JsonResp, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "Inbox store is unavailable")
	}
	items, err := s.inboxStore.ListArchivedInboxItems(ctx)
	return jsonResp(s.inboxSessionTitles(ctx, items), err)
}

func (s *Server) MarkInboxItemRead(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "Inbox store is unavailable")
	}
	var mutation inboxMutation
	if err := unmarshalJSON(req.Payload, &mutation); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, s.inboxStore.MarkInboxItemRead(ctx, mutation.ID)
}

func (s *Server) ArchiveInboxItems(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "Inbox store is unavailable")
	}
	var mutation inboxMutation
	if err := unmarshalJSON(req.Payload, &mutation); err != nil {
		return nil, err
	}
	if mutation.AllRead {
		return &pb.JsonResp{}, s.inboxStore.ArchiveAllReadInboxItems(ctx)
	}
	return &pb.JsonResp{}, s.inboxStore.ArchiveInboxItems(ctx, mutation.IDs)
}

func (s *Server) MarkInboxItemUnread(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "Inbox store is unavailable")
	}
	var mutation inboxMutation
	if err := unmarshalJSON(req.Payload, &mutation); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, s.inboxStore.MarkInboxItemUnread(ctx, mutation.ID)
}

func (c *RemoteConn) InboxItems(ctx context.Context) ([]state.InboxItem, error) {
	return hostCall[[]state.InboxItem](ctx, c, "InboxItems", nil)
}

func (c *RemoteConn) ArchivedInboxItems(ctx context.Context) ([]state.InboxItem, error) {
	return hostCall[[]state.InboxItem](ctx, c, "ArchivedInboxItems", nil)
}

func (c *RemoteConn) MarkInboxItemRead(ctx context.Context, id string) error {
	return hostExec(ctx, c, "MarkInboxItemRead", inboxMutation{ID: id})
}

func (c *RemoteConn) MarkInboxItemUnread(ctx context.Context, id string) error {
	return hostExec(ctx, c, "MarkInboxItemUnread", inboxMutation{ID: id})
}

func (c *RemoteConn) ArchiveInboxItems(ctx context.Context, ids []string) error {
	return hostExec(ctx, c, "ArchiveInboxItems", inboxMutation{IDs: ids})
}

func (m *Manager) inboxOwner(source string) (*state.DB, *RemoteConn, error) {
	if source == "local" {
		if m.store == nil {
			return nil, nil, platforms.ErrRemoteOffline
		}
		return m.store, nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mr := range m.remotes {
		if mr.conn != nil && mr.host != nil && mr.conn.RemoteID() == source {
			return nil, mr.conn, nil
		}
	}
	return nil, nil, platforms.ErrRemoteOffline
}

func (m *Manager) InboxItems(ctx context.Context, source string) ([]state.InboxItem, error) {
	store, conn, err := m.inboxOwner(source)
	if err != nil {
		return nil, err
	}
	if store != nil {
		return store.ListInboxItems(ctx)
	}
	return conn.InboxItems(ctx)
}

func (m *Manager) ArchivedInboxItems(ctx context.Context, source string) ([]state.InboxItem, error) {
	store, conn, err := m.inboxOwner(source)
	if err != nil {
		return nil, err
	}
	if store != nil {
		return store.ListArchivedInboxItems(ctx)
	}
	return conn.ArchivedInboxItems(ctx)
}

// InboxSources returns the local owner and currently connected remote owners.
func (m *Manager) InboxSources() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sources := []string{}
	if m.store != nil {
		sources = append(sources, "local")
	}
	for _, mr := range m.remotes {
		if mr.host != nil && mr.conn != nil && mr.conn.RemoteID() != "" {
			sources = append(sources, mr.conn.RemoteID())
		}
	}
	return sources
}

func (m *Manager) MarkInboxItemRead(ctx context.Context, source, id string) error {
	store, conn, err := m.inboxOwner(source)
	if err != nil {
		return err
	}
	if store != nil {
		return store.MarkInboxItemRead(ctx, id)
	}
	return conn.MarkInboxItemRead(ctx, id)
}

func (m *Manager) MarkInboxItemUnread(ctx context.Context, source, id string) error {
	store, conn, err := m.inboxOwner(source)
	if err != nil {
		return err
	}
	if store != nil {
		return store.MarkInboxItemUnread(ctx, id)
	}
	return conn.MarkInboxItemUnread(ctx, id)
}

func (m *Manager) ArchiveInboxItems(ctx context.Context, source string, ids []string) error {
	store, conn, err := m.inboxOwner(source)
	if err != nil {
		return err
	}
	if store == nil {
		return conn.ArchiveInboxItems(ctx, ids)
	}
	return store.ArchiveInboxItems(ctx, ids)
}
