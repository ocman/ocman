package conversationread

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type readPlatform struct {
	platforms.Platform
	id                     platforms.ID
	head                   platforms.ConversationHead
	headErr                error
	live                   *platforms.LiveState
	onHead                 func(int, *platforms.ConversationHead)
	nativeCalls, headCalls int
}

func (p *readPlatform) ID() platforms.ID                       { return p.id }
func (p *readPlatform) LiveStatus(string) *platforms.LiveState { return p.live }
func (p *readPlatform) ConversationHead(context.Context, string) (*platforms.ConversationHead, error) {
	p.headCalls++
	h := p.head
	if p.onHead != nil {
		p.onHead(p.headCalls, &h)
	}
	return &h, p.headErr
}
func (p *readPlatform) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	if d, handled, err := platforms.TryStoredConversation(ctx, p, id, limit, offset); handled || err != nil {
		return d, err
	}
	p.nativeCalls++
	return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: db.StatusBusy}}, nil
}

func readFixture(t *testing.T) (*Service, *readPlatform, *statev2.DB, statev2.Batch) {
	t.Helper()
	store, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	identity := platforms.HistoryIdentity{Platform: "test", SourceKey: "/source"}
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "source", OwnerID: "owner", Platform: identity.Platform, SourceKey: identity.SourceKey}); err != nil {
		t.Fatal(err)
	}
	snapshot := &db.HistorySnapshot{Session: db.Session{ID: "s", Title: "Stored", Directory: "/repo", TimeCreated: 1, TimeUpdated: 6},
		Messages: []db.Message{{ID: "u", SessionID: "s", TimeCreated: 1, Data: json.RawMessage(`{"role":"user","model":{"providerID":"p","modelID":"m"}}`)},
			{ID: "opaque", SessionID: "s", TimeCreated: 2, Data: json.RawMessage(`{"role":"unknown"}`)},
			{ID: "a", SessionID: "s", TimeCreated: 3, Data: json.RawMessage(`{"role":"assistant","finish":"stop","tokens":{"input":10,"output":5,"reasoning":3,"cache":{"read":2,"write":1}},"time":{"created":3,"completed":6}}`)}},
		Parts: []db.Part{{ID: "text", MessageID: "a", SessionID: "s", TimeCreated: 4, Data: json.RawMessage(`{"type":"text","text":"","extra":1234567890123456789}`)},
			{ID: "tool", MessageID: "a", SessionID: "s", TimeCreated: 5, Data: json.RawMessage(`{"type":"tool","tool":"bash","state":{"input":{"cmd":"ok"},"output":"done"}}`)}},
		NativeMessages: map[string]json.RawMessage{"opaque": json.RawMessage(`{"kind":"system"}`), "a": json.RawMessage(`{"nativeOnly":true}`)}}
	batch, _, err := conversationingest.Normalize(statev2.Key{InstanceID: "source", SessionID: "s"}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if err := store.SetSourceState(t.Context(), "source", "read-ready/s", "v1"); err != nil {
		t.Fatal(err)
	}
	p := &readPlatform{id: "test", head: platforms.ConversationHead{Identity: identity, Version: "v1", Session: &db.Session{ID: "s", Title: "Stored", Directory: "/repo", Status: db.StatusWaiting}, DirectoryDefaults: true}}
	return &Service{Store: store, OwnerID: "owner"}, p, store, batch
}

