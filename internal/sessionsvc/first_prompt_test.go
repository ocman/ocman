package sessionsvc

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type firstPromptPlatform struct {
	fakePlatform
	mu      sync.Mutex
	reads   int
	detail  *platforms.SessionDetail
	readErr error
}

func (p *firstPromptPlatform) SendMessage(ctx context.Context, req platforms.SendMessageRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fakePlatform.SendMessage(ctx, req)
}
func (p *firstPromptPlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	p.reads++
	return p.detail, p.readErr
}

func TestFirstPromptPublishedOnceAfterAcceptance(t *testing.T) {
	p := &firstPromptPlatform{fakePlatform: fakePlatform{id: "claudecode"}}
	var events []module.FirstPromptAdded
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.id: p}}, Hooks{FirstPromptAdded: func(_ context.Context, e module.FirstPromptAdded) { events = append(events, e) }})
	res, err := svc.Create(t.Context(), string(p.id), platforms.CreateSessionRequest{Directory: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.SendMessage(t.Context(), string(p.id), platforms.SendMessageRequest{SessionID: res.ID, Message: "fix login", Model: "anthropic/haiku"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 1 || events[0].Prompt != "fix login" || events[0].Directory != "/repo" || events[0].HasCustomTitle || p.reads != 0 {
		t.Fatalf("events = %+v, reads = %d", events, p.reads)
	}
}

func TestFirstPromptRetriesFailedSendAndPreservesExplicitTitle(t *testing.T) {
	p := &firstPromptPlatform{fakePlatform: fakePlatform{id: "claudecode", sendErr: errors.New("offline")}}
	var events []module.FirstPromptAdded
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.id: p}}, Hooks{FirstPromptAdded: func(_ context.Context, e module.FirstPromptAdded) { events = append(events, e) }})
	res, _ := svc.Create(t.Context(), string(p.id), platforms.CreateSessionRequest{Directory: "/repo", Title: "Mine"})
	req := platforms.SendMessageRequest{SessionID: res.ID, Message: "task", Model: "anthropic/haiku"}
	if err := svc.SendMessage(t.Context(), string(p.id), req); err == nil || len(events) != 0 {
		t.Fatal("failed send emitted an event")
	}
	p.sendErr = nil
	if err := svc.SendMessage(t.Context(), string(p.id), req); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !events[0].HasCustomTitle {
		t.Fatal(events)
	}
}

func TestFirstPromptRecoveryDoesNotRenameExistingConversation(t *testing.T) {
	for _, total := range []int{0, 3} {
		p := &firstPromptPlatform{fakePlatform: fakePlatform{id: "claudecode"}, detail: &platforms.SessionDetail{Session: &db.Session{ID: "s", Directory: "/repo"}, TotalMessages: total}}
		count := 0
		svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.id: p}}, Hooks{FirstPromptAdded: func(context.Context, module.FirstPromptAdded) { count++ }})
		for i := 0; i < 2; i++ {
			if err := svc.SendMessage(t.Context(), string(p.id), platforms.SendMessageRequest{SessionID: "s", Message: "hi", Model: "anthropic/haiku"}); err != nil {
				t.Fatal(err)
			}
		}
		if count != map[bool]int{true: 1, false: 0}[total == 0] || p.reads != 1 {
			t.Fatalf("total=%d events=%d reads=%d", total, count, p.reads)
		}
	}
}

func TestFirstPromptConcurrentSendsAndOwnerQualification(t *testing.T) {
	local := &firstPromptPlatform{fakePlatform: fakePlatform{id: "claudecode"}}
	remote := &firstPromptPlatform{fakePlatform: fakePlatform{id: "r-box:claudecode"}}
	var mu sync.Mutex
	counts := map[string]int{}
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{local.id: local, remote.id: remote}}, Hooks{FirstPromptAdded: func(_ context.Context, e module.FirstPromptAdded) { mu.Lock(); counts[e.Platform]++; mu.Unlock() }})
	for _, p := range []*firstPromptPlatform{local, remote} {
		svc.rememberFirstPrompt(p, "same-id", "/repo", "")
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			p := local
			if i%2 != 0 {
				p = remote
			}
			if err := svc.SendMessage(t.Context(), string(p.id), platforms.SendMessageRequest{SessionID: "same-id", Message: "task", Model: "anthropic/haiku"}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if counts[string(local.id)] != 1 || counts[string(remote.id)] != 1 {
		t.Fatal(counts)
	}
}

func TestFirstPromptCommandAndUnknownMetadata(t *testing.T) {
	p := &firstPromptPlatform{fakePlatform: fakePlatform{id: "claudecode"}}
	var events []module.FirstPromptAdded
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.id: p}}, Hooks{FirstPromptAdded: func(_ context.Context, e module.FirstPromptAdded) { events = append(events, e) }})
	svc.rememberFirstPrompt(p, "command", "/repo", "")
	if err := svc.ExecuteCommand(t.Context(), string(p.id), platforms.ExecuteCommandRequest{SessionID: "command", Command: "review", Arguments: "login", Model: "anthropic/haiku"}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Prompt != "/review login" {
		t.Fatal(events)
	}
	for i := 0; i < 2; i++ {
		if err := svc.SendMessage(t.Context(), string(p.id), platforms.SendMessageRequest{SessionID: "unknown", Message: "hi", Model: "anthropic/haiku"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 1 || p.reads != 1 {
		t.Fatal("unknown metadata emitted a late event")
	}
}
