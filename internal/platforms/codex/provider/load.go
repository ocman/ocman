package codexprovider

import (
	"context"
	"errors"
	"slices"

	"github.com/NoUseFreak/ocman/internal/platforms"
	codexappserver "github.com/NoUseFreak/ocman/internal/platforms/codex/appserver"
)

// load serializes resumes across sends, renames and skills. A rejected resume
// may still name a writer in our own app-server after a lost response.
func (b *backend) load(ctx context.Context, c *codexappserver.Client, gen uint64, id string) error {
	b.mu.Lock()
	s := b.sessionLocked(id)
	b.mu.Unlock()
	s.loadMu.Lock()
	defer s.loadMu.Unlock()
	b.mu.Lock()
	loaded := b.gen == gen && b.client == c && b.loaded[id]
	b.mu.Unlock()
	if loaded {
		return nil
	}
	resp, err := c.ResumeThread(ctx, codexappserver.ThreadResumeParams{ThreadID: id, ExcludeTurns: true})
	if err != nil {
		if !activeWriterError(err) {
			return threadLoadError(err)
		}
		th, probeErr := locallyLoadedThread(ctx, c, id)
		if probeErr != nil || th == nil {
			return threadLoadError(err)
		}
		if th.Status.Type != "idle" {
			return &platforms.BusyError{Message: "This Codex thread is still active. Wait for it to finish and retry.", Cause: err}
		}
		resp = &codexappserver.ThreadResponse{Cwd: th.Cwd, ModelProvider: th.ModelProvider, Model: th.Model}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.gen != gen || b.client != c || b.loaded == nil {
		return errors.New("codex app-server exited while resuming thread")
	}
	b.loaded[id] = true
	s.dir, s.provider, s.model = resp.Cwd, resp.ModelProvider, resp.Model
	return nil
}

func locallyLoadedThread(ctx context.Context, c *codexappserver.Client, id string) (*codexappserver.Thread, error) {
	ids, err := c.ListLoadedThreads(ctx)
	if err != nil || !slices.Contains(ids, id) {
		return nil, err
	}
	th, err := c.ReadThread(ctx, id, false)
	if err == nil && th.ID != id {
		return nil, errors.New("codex returned a different thread while checking ownership")
	}
	return th, err
}
