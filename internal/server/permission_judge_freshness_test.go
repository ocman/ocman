package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/analytics"
	"github.com/ocman/ocman/internal/conversationingest"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type permissionJudgeHistory struct {
	mu       sync.Mutex
	messages []string
	gate     <-chan struct{}
	err      error
	started  chan struct{}
}

func (*permissionJudgeHistory) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "test", SourceKey: "judge-history"}, nil
}

func (*permissionJudgeHistory) HistoryInventory(context.Context) ([]db.HistoryEntry, error) {
	return []db.HistoryEntry{{ID: "session", Version: "constant"}}, nil
}

func (p *permissionJudgeHistory) set(messages []string, gate <-chan struct{}, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages, p.gate, p.err = messages, gate, err
}

func (p *permissionJudgeHistory) HistorySnapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	if id != "session" {
		return nil, db.ErrSessionNotFound
	}
	p.mu.Lock()
	messages, gate, err := append([]string(nil), p.messages...), p.gate, p.err
	p.mu.Unlock()
	select {
	case p.started <- struct{}{}:
	default:
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	snapshot := &db.HistorySnapshot{Session: db.Session{ID: id, Directory: "/repo"}, Version: "constant"}
	for i, text := range messages {
		messageID := fmt.Sprintf("m%03d", i)
		data, _ := json.Marshal(map[string]string{"type": "text", "text": text})
		snapshot.Messages = append(snapshot.Messages, db.Message{ID: messageID, SessionID: id, Data: json.RawMessage(`{"role":"user"}`)})
		snapshot.Parts = append(snapshot.Parts, db.Part{ID: "p" + messageID, SessionID: id, MessageID: messageID, Data: data})
	}
	return snapshot, nil
}

func permissionJudgeFreshnessFixture(t *testing.T, messages []string) (*Server, *permissionJudgeHistory) {
	t.Helper()
	store := statev2test.Open(t)
	source := &permissionJudgeHistory{messages: messages, started: make(chan struct{}, 1)}
	ingest := conversationingest.NewModule(conversationingest.Deps{Store: store, OwnerID: "owner", Sources: []conversationingest.Source{{AdapterID: "test", Reader: source}}, Debounce: time.Millisecond, PollInterval: time.Hour})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	if err := ingest.Start(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { defer close(done); ingest.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	seed, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if err := ingest.RefreshStored(seed, "test", "session"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-source.started:
	default:
	}
	return &Server{registry: platforms.NewRegistry(), historyOwner: "owner", historyAnalytics: &analytics.HistoryAnalytics{Store: store}, historyIngest: ingest}, source
}

func TestPermissionJudgeWaitsForNewRestrictionBeforeEachRead(t *testing.T) {
	s, source := permissionJudgeFreshnessFixture(t, []string{"Push origin topic."})
	first, err := s.permissionJudgeUserMessages(t.Context(), "test", "session")
	if err != nil || len(first) != 1 {
		t.Fatalf("initial consent: %v %v", first, err)
	}
	select {
	case <-source.started:
	default:
	}
	release := make(chan struct{})
	source.set([]string{"Push origin topic.", "Do not push until I review."}, release, nil)
	type result struct {
		messages []string
		err      error
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() {
		messages, err := s.permissionJudgeUserMessages(ctx, "test", "session")
		done <- result{messages, err}
	}()
	select {
	case <-source.started:
	case got := <-done:
		t.Fatalf("returned stale context before validation: %+v", got)
	case <-ctx.Done():
		t.Fatal("never requested session-specific validation")
	}
	select {
	case got := <-done:
		t.Fatalf("did not wait for validation: %+v", got)
	default:
	}
	close(release)
	got := <-done
	if got.err != nil || len(got.messages) != 2 || got.messages[1] != "Do not push until I review." {
		t.Fatalf("restriction omitted: %+v", got)
	}
}

func TestPermissionJudgeFailsClosedWhenFreshnessCannotBeEstablished(t *testing.T) {
	s, source := permissionJudgeFreshnessFixture(t, []string{"Push origin topic."})
	source.set([]string{"Push origin topic."}, nil, errors.New("source unavailable"))
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if messages, err := s.permissionJudgeUserMessages(ctx, "test", "session"); err == nil || len(messages) != 0 {
		t.Fatalf("used stale consent after failed refresh: %v %v", messages, err)
	}
}
