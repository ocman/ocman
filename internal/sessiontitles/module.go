// Package sessiontitles names the first prompt in platforms that opt into
// isolated title helpers. It never sends a command into the user's session.
package sessiontitles

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
	log "github.com/sirupsen/logrus"
)

type Deps struct {
	Lookup  func(string) (platforms.Platform, bool)
	Rename  func(context.Context, string, platforms.RenameSessionRequest) error
	Changed func(string, string)
}

type Module struct {
	deps Deps
	jobs chan module.FirstPromptAdded
}

func NewModule(deps Deps) *Module {
	return &Module{deps: deps, jobs: make(chan module.FirstPromptAdded, 32)}
}
func (*Module) Name() string         { return "session-titles" }
func (*Module) Routes(module.Router) {}

func (m *Module) Subscribe(bus *module.Bus) {
	bus.OnFirstPromptAdded(func(_ context.Context, e module.FirstPromptAdded) {
		if e.HasCustomTitle || e.Directory == "" || strings.TrimSpace(e.Prompt) == "" {
			return
		}
		p, ok := m.deps.Lookup(e.Platform)
		if !ok || !p.Capabilities().AutoTitle {
			return
		}
		if _, ok := p.(platforms.SessionTitleNamer); !ok {
			return
		}
		select {
		case m.jobs <- e:
		default:
			log.WithField("sessionID", e.SessionID).Warn("session title queue full; keeping prompt title")
		}
	})
}

func (m *Module) Run(ctx context.Context) {
	// Two workers match Claude's separate helper budget, never its turn slots.
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case e := <-m.jobs:
					if err := m.name(ctx, e); err != nil && ctx.Err() == nil {
						log.WithError(err).WithField("sessionID", e.SessionID).Debug("session title generation failed; keeping prompt title")
					}
				}
			}
		})
	}
	workers.Wait()
}

func (m *Module) name(ctx context.Context, e module.FirstPromptAdded) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, ok := m.deps.Lookup(e.Platform)
	if !ok {
		return platforms.ErrNotFound
	}
	namer, ok := p.(platforms.SessionTitleNamer)
	if !ok {
		return platforms.ErrUnsupported
	}
	title, err := namer.GenerateSessionTitle(ctx, e.Directory, e.Prompt)
	if err != nil {
		return err
	}
	if err := m.deps.Rename(ctx, e.Platform, platforms.RenameSessionRequest{SessionID: e.SessionID, Title: title, OnlyIfUntitled: true}); err != nil {
		return err
	}
	if m.deps.Changed != nil {
		m.deps.Changed(e.Platform, e.SessionID)
	}
	return nil
}
