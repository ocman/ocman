package autoapprove

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

type noNativeIntentPlatform struct {
	*fakePlatform
	t *testing.T
}

func (p noNativeIntentPlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	p.t.Fatal("judge read native transcript")
	return nil, nil
}

func TestJudgeIntentUsesStoredReaderAndNeverNativeFallback(t *testing.T) {
	p := noNativeIntentPlatform{&fakePlatform{id: "test"}, t}
	s := NewService(Deps{RecentUserMessages: func(_ context.Context, platform platforms.ID, id string) ([]string, error) {
		if platform != "test" || id != "canonical" {
			t.Fatalf("wrong stored scope: %s %s", platform, id)
		}
		return []string{"stored intent", strings.Repeat("x", 301)}, nil
	}})
	s.judge.openCodePort = func(string) string { t.Fatal("judge intent discovered native instance"); return "" }
	sections := s.resolveJudgeSections(t.Context(), p, "/repo", "canonical")
	if len(sections) != 1 || !strings.Contains(sections[0].Content, "stored intent") || strings.Contains(sections[0].Content, strings.Repeat("x", 301)) {
		t.Fatalf("%+v", sections)
	}
	s.deps.RecentUserMessages = func(context.Context, platforms.ID, string) ([]string, error) { return nil, errors.New("not ingested") }
	if got := s.resolveJudgeSections(t.Context(), p, "/repo", "canonical"); len(got) != 0 {
		t.Fatal("unavailable history fabricated intent")
	}
	s.deps.RecentUserMessages = nil
	if got := s.resolveJudgeSections(t.Context(), p, "/repo", "canonical"); len(got) != 0 {
		t.Fatal(got)
	}
}
