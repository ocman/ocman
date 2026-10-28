package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
)

type runtimeNative struct {
	*nativePlatform
	idle              bool
	err               error
	directory, prompt string
}

func (p *runtimeNative) ReloadIdle(context.Context) (bool, error) { return p.idle, p.err }
func (p *runtimeNative) GenerateSessionTitle(_ context.Context, directory, prompt string) (string, error) {
	p.directory, p.prompt = directory, prompt
	return "generated title", p.err
}

func TestCanonicalAdapterKeepsAuthoritativeReloadAndIsolatedTitleHelpers(t *testing.T) {
	a, p, _ := fixture(t)
	if helper, err := a.HelperSessionIdentity(t.Context(), "native"); err != nil || !identity.IsSessionID(helper) {
		t.Fatalf("new helper not adopted before callback: %q %v", helper, err)
	}
	native := &runtimeNative{nativePlatform: p}
	a.native = native
	if idle, err := a.ReloadIdle(t.Context()); err != nil || idle {
		t.Fatalf("authoritative busy state ignored: %v %v", idle, err)
	}
	native.idle = true
	if idle, err := a.ReloadIdle(t.Context()); err != nil || !idle {
		t.Fatalf("idle state lost: %v %v", idle, err)
	}
	if title, err := a.GenerateSessionTitle(t.Context(), "/repo", "opaque prompt"); err != nil || title != "generated title" || native.directory != "/repo" || native.prompt != "opaque prompt" {
		t.Fatalf("title helper not forwarded: %q %v", title, err)
	}
	native.err = platforms.ErrPlatformUnreachable
	if _, err := a.ReloadIdle(t.Context()); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("reload error lost: %v", err)
	}
	if _, err := a.GenerateSessionTitle(t.Context(), "/repo", "prompt"); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("title error lost: %v", err)
	}
	a.native = p
	p.rows = []db.Session{{ID: "unpublished-native", Status: db.StatusBusy}}
	if idle, err := a.ReloadIdle(t.Context()); err != nil || idle {
		t.Fatalf("unpublished busy source allowed reload: %v %v", idle, err)
	}
	p.rows[0].Status = db.StatusDone
	if idle, err := a.ReloadIdle(t.Context()); err != nil || !idle {
		t.Fatalf("settled source blocked reload: %v %v", idle, err)
	}
	if _, err := a.GenerateSessionTitle(t.Context(), "/repo", "prompt"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("invented title helper: %v", err)
	}
	p.source = "changed"
	if _, err := a.ReloadIdle(t.Context()); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("source drift allowed reload: %v", err)
	}
}
