package server

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type conversationLifecyclePlatform struct {
	fakePlatform
	lifecycle    platforms.SessionLifecycle
	lifecycleErr error
	reads        int
}

func (f *conversationLifecyclePlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	return &f.lifecycle, f.lifecycleErr
}

func TestConversationReceiptPreflight(t *testing.T) {
	d, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	key := state.PluginConversationKey{PluginID: "plugin", AccountID: "account", ThreadID: "thread"}
	if _, err := d.AppendPluginConversationReply(t.Context(), key, "session:recorded", "answer"); err != nil {
		t.Fatal(err)
	}
	s := &Server{stateDB: d}
	for _, tc := range []struct {
		name          string
		status        db.SessionStatus
		id, role      string
		err           error
		skip, wantErr bool
	}{
		{name: "recorded", status: db.StatusDone, id: "recorded", role: "assistant", skip: true},
		{name: "new reply", status: db.StatusDone, id: "new", role: "assistant"},
		{name: "busy", status: db.StatusBusy, skip: true},
		{name: "error notice still needed", status: db.StatusError, id: "recorded", role: "assistant"},
		{name: "latest user", status: db.StatusDone, id: "recorded", role: "user"},
		{name: "no message", status: db.StatusDone, role: "assistant"},
		{name: "old remote fallback", err: platforms.ErrUnsupported},
		{name: "read failed", err: errors.New("read failed"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &conversationLifecyclePlatform{lifecycle: platforms.SessionLifecycle{Status: tc.status, LatestMessageID: tc.id, LatestMessageRole: tc.role}, lifecycleErr: tc.err}
			skip, err := s.conversationReplyAlreadyRecorded(t.Context(), p, key, "session")
			if skip != tc.skip || (err != nil) != tc.wantErr {
				t.Fatalf("preflight = %v, %v", skip, err)
			}
		})
	}
	if skip, err := s.conversationReplyAlreadyRecorded(t.Context(), &fakePlatform{}, key, "session"); skip || err != nil {
		t.Fatalf("fallback = %v, %v", skip, err)
	}
}

type factoryIdleNotifier struct {
	factoryService
	platform, session string
}

func (f *factoryIdleNotifier) NotifySessionIdle(platform, session string) {
	f.platform, f.session = platform, session
}

func TestSessionIdleNotifiesFactoryWithOwner(t *testing.T) {
	f := &factoryIdleNotifier{}
	s := &Server{factory: f}
	s.onSessionIdle("r-owner:opencode", "session")
	if f.platform != "r-owner:opencode" || f.session != "session" {
		t.Fatalf("notified %q/%q", f.platform, f.session)
	}
}

func (f *conversationLifecyclePlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	f.reads++
	return nil, platforms.ErrNotFound
}

func TestConversationRecordedReplySkipsTranscript(t *testing.T) {
	d, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	key := state.PluginConversationKey{PluginID: "plugin", AccountID: "account", ThreadID: "thread"}
	if _, _, err := d.LinkPluginConversation(t.Context(), key, state.PluginConversationSession{PlatformID: "fake", SessionID: "session"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AppendPluginConversationReply(t.Context(), key, "session:message", "answer"); err != nil {
		t.Fatal(err)
	}
	p := &conversationLifecyclePlatform{fakePlatform: fakePlatform{id: "fake"}, lifecycle: platforms.SessionLifecycle{Status: db.StatusDone, LatestMessageRole: "assistant", LatestMessageID: "message"}}
	registry := platforms.NewRegistry()
	registry.Register(p)
	s := &Server{stateDB: d, registry: registry}
	s.replyToConversation(t.Context(), "fake", "session")
	if p.reads != 0 {
		t.Fatalf("already recorded reply fetched the transcript %d times", p.reads)
	}
}
