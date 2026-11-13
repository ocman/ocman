package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestPermissionJudgeKeepsEarlierUserBoundaries(t *testing.T) {
	texts := []string{"Do not push until I review."}
	for i := 1; i < 8; i++ {
		texts = append(texts, fmt.Sprintf("continue %d", i))
	}
	s, source := permissionJudgeFreshnessFixture(t, texts)
	deps := s.autoapproveEngineDeps()
	messages, err := deps.JudgeUserMessages(t.Context(), "test", "session")
	if err != nil || len(messages.Messages) != 8 || messages.Messages[0] != "Do not push until I review." {
		t.Fatalf("%v %v", messages, err)
	}
	for i := 8; i < 100; i++ {
		texts = append(texts, fmt.Sprintf("continue %d", i))
	}
	source.set(texts, nil, nil)
	if messages, err := deps.JudgeUserMessages(t.Context(), "test", "session"); err != nil || len(messages.Messages) != 100 || messages.Limited {
		t.Fatalf("exact old message boundary: %d messages, %v", len(messages.Messages), err)
	}
	texts = append(texts, "Still do not push.")
	source.set(texts, nil, nil)
	if messages, err := deps.JudgeUserMessages(t.Context(), "test", "session"); err != nil || len(messages.Messages) != 101 || messages.Messages[0] != texts[0] || messages.Messages[100] != texts[100] || messages.Limited {
		t.Fatalf("complete long history: %v %v", messages, err)
	}
	source.set([]string{strings.Repeat("x", 32_001)}, nil, nil)
	if messages, err := deps.JudgeUserMessages(t.Context(), "test", "session"); err != nil || !messages.Limited || len(messages.Messages) != 1 || len(messages.Messages[0]) != 32_000 {
		t.Fatalf("oversized history: %v %v", messages, err)
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

func TestPermissionJudgeRejectsHistoryWithoutIngestion(t *testing.T) {
	s := withReadyTestHistory(t, &Server{registry: platforms.NewRegistry()})
	_, err := s.historyAnalytics.Store.Commit(t.Context(), statev2.Batch{
		Session:  statev2.Session{Key: statev2.Key{InstanceID: "fixture", SessionID: "session"}, Directory: "/repo"},
		Messages: []statev2.Message{{ID: "m", Role: "user"}},
		Parts:    []statev2.Part{{ID: "p", MessageID: "m", Kind: "text", Text: "Push origin topic."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.historyIngest = nil
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if messages, err := s.permissionJudgeUserMessages(ctx, "test", "session"); err == nil || len(messages.Messages) != 0 {
		t.Fatalf("stale retained consent accepted without ingestion: %v, %v", messages, err)
	}
}
