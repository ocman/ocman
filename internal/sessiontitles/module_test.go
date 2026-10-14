package sessiontitles

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type namingPlatform struct {
	platforms.Platform
	generate func(context.Context, string, string) (string, error)
}

func (*namingPlatform) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{AutoTitle: true}
}
func (p *namingPlatform) GenerateSessionTitle(ctx context.Context, dir, prompt string) (string, error) {
	return p.generate(ctx, dir, prompt)
}

type unsupportedPlatform struct {
	platforms.Platform
	caps platforms.Capabilities
}

func (p unsupportedPlatform) Capabilities() platforms.Capabilities { return p.caps }

func testEvent() module.FirstPromptAdded {
	return module.FirstPromptAdded{Platform: "claudecode", SessionID: "s", Directory: "/repo", Prompt: "fix login redirect"}
}

func TestFirstPromptNamesInBackgroundWithoutSendingToConversation(t *testing.T) {
	renames := make(chan platforms.RenameSessionRequest, 1)
	changed := make(chan string, 1)
	p := &namingPlatform{generate: func(ctx context.Context, dir, prompt string) (string, error) {
		if dir != "/repo" || prompt != "fix login redirect" {
			t.Errorf("helper input = %q, %q", dir, prompt)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("helper has no deadline")
		}
		return "Login redirect loop", nil
	}}
	m := NewModule(Deps{Lookup: func(id string) (platforms.Platform, bool) { return p, id == "claudecode" },
		Rename: func(_ context.Context, id string, req platforms.RenameSessionRequest) error {
			if id != "claudecode" {
				t.Error(id)
			}
			renames <- req
			return nil
		},
		Changed: func(platform, id string) { changed <- platform + ":" + id },
	})
	var bus module.Bus
	m.Subscribe(&bus)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	bus.PublishFirstPromptAdded(t.Context(), testEvent())
	select {
	case req := <-renames:
		if req.SessionID != "s" || req.Title != "Login redirect loop" || !req.OnlyIfUntitled {
			t.Fatal(req)
		}
	case <-time.After(time.Second):
		t.Fatal("helper never renamed the session")
	}
	if got := <-changed; got != "claudecode:s" {
		t.Fatal(got)
	}
}

func TestFirstPromptSkipsCustomTitlesAndUnsupportedPlatforms(t *testing.T) {
	p := &namingPlatform{}
	plain := unsupportedPlatform{caps: platforms.Capabilities{AutoTitle: true}}
	m := NewModule(Deps{Lookup: func(id string) (platforms.Platform, bool) {
		switch id {
		case "claudecode":
			return p, true
		case "remote":
			return plain, true
		case "opencode":
			return unsupportedPlatform{}, true
		default:
			return nil, false
		}
	}})
	var bus module.Bus
	m.Subscribe(&bus)
	for _, e := range []module.FirstPromptAdded{
		{Platform: "claudecode", SessionID: "s", Directory: "/repo", Prompt: "task", HasCustomTitle: true},
		{Platform: "claudecode", SessionID: "s", Prompt: "task"},
		{Platform: "claudecode", SessionID: "s", Directory: "/repo", Prompt: " "},
		{Platform: "missing", Directory: "/repo", Prompt: "task"},
		{Platform: "opencode", Directory: "/repo", Prompt: "task"},
		{Platform: "remote", Directory: "/repo", Prompt: "task"},
	} {
		bus.PublishFirstPromptAdded(t.Context(), e)
	}
	if len(m.jobs) != 0 {
		t.Fatal("an ignored event started naming")
	}
	for range 33 {
		bus.PublishFirstPromptAdded(t.Context(), testEvent())
	}
	if len(m.jobs) != 32 {
		t.Fatal("background queue is unbounded")
	}
}

func TestNamingErrorsLeaveTitleUnchanged(t *testing.T) {
	for _, stage := range []string{"missing", "unsupported", "generate", "rename"} {
		t.Run(stage, func(t *testing.T) {
			errExpected := errors.New("failed")
			p := &namingPlatform{generate: func(context.Context, string, string) (string, error) {
				if stage == "generate" {
					return "", errExpected
				}
				return "Title", nil
			}}
			changed := false
			m := NewModule(Deps{Lookup: func(string) (platforms.Platform, bool) {
				if stage == "unsupported" {
					return unsupportedPlatform{}, true
				}
				return p, stage != "missing"
			},
				Rename: func(context.Context, string, platforms.RenameSessionRequest) error {
					if stage == "rename" {
						return errExpected
					}
					t.Fatal("failure reached rename")
					return nil
				},
				Changed: func(string, string) { changed = true },
			})
			if err := m.name(t.Context(), testEvent()); err == nil || changed {
				t.Fatalf("failure changed title: %v", err)
			}
		})
	}
}

func TestRunJoinsCanceledHelpers(t *testing.T) {
	started := make(chan struct{})
	p := &namingPlatform{generate: func(ctx context.Context, _, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}}
	m := NewModule(Deps{Lookup: func(string) (platforms.Platform, bool) { return p, true }})
	m.jobs <- testEvent()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join the helper")
	}
	if m.Name() != "session-titles" {
		t.Fatal(m.Name())
	}
	m.Routes(nil)
}
