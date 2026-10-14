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
	if req.Message == "" && len(req.Images) == 0 {
		return validation("message or images required")
	}
	p, err := s.resolve(ctx, req.SessionID, platformID)
	if err != nil {
		return err
	}
	if req.Model, err = s.sessionModel(ctx, p, req.SessionID, req.Model); err != nil {
		return err
	}
	return s.deliverFirstPrompt(ctx, p, req.SessionID, req.Message, func() error { return p.SendMessage(ctx, req) })
}

func (s *Service) deliverFirstPrompt(ctx context.Context, p platforms.Platform, id, prompt string, send func() error) error {
	if s.hooks.FirstPromptAdded == nil {
		return send()
	}
	value, _ := s.firstPrompts.LoadOrStore(firstPromptKey{p.ID(), id}, &firstPrompt{})
	f := value.(*firstPrompt)
	f.mu.Lock()
	if !f.known {
		// One read after restart; ordinary follow-ups never read the transcript.
		detail, err := p.Session(ctx, id, 1, 0)
		if err != nil || detail == nil || detail.Session == nil {
			err = send()
			if err == nil {
				f.known, f.done = true, true
			}
			f.mu.Unlock()
			return err
		}
		f.known, f.done = true, detail.TotalMessages != 0 || len(detail.Messages) != 0
		f.directory, f.title = detail.Session.Directory, detail.Session.Title
	}
	if f.done {
		f.mu.Unlock()
		return send()
	}
	err := send()
	if err != nil {
		f.mu.Unlock()
		return err
	}
	f.done = true
	e := module.FirstPromptAdded{Platform: string(p.ID()), SessionID: id, Directory: f.directory, Prompt: prompt, HasCustomTitle: f.title != ""}
	f.mu.Unlock()
	s.hooks.FirstPromptAdded(context.WithoutCancel(ctx), e)
	return nil
}
