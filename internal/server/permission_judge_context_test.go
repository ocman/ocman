package server

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestPermissionJudgeKeepsEarlierUserBoundaries(t *testing.T) {
	s := withReadyTestHistory(t, &Server{registry: platforms.NewRegistry()})
	history := s.historyAnalytics.Store
	batch := statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "fixture", SessionID: "session"}, Directory: "/repo"}}
	add := func(i int) {
		id := fmt.Sprintf("m%03d", i)
		text := "continue"
		if i == 0 {
			text = "Do not push until I review."
		}
		batch.Messages = append(batch.Messages, statev2.Message{ID: id, Role: "user", Position: int64(i)})
		batch.Parts = append(batch.Parts, statev2.Part{ID: "p" + id, MessageID: id, Kind: "text", Text: text})
	}
	for i := range 8 {
		add(i)
	}
	if _, err := history.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	deps := s.autoapproveEngineDeps()
	messages, err := deps.JudgeUserMessages(t.Context(), "test", "session")
	if err != nil || len(messages) != 8 || messages[0] != "Do not push until I review." {
		t.Fatalf("%v %v", messages, err)
	}
	for i := 8; i < 100; i++ {
		add(i)
	}
	batch.ExpectedRevision = 1
	if _, err := history.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.JudgeUserMessages(t.Context(), "test", "session"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("truncated context: %v", err)
	}
	if _, err := deps.JudgeUserMessages(t.Context(), "r-remote:test", "session"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("remote context: %v", err)
	}
	if _, err := deps.JudgeUserMessages(t.Context(), "test", "missing"); err == nil {
		t.Fatal("missing session accepted")
	}
	if _, err := (&Server{}).permissionJudgeUserMessages(t.Context(), "test", "session"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatal(err)
	}
}
