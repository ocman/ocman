package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

type readNative struct {
	*nativePlatform
	err  error
	seen string
}

func (*readNative) Available(context.Context) bool { return true }
func (*readNative) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{AutoApprove: true, FileChanges: true, SessionInfo: true}
}
func (p *readNative) SessionLifecycle(_ context.Context, id string) (*platforms.SessionLifecycle, error) {
	p.seen = id
	return &platforms.SessionLifecycle{LatestMessageID: "recent"}, p.err
}
func (p *readNative) ListPermissions(_ context.Context, id string) ([]platforms.LivePrompt, error) {
	p.seen = id
	return []platforms.LivePrompt{{"sessionID": id, "id": "permission"}}, p.err
}
func (p *readNative) ListQuestions(_ context.Context, id string) ([]platforms.LivePrompt, error) {
	p.seen = id
	return []platforms.LivePrompt{{"sessionID": id, "id": "question"}}, p.err
}

type notificationNative struct {
	*readNative
	prompts []platforms.LivePrompt
}

func (p *notificationNative) ReplyOutcomes(_ context.Context, id string) bool {
	p.seen = id
	return false
}
func (p *notificationNative) NotificationPrompts(_ context.Context, id string) ([]platforms.LivePrompt, []platforms.LivePrompt, error) {
	p.seen = id
	return p.prompts, p.prompts, p.err
}

// Persisted reads come from statev2; only live prompts, reply outcomes and the
// lifecycle probe reach the integration, with canonical keys on both sides.
func TestCanonicalLiveContractsKeepNativeKeysInsideIntegration(t *testing.T) {
	a, p, _ := fixture(t)
	publish(t, a, p.rows...)
	native := &readNative{nativePlatform: p}
	a.native = native
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	id := row.ID
	if !a.Available(t.Context()) || !a.Owns(t.Context(), id) || a.Owns(t.Context(), "native") {
		t.Fatal("canonical ownership contract changed")
	}
	if caps := a.Capabilities(); !caps.AutoApprove || caps.FileChanges || caps.SessionInfo {
		t.Fatalf("panels without a stored projector stayed advertised: %+v", caps)
	}
	if summary, err := a.SessionSummary(t.Context(), id); err != nil || summary.ID != id {
		t.Fatalf("summary identity: %+v %v", summary, err)
	}
	for name, read := range map[string]func() error{
		"changes": func() error { _, err := a.SessionChanges(t.Context(), id); return err },
		"info":    func() error { _, err := a.SessionInfo(t.Context(), id); return err },
	} {
		if err := read(); !errors.Is(err, platforms.ErrUnsupported) {
			t.Errorf("%s without a stored projector: %v", name, err)
		}
	}
	if lifecycle, err := a.SessionLifecycle(t.Context(), id); err != nil || lifecycle.LatestMessageID != "recent" || native.seen != "native" {
		t.Fatalf("lifecycle probe: %+v %v", lifecycle, err)
	}
	permissions, questions, err := a.NotificationPrompts(t.Context(), id)
	if err != nil || permissions[0]["sessionID"] != id || questions[0]["sessionID"] != id || permissions[0]["id"] != "permission" || questions[0]["id"] != "question" {
		t.Fatalf("fallback prompts changed: %+v %+v %v", permissions, questions, err)
	}
	if !a.ReplyOutcomes(t.Context(), id) || a.ReplyOutcomes(t.Context(), "native") {
		t.Fatal("fallback reply outcome contract changed")
	}
	reporter := &notificationNative{readNative: native, prompts: []platforms.LivePrompt{{"sessionId": "native", "id": "request"}}}
	a.native = reporter
	if a.ReplyOutcomes(t.Context(), id) || native.seen != "native" {
		t.Fatal("native reporter got a canonical execution key")
	}
	permissions, questions, err = a.NotificationPrompts(t.Context(), id)
	if err != nil || permissions[0]["sessionId"] != id || questions[0]["sessionId"] != id || reporter.prompts[0]["sessionId"] != "native" {
		t.Fatalf("notification cache mutated: %+v %+v %v", permissions, questions, err)
	}
	reporter.prompts = []platforms.LivePrompt{{"sessionID": "unknown"}}
	if _, _, err := a.NotificationPrompts(t.Context(), id); err == nil {
		t.Fatal("unattributed notification prompt escaped")
	}
	native.err = platforms.ErrPlatformUnreachable
	for name, read := range map[string]func() error{
		"lifecycle":     func() error { _, err := a.SessionLifecycle(t.Context(), id); return err },
		"notifications": func() error { _, _, err := a.NotificationPrompts(t.Context(), id); return err },
	} {
		if err := read(); !errors.Is(err, native.err) {
			t.Errorf("%s lost owner error: %v", name, err)
		}
	}
	p.source = "changed"
	for name, read := range map[string]func() error{
		"lifecycle":     func() error { _, err := a.SessionLifecycle(t.Context(), id); return err },
		"notifications": func() error { _, _, err := a.NotificationPrompts(t.Context(), id); return err },
	} {
		if err := read(); !errors.Is(err, platforms.ErrHistoryUnavailable) {
			t.Errorf("%s crossed physical source: %v", name, err)
		}
	}
	// Retained history stays browsable after the source changes.
	if summary, err := a.SessionSummary(t.Context(), id); err != nil || summary.ID != id || summary.LiveConnection {
		t.Fatalf("retained summary: %+v %v", summary, err)
	}
}

func TestCanonicalOptionalReadsDoNotInventUnsupportedNativeCapabilities(t *testing.T) {
	a, p, _ := fixture(t)
	publish(t, a, p.rows...)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	for name, read := range map[string]func() error{
		"refresh":   func() error { _, err := a.RefreshPermissions(t.Context(), row.ID); return err },
		"lifecycle": func() error { _, err := a.SessionLifecycle(t.Context(), row.ID); return err },
	} {
		if err := read(); !errors.Is(err, platforms.ErrUnsupported) {
			t.Errorf("%s invented support: %v", name, err)
		}
	}
	for name, read := range map[string]func() error{
		"usage":   func() error { _, err := a.SessionUsage(t.Context(), "native"); return err },
		"refresh": func() error { _, err := a.RefreshPermissions(t.Context(), "native"); return err },
	} {
		if err := read(); !errors.Is(err, sessioncatalog.ErrInvalid) {
			t.Errorf("%s accepted native caller: %v", name, err)
		}
	}
}
