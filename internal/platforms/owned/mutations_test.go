package owned

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

type operationPlatform struct {
	*nativePlatform
	operation, sessionID, messageID string
}

func (p *operationPlatform) record(operation, id string) error {
	p.operation, p.sessionID = operation, id
	return nil
}
func (p *operationPlatform) ExecuteCommand(_ context.Context, r platforms.ExecuteCommandRequest) error {
	return p.record("command", r.SessionID)
}
func (p *operationPlatform) RunShell(_ context.Context, r platforms.RunShellRequest) error {
	return p.record("shell", r.SessionID)
}
func (p *operationPlatform) RespondPermission(_ context.Context, r platforms.RespondPermissionRequest) error {
	return p.record("permission", r.SessionID)
}
func (p *operationPlatform) RespondQuestion(_ context.Context, r platforms.RespondQuestionRequest) error {
	return p.record("question", r.SessionID)
}
func (p *operationPlatform) RejectQuestion(_ context.Context, r platforms.RejectQuestionRequest) error {
	return p.record("reject", r.SessionID)
}
func (p *operationPlatform) Abort(_ context.Context, r platforms.AbortRequest) error {
	return p.record("abort", r.SessionID)
}
func (p *operationPlatform) RenameSession(_ context.Context, r platforms.RenameSessionRequest) error {
	return p.record("rename", r.SessionID)
}
func (p *operationPlatform) PermissionRules(_ context.Context, id string) ([]platforms.PermissionRule, error) {
	return nil, p.record("read rules", id)
}
func (p *operationPlatform) SetPermissionRules(_ context.Context, r platforms.SetPermissionRulesRequest) error {
	return p.record("rules", r.SessionID)
}
func (p *operationPlatform) Compact(_ context.Context, r platforms.CompactRequest) error {
	return p.record("compact", r.SessionID)
}
func (p *operationPlatform) MoveSession(_ context.Context, r platforms.MoveSessionRequest) error {
	return p.record("move", r.SessionID)
}
func (p *operationPlatform) Revert(_ context.Context, r platforms.RevertSessionRequest) error {
	p.messageID = r.MessageID
	return p.record("revert", r.SessionID)
}
func (p *operationPlatform) Unrevert(_ context.Context, r platforms.UnrevertSessionRequest) error {
	return p.record("unrevert", r.SessionID)
}
func (p *operationPlatform) NativeQueued(_ context.Context, id string) ([]platforms.NativeQueuedMessage, error) {
	return nil, p.record("queued", id)
}
func (p *operationPlatform) CancelNativeQueued(_ context.Context, r platforms.CancelNativeQueuedRequest) error {
	return p.record("cancel queued", r.SessionID)
}
func (p *operationPlatform) ListPermissions(_ context.Context, id string) ([]platforms.LivePrompt, error) {
	return nil, p.record("permissions", id)
}
func (p *operationPlatform) ListQuestions(_ context.Context, id string) ([]platforms.LivePrompt, error) {
	return nil, p.record("questions", id)
}

func TestEverySessionOperationTranslatesAtTheNativeBoundary(t *testing.T) {
	operations := []struct {
		name string
		call func(context.Context, *Adapter, string) error
	}{
		{"command", func(ctx context.Context, a *Adapter, id string) error {
			return a.ExecuteCommand(ctx, platforms.ExecuteCommandRequest{SessionID: id})
		}},
		{"shell", func(ctx context.Context, a *Adapter, id string) error {
			return a.RunShell(ctx, platforms.RunShellRequest{SessionID: id})
		}},
		{"permission", func(ctx context.Context, a *Adapter, id string) error {
			return a.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: id})
		}},
		{"question", func(ctx context.Context, a *Adapter, id string) error {
			return a.RespondQuestion(ctx, platforms.RespondQuestionRequest{SessionID: id})
		}},
		{"reject", func(ctx context.Context, a *Adapter, id string) error {
			return a.RejectQuestion(ctx, platforms.RejectQuestionRequest{SessionID: id})
		}},
		{"abort", func(ctx context.Context, a *Adapter, id string) error {
			return a.Abort(ctx, platforms.AbortRequest{SessionID: id})
		}},
		{"rename", func(ctx context.Context, a *Adapter, id string) error {
			return a.RenameSession(ctx, platforms.RenameSessionRequest{SessionID: id})
		}},
		{"read rules", func(ctx context.Context, a *Adapter, id string) error {
			_, err := a.PermissionRules(ctx, id)
			return err
		}},
		{"rules", func(ctx context.Context, a *Adapter, id string) error {
			return a.SetPermissionRules(ctx, platforms.SetPermissionRulesRequest{SessionID: id})
		}},
		{"compact", func(ctx context.Context, a *Adapter, id string) error {
			return a.Compact(ctx, platforms.CompactRequest{SessionID: id})
		}},
		{"move", func(ctx context.Context, a *Adapter, id string) error {
			return a.MoveSession(ctx, platforms.MoveSessionRequest{SessionID: id})
		}},
		{"revert", func(ctx context.Context, a *Adapter, id string) error {
			return a.Revert(ctx, platforms.RevertSessionRequest{SessionID: id, MessageID: "unchanged-message"})
		}},
		{"unrevert", func(ctx context.Context, a *Adapter, id string) error {
			return a.Unrevert(ctx, platforms.UnrevertSessionRequest{SessionID: id})
		}},
		{"queued", func(ctx context.Context, a *Adapter, id string) error { _, err := a.NativeQueued(ctx, id); return err }},
		{"cancel queued", func(ctx context.Context, a *Adapter, id string) error {
			return a.CancelNativeQueued(ctx, platforms.CancelNativeQueuedRequest{SessionID: id})
		}},
		{"permissions", func(ctx context.Context, a *Adapter, id string) error {
			_, err := a.ListPermissions(ctx, id)
			return err
		}},
		{"questions", func(ctx context.Context, a *Adapter, id string) error { _, err := a.ListQuestions(ctx, id); return err }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			a, native, _ := fixture(t)
			publish(t, a, native.rows...)
			p := &operationPlatform{nativePlatform: native}
			a.native = p
			rows, err := a.Sessions(t.Context(), "", 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := operation.call(t.Context(), a, "native"); err == nil || p.operation != "" {
				t.Fatalf("native caller reference reached execution: %s %v", p.operation, err)
			}
			if err := operation.call(t.Context(), a, rows[0].ID); err != nil {
				t.Fatal(err)
			}
			if p.operation != operation.name || p.sessionID != "native" {
				t.Fatalf("wrong native operation: %+v", p)
			}
			if operation.name == "revert" && p.messageID != "unchanged-message" {
				t.Fatal("message ID was translated")
			}
		})
	}
}
