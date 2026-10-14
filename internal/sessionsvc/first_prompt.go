package sessionsvc

import (
	"context"
	"sync"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type firstPromptKey struct {
	platform platforms.ID
	session  string
}
type firstPrompt struct {
	mu               sync.Mutex
	known, done      bool
	directory, title string
}

func (s *Service) rememberFirstPrompt(p platforms.Platform, id, directory, title string) {
	if s.hooks.FirstPromptAdded != nil {
		s.firstPrompts.Store(firstPromptKey{p.ID(), id}, &firstPrompt{known: true, directory: directory, title: title})
	}
}

// SendMessage sends a prompt to a session and reports its first accepted input.
func (s *Service) SendMessage(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	send, err := s.PrepareSendMessage(ctx, platformID, req)
	if err != nil {
		return err
	}
	return send(ctx)
}

// PrepareSendMessage finishes owner/model/first-prompt reads before returning
// a mutation-only delivery. Queue admission can then detach its receipt context.
func (s *Service) PrepareSendMessage(ctx context.Context, platformID string, req platforms.SendMessageRequest) (func(context.Context) error, error) {
	if req.Message == "" && len(req.Images) == 0 {
		return nil, validation("message or images required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return nil, err
	}
	if req.Model, err = s.sessionModel(ctx, p, req.SessionID, req.Model); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	complete, err := s.prepareFirstPrompt(ctx, p, req.SessionID, req.Message)
	if err != nil {
		return nil, err
	}
	return func(delivery context.Context) error {
		finish, err := s.admit(delivery, p)
		if err != nil {
			return err
		}
		defer finish()
		return complete(delivery, func() error { return p.SendMessage(delivery, req) })
	}, nil
}

func (s *Service) deliverFirstPrompt(ctx context.Context, p platforms.Platform, id, prompt string, send func() error) error {
	complete, err := s.prepareFirstPrompt(ctx, p, id, prompt)
	if err != nil {
		return err
	}
	return complete(ctx, send)
}

func (s *Service) prepareFirstPrompt(ctx context.Context, p platforms.Platform, id, prompt string) (func(context.Context, func() error) error, error) {
	if s.hooks.FirstPromptAdded == nil {
		return func(_ context.Context, send func() error) error { return send() }, nil
	}
	value, _ := s.firstPrompts.LoadOrStore(firstPromptKey{p.ID(), id}, &firstPrompt{})
	f := value.(*firstPrompt)
	f.mu.Lock()
	if !f.known {
		// One read after restart; ordinary follow-ups never read the transcript.
		detail, err := p.Session(ctx, id, 1, 0)
		if ctx.Err() != nil {
			f.mu.Unlock()
			return nil, ctx.Err()
		}
		if err == nil && detail != nil && detail.Session != nil {
			f.known, f.done = true, detail.TotalMessages != 0 || len(detail.Messages) != 0
			f.directory, f.title = detail.Session.Directory, detail.Session.Title
		}
	}
	f.mu.Unlock()
	return func(delivery context.Context, send func() error) error {
		f.mu.Lock()
		if f.done {
			f.mu.Unlock()
			return send()
		}
		err := send()
		if err != nil {
			f.mu.Unlock()
			return err
		}
		// A failed discovery stays soft-failing, as before: an accepted input
		// settles the unknown first-prompt state without emitting a title event.
		known := f.known
		f.known, f.done = true, true
		e := module.FirstPromptAdded{Platform: string(p.ID()), SessionID: id, Directory: f.directory, Prompt: prompt, HasCustomTitle: f.title != ""}
		f.mu.Unlock()
		if known {
			s.hooks.FirstPromptAdded(context.WithoutCancel(delivery), e)
		}
		return nil
	}, nil
}