func TestCompletedConversationUsesStoreAndRestoresWireFields(t *testing.T) {
	s, p, _, _ := readFixture(t)
	d, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.nativeCalls != 0 || d.ConversationSource != "statev2" || d.TotalMessages != 2 || len(d.Messages) != 1 || d.Messages[0].ID != "a" {
		t.Fatalf("did not select stored newest page: %+v calls=%d", d, p.nativeCalls)
	}
	if d.ContextTokenCount != 21 || d.DefaultModel != "p/m" || len(d.Parts) != 2 || d.Parts[0].TimeCreated != 4 || d.Parts[1].TimeCreated != 5 {
		t.Fatalf("wire metadata changed: %+v", d)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(d.Messages[0].Data, &metadata); err != nil {
		t.Fatal(err)
	}
	if _, ok := metadata["native"]; ok {
		t.Fatal("native envelope leaked into v1 metadata")
	}
	var text, tool map[string]json.RawMessage
	_ = json.Unmarshal(d.Parts[0].Data, &text)
	_ = json.Unmarshal(d.Parts[1].Data, &tool)
	if string(text["text"]) != `""` || string(text["extra"]) != "1234567890123456789" {
		t.Fatal("empty text or integer precision lost", text)
	}
	if _, ok := tool["text"]; ok {
		t.Fatal("fabricated text field on tool part")
	}
}

func TestConversationSourceSelectionDoesNotChangeLiveReads(t *testing.T) {
	for _, kind := range []string{"no context", "busy", "permission", "remote"} {
		t.Run(kind, func(t *testing.T) {
			s, p, _, _ := readFixture(t)
			ctx := platforms.WithConversationReader(t.Context(), s)
			switch kind {
			case "no context":
				ctx = t.Context()
			case "busy":
				p.live = &platforms.LiveState{Status: "busy"}
			case "permission":
				p.live = &platforms.LiveState{PendingPermission: true}
			case "remote":
				p.id = "r-box:test"
			}
			if _, err := p.Session(ctx, "s", 30, 0); err != nil {
				t.Fatal(err)
			}
			if p.nativeCalls != 1 {
				t.Fatal("live/platform path was changed", p.nativeCalls)
			}
		})
	}
}

func TestEligibleConversationNeverFallsBack(t *testing.T) {
	for _, kind := range []string{"no store", "no receipt", "stale", "old format", "changed during read", "wrong identity", "other owner", "unsettled"} {
		t.Run(kind, func(t *testing.T) {
			s, p, store, batch := readFixture(t)
			notified := 0
			s.Notify = func(string, string) { notified++ }
			switch kind {
			case "unsettled":
				p.head.Session.Status = ""
			case "no store":
				s.Store = nil
			case "no receipt":
				if err := store.SetSourceState(t.Context(), "source", "read-ready/s", ""); err != nil {
					t.Fatal(err)
				}
			case "stale":
				p.head.Version = "v2"
			case "old format":
				batch.ExpectedRevision = 1
				batch.Session.ReadFormat = 0
				if _, err := store.Commit(t.Context(), batch); err != nil {
					t.Fatal(err)
				}
			case "changed during read":
				p.onHead = func(n int, h *platforms.ConversationHead) {
					if n == 2 {
						h.Version = "v2"
					}
				}
			case "wrong identity":
				p.head.Session.ID = "other"
			case "other owner":
				s.OwnerID = "another-owner"
			}
			_, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0)
			if !errors.Is(err, platforms.ErrHistoryUnavailable) || p.nativeCalls != 0 || notified != 1 {
				t.Fatalf("silently fell back: %v native=%d notify=%d", err, p.nativeCalls, notified)
			}
		})
	}
}

// A turn that just ended is read before ingestion has applied its final
// events. The read waits for that session's ingestion instead of failing.
func TestSettledReadWaitsForIngestionToCatchUp(t *testing.T) {
	s, p, store, _ := readFixture(t)
	p.head.Version = "seq:12"
	if err := store.SetSourceState(t.Context(), "source", "read-ready/s", "seq:10"); err != nil {
		t.Fatal(err)
	}
	waits := 0
	s.Await = func(ctx context.Context, platformID, sessionID, version string) bool {
		waits++
		if platformID != "test" || sessionID != "s" || version != "seq:12" {
			t.Errorf("awaited %s %s %s", platformID, sessionID, version)
		}
		if err := store.SetSourceState(ctx, "source", "read-ready/s", "seq:12"); err != nil {
			t.Error(err)
		}
		return true
	}
	d, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0)
	if err != nil || d.ConversationSource != "statev2" || waits != 1 || p.nativeCalls != 0 {
		t.Fatalf("read = %v, %v after %d waits", d, err, waits)
	}
	// Ingestion that does not catch up in time still fails closed.
	p.head.Version = "seq:20"
	s.Await = func(context.Context, string, string, string) bool { return false }
	if _, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0); !errors.Is(err, platforms.ErrHistoryUnavailable) || p.nativeCalls != 0 {
		t.Fatalf("timed-out wait = %v", err)
	}
}

