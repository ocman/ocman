package remote

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/state"
)

type inboxMutation struct {
	ID      string   `json:"id,omitempty"`
	IDs     []string `json:"ids,omitempty"`
	AllRead bool     `json:"allRead,omitempty"`
}

func (s *Server) InboxItems(ctx context.Context, _ *pb.Empty) (*pb.JsonResp, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "Inbox store is unavailable")
	}
	return jsonResp(s.inboxStore.ListInboxItems(ctx))
}

func (s *Server) MarkInboxItemRead(ctx context.Context, req *pb.JsonReq) (*pb.Empty, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "Inbox store is unavailable")
	}
	var mutation inboxMutation
	if err := unmarshalJSON(req.Payload, &mutation); err != nil {
		return nil, err
	}
	return &pb.Empty{}, s.inboxStore.MarkInboxItemRead(ctx, mutation.ID)
}

func (s *Server) ArchiveInboxItems(ctx context.Context, req *pb.JsonReq) (*pb.Empty, error) {
	if s.inboxStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "Inbox store is unavailable")
	}
	var mutation inboxMutation
	if err := unmarshalJSON(req.Payload, &mutation); err != nil {
		return nil, err
	}
	if mutation.AllRead {
		return &pb.Empty{}, s.inboxStore.ArchiveAllReadInboxItems(ctx)
	}
	return &pb.Empty{}, s.inboxStore.ArchiveInboxItems(ctx, mutation.IDs)
}

func (c *RemoteConn) InboxItems(ctx context.Context) ([]state.InboxItem, error) {
	client := c.Client()
	if client == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := client.InboxItems(ctx, &pb.Empty{})
	if err != nil {
		return nil, err
	}
	var items []state.InboxItem
	if err := unmarshalJSON(resp.Payload, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *RemoteConn) MarkInboxItemRead(ctx context.Context, id string) error {
	client := c.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	payload, err := marshalJSON(inboxMutation{ID: id})
	if err != nil {
		return err
	}
	_, err = client.MarkInboxItemRead(ctx, &pb.JsonReq{Payload: payload})
	return err
}

func (c *RemoteConn) ArchiveInboxItems(ctx context.Context, ids []string, allRead bool) error {
	client := c.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	payload, err := marshalJSON(inboxMutation{IDs: ids, AllRead: allRead})
	if err != nil {
		return err
	}
	_, err = client.ArchiveInboxItems(ctx, &pb.JsonReq{Payload: payload})
	return err
}

func (m *Manager) inboxOwner(source string) (*state.DB, *RemoteConn, error) {
	if source == "local" {
		if m.store == nil {
			return nil, nil, ErrRemoteOffline
		}
		return m.store, nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mr := range m.remotes {
		if mr.conn != nil && mr.platform != nil && mr.conn.RemoteID() == source {
			return nil, mr.conn, nil
		}
	}
	return nil, nil, ErrRemoteOffline
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

func (m *Manager) ArchiveInboxItems(ctx context.Context, source string, ids []string, allRead bool) error {
	store, conn, err := m.inboxOwner(source)
	if err != nil {
		return err
	}
	if store == nil {
		return conn.ArchiveInboxItems(ctx, ids, allRead)
	}
	if allRead {
		return store.ArchiveAllReadInboxItems(ctx)
	}
	return store.ArchiveInboxItems(ctx, ids)
}
