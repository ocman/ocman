package remote

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/state"
)

type inboxMetadataPlatform struct {
	*fakePlatform
	reads, heavy int
}

func (p *inboxMetadataPlatform) SessionMetadata(_ context.Context, id string) (*db.Session, error) {
	p.reads++
	if id == "gone" {
		return nil, errors.New("deleted")
	}
	return &db.Session{ID: id, ParentID: "parent", Title: "Owner child"}, nil
}

func (p *inboxMetadataPlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	p.heavy++
	return nil, errors.New("transcript read")
}

func (p *inboxMetadataPlatform) Sessions(context.Context, string, int64) ([]db.Session, error) {
	p.heavy++
	return nil, nil
}

func TestInboxTitlesOwnerMetadata(t *testing.T) {
	store := openInboxStore(t)
	if err := store.EnsurePermissionInboxItem(t.Context(), state.InboxPermission{
		Platform: "opencode", SessionID: "child", PermissionID: "p", Permission: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	adapter := &inboxMetadataPlatform{fakePlatform: &fakePlatform{id: "opencode"}}
	reg := platforms.NewRegistry()
	reg.Register(adapter)
	s := NewServer(reg, localStubHost{}, "laptop", "test").UseInboxStore(store.DB)
	for _, archived := range []bool{false, true} {
		var resp *pb.JsonResp
		var err error
		if archived {
			items, _ := store.ListInboxItems(t.Context())
			if err := store.ArchiveInboxItems(t.Context(), []string{items[0].ID}); err != nil {
				t.Fatal(err)
			}
			resp, err = s.ArchivedInboxItems(t.Context(), &pb.Empty{})
		} else {
			resp, err = s.InboxItems(t.Context(), &pb.Empty{})
		}
		if err != nil {
			t.Fatal(err)
		}
		var items []state.InboxItem
		if err := unmarshalJSON(resp.Payload, &items); err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].Session.Title != "Owner child" || items[0].Session.Platform != "opencode" || adapter.heavy != 0 {
			t.Fatalf("items=%+v heavy reads=%d", items, adapter.heavy)
		}
	}
	items := s.inboxSessionTitles(t.Context(), []state.InboxItem{
		{Session: &state.InboxSession{Platform: "opencode", SessionID: "child"}},
		{Session: &state.InboxSession{Platform: "opencode", SessionID: "child"}},
		{Session: &state.InboxSession{Platform: "opencode", SessionID: "gone", Title: "Stored title"}},
		{Session: &state.InboxSession{Platform: "unknown", SessionID: "child"}},
		{Title: "Legacy"},
	})
	if adapter.reads != 4 || items[2].Session.Title != "Stored title" || items[4].Session != nil {
		t.Fatalf("reads=%d items=%+v", adapter.reads, items)
	}
	s.registry = nil
	if got := s.inboxSessionTitles(t.Context(), items); len(got) != len(items) {
		t.Fatal("unavailable registry dropped items")
	}
}
