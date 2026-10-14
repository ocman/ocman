package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestReloadAdmissionCoversTurnMutationsButNotPromptReplies(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.ID(): p}}, Hooks{
		BeginTurn: func(context.Context, platforms.ID) (func(), error) { return nil, platforms.ErrBusy },
	})
	for name, run := range map[string]func() error{
		"create": func() error {
			_, err := svc.Create(t.Context(), "opencode", platforms.CreateSessionRequest{Directory: "/repo"})
			return err
		},
		"send": func() error {
			return svc.SendMessage(t.Context(), "opencode", platforms.SendMessageRequest{SessionID: "s", Message: "hello"})
		},
		"command": func() error {
			return svc.ExecuteCommand(t.Context(), "opencode", platforms.ExecuteCommandRequest{SessionID: "s", Command: "test"})
		},
		"shell": func() error {
			return svc.RunShell(t.Context(), "opencode", platforms.RunShellRequest{SessionID: "s", Command: "true"})
		},
		"compact": func() error {
			return svc.Compact(t.Context(), "opencode", platforms.CompactRequest{SessionID: "s", ProviderID: "p", ModelID: "m"})
		},
		"fork": func() error {
			_, err := svc.Fork(t.Context(), "opencode", platforms.ForkSessionRequest{SessionID: "s"})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); !errors.Is(err, platforms.ErrBusy) {
				t.Fatal(err)
			}
		})
	}
	if err := svc.RespondPermission(t.Context(), "opencode", platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p", Reply: "once"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Abort(t.Context(), "opencode", platforms.AbortRequest{SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	finished := 0
	svc.hooks.BeginTurn = func(context.Context, platforms.ID) (func(), error) { return func() { finished++ }, nil }
	if err := svc.RunShell(t.Context(), "opencode", platforms.RunShellRequest{SessionID: "s", Command: "true"}); err != nil || finished != 1 {
		t.Fatalf("lease: %d %v", finished, err)
	}
}