// Stored history may already include events newer than the head read at the
// start of the request; it is current, not stale.
func TestSequenceReceiptAheadOfHeadIsCurrent(t *testing.T) {
	s, p, store, _ := readFixture(t)
	p.head.Version = "seq:10"
	if err := store.SetSourceState(t.Context(), "source", "read-ready/s", "seq:11"); err != nil {
		t.Fatal(err)
	}
	s.Await = func(context.Context, string, string, string) bool {
		t.Error("waited for history that was already current")
		return false
	}
	if _, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0); err != nil {
		t.Fatal(err)
	}
	for receipt, head := range map[string]string{"seq:9": "seq:10", "seq:none": "seq:1", "v2": "v1"} {
		if covers(receipt, head) {
			t.Errorf("%s covered %s", receipt, head)
		}
	}
	for receipt, head := range map[string]string{"seq:10": "seq:10", "seq:3": "seq:none", "v1": "v1"} {
		if !covers(receipt, head) {
			t.Errorf("%s did not cover %s", receipt, head)
		}
	}
}

func TestConversationPurgeRetentionAndCancellation(t *testing.T) {
	s, p, store, batch := readFixture(t)
	batch.ExpectedRevision = 1
	batch.Session.SourceDeleted = true
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	p.head.Session = nil
	p.headErr = platforms.ErrNotFound
	d, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0)
	if err != nil || d.TotalMessages != 2 || p.nativeCalls != 0 {
		t.Fatal("lost retained source-deleted history", d, err)
	}
	p.headErr = nil
	p.head.Session = &db.Session{ID: "s", Status: db.StatusWaiting}
	p.head.Version = "v2"
	if _, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatal("restored source used stale retained snapshot", err)
	}
	if err := store.Purge(t.Context(), batch.Session.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal("purged history resurfaced", err)
	}
	ctx, cancel := context.WithCancel(platforms.WithConversationReader(t.Context(), s))
	cancel()
	if _, err := p.Session(ctx, "s", 30, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
}

func TestInterruptedHistoryAndConcurrentResumeKeepLifecycleAuthority(t *testing.T) {
	s, p, _, _ := readFixture(t)
	p.head.Session.Status = db.StatusInterrupted
	d, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0)
	if err != nil || p.nativeCalls != 0 || d.Session.Status != db.StatusInterrupted {
		t.Fatal("interrupted history not readable", d, err)
	}
	p.headCalls = 0
	p.onHead = func(n int, h *platforms.ConversationHead) {
		if n == 2 {
			copy := *h.Session
			copy.Status = db.StatusBusy
			h.Session = &copy
		}
	}
	if _, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0); err != nil || p.nativeCalls != 1 {
		t.Fatal("resumed turn did not retain platform reads", err, p.nativeCalls)
	}
}

func TestConversationTreeDoesNotTrustMismatchedMetadata(t *testing.T) {
	s, p, store, child := readFixture(t)
	child.Session.SessionID = "child"
	child.Session.ParentID = "s"
	if _, err := store.Commit(t.Context(), child); err != nil {
		t.Fatal(err)
	}
	d, _, err := s.ReadConversation(t.Context(), p, "s", 30, 0)
	if err != nil || len(d.SessionTree) != 2 {
		t.Fatal(d, err)
	}
	if d.SessionTree[0].ID != "child" {
		t.Fatal("mismatched platform metadata replaced a tree identity", d.SessionTree)
	}
	p.headCalls = 0
	p.onHead = func(n int, h *platforms.ConversationHead) {
		if n == 3 {
			copy := *h.Session
			copy.ID = "child"
			copy.Status = db.StatusBusy
			h.Session = &copy
		}
	}
	d, _, err = s.ReadConversation(t.Context(), p, "s", 30, 0)
	if err != nil || d.SessionTree[0].Status != db.StatusBusy {
		t.Fatal("tree lost current child lifecycle", d, err)
	}
}

func TestLiveRuntimeBeforeFirstPersistenceIsNotAHistoryFallback(t *testing.T) {
	s, p, _, _ := readFixture(t)
	s.Store = nil
	p.head.LiveOnly = true
	p.head.Version = ""
	if _, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0); err != nil || p.nativeCalls != 1 {
		t.Fatal("live-only runtime was not served by its platform", err)
	}
	p.head.LiveOnly = false
	if _, err := p.Session(platforms.WithConversationReader(t.Context(), s), "s", 30, 0); !errors.Is(err, platforms.ErrHistoryUnavailable) || p.nativeCalls != 1 {
		t.Fatal("missing persisted history selected a platform fallback", err)
	}
}
