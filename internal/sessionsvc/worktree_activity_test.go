package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestWorktreeActivityCoversMutationsAndPreparedDelivery(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	active, admitted, released := false, 0, 0
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.ID(): p}}, Hooks{
		BeginActivity: func(ctx context.Context, owner platforms.Platform, id string) (context.Context, func(), error) {
			if owner != p || id != "s" {
				t.Fatalf("wrong identity: %v %s", owner, id)
			}
			active = true
			admitted++
			return ctx, func() { active = false; released++ }, nil
		},
	})
	for name, run := range map[string]func() error{
		"send": func() error {
			return svc.SendMessage(t.Context(), "opencode", platforms.SendMessageRequest{SessionID: "s", Message: "hi"})
		},
		"rename": func() error {
			return svc.Rename(t.Context(), "opencode", platforms.RenameSessionRequest{SessionID: "s", Title: "title"})
		},
		"abort": func() error { return svc.Abort(t.Context(), "opencode", platforms.AbortRequest{SessionID: "s"}) },
		"command": func() error {
			return svc.ExecuteCommand(t.Context(), "opencode", platforms.ExecuteCommandRequest{SessionID: "s", Command: "test"})
		},
		"shell": func() error {
			return svc.RunShell(t.Context(), "opencode", platforms.RunShellRequest{SessionID: "s", Command: "true"})
		},
		"permission": func() error {
			return svc.RespondPermission(t.Context(), "opencode", platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p", Reply: "once"})
		},
		"question": func() error {
			return svc.RespondQuestion(t.Context(), "opencode", platforms.RespondQuestionRequest{SessionID: "s", RequestID: "q"})
		},
		"reject question": func() error {
			return svc.RejectQuestion(t.Context(), "opencode", platforms.RejectQuestionRequest{SessionID: "s", RequestID: "q"})
		},
		"move": func() error {
			return svc.Move(t.Context(), "opencode", platforms.MoveSessionRequest{SessionID: "s", Directory: "/other"})
		},
		"compact": func() error {
			return svc.Compact(t.Context(), "opencode", platforms.CompactRequest{SessionID: "s", ProviderID: "p", ModelID: "m"})
		},
		"dispose unsupported": func() error {
			return svc.Dispose(t.Context(), "opencode", platforms.DisposeSessionRequest{SessionID: "s"})
		},
		"revert unsupported": func() error {
			return svc.Revert(t.Context(), "opencode", platforms.RevertSessionRequest{SessionID: "s", MessageID: "m"})
		},
		"unrevert unsupported": func() error {
			return svc.Unrevert(t.Context(), "opencode", platforms.UnrevertSessionRequest{SessionID: "s"})
		},
		"background unsupported": func() error {
			return svc.SetBackgroundLifecycle(t.Context(), "opencode", platforms.SetBackgroundLifecycleRequest{SessionID: "s", TaskID: "t", Lifecycle: "task"})
		},
		"permission rules": func() error {
			return svc.SetPermissionRules(t.Context(), "opencode", platforms.SetPermissionRulesRequest{SessionID: "s"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := admitted
			_ = run()
			if admitted != before+1 || admitted != released || active {
				t.Fatalf("lease not scoped to mutation: %d %d", admitted, released)
			}
		})
	}
	before := admitted
	send, err := svc.PrepareSendMessage(t.Context(), "opencode", platforms.SendMessageRequest{SessionID: "s", Message: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if admitted != before {
		t.Fatal("read preparation counted as activity")
	}
	if err := send(t.Context()); err != nil {
		t.Fatal(err)
	}
	svc.hooks.BeginTurn = func(context.Context, platforms.ID) (func(), error) { return nil, platforms.ErrBusy }
	if err := svc.RunShell(t.Context(), "opencode", platforms.RunShellRequest{SessionID: "s", Command: "true"}); !errors.Is(err, platforms.ErrBusy) {
		t.Fatal(err)
	}
	if admitted != released {
		t.Fatal("admission failure leaked activity lease")
	}
	svc.hooks.BeginActivity = func(ctx context.Context, _ platforms.Platform, _ string) (context.Context, func(), error) {
		return ctx, nil, platforms.ErrRemoteOffline
	}
	if err := send(t.Context()); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("delivery bypassed failed activity admission: %v", err)
	}
}

func TestCreateUsesDirectoryActivityFence(t *testing.T) {
	p := &fakePlatform{id: "opencode", available: true}
	called := false
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.ID(): p}}, Hooks{
		BeginDirectory: func(ctx context.Context, owner platforms.Platform, dir string) (context.Context, func(), error) {
			called = true
			if owner != p || dir != "/repo" {
				t.Fatal("wrong owner/directory")
			}
			return ctx, nil, platforms.ErrBusy
		},
	})
	_, err := svc.Create(t.Context(), "opencode", platforms.CreateSessionRequest{Directory: "/repo"})
	if !called || !errors.Is(err, platforms.ErrBusy) {
		t.Fatalf("create bypassed fence: %v", err)
	}
}
