package sessionsvc

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Defaults for the two global fallthrough thresholds.
const (
	DefaultPatience         = 5 * time.Minute
	DefaultCooldownFallback = 15 * time.Minute
)

// cooldowns maps provider → the moment its cooldown expires. In-memory
// only: a restart forgets it, costing one wasted failure. Expired
// entries simply stop matching; nothing clears them.
type cooldowns struct {
	mu    sync.Mutex
	until map[string]time.Time
	now   func() time.Time // test seam; nil = time.Now
}

func (c *cooldowns) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *cooldowns) cooled(provider string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clock().Before(c.until[provider])
}

// RecordCooldown cools provider for d, or for the fallback when d <= 0
// (no reset time known). Every duration is floored at the patience
// threshold: that floor is what bounds the fallthrough chain to one
// attempt per model, with no counter and no per-session state.
func (s *Service) RecordCooldown(ctx context.Context, provider string, d time.Duration) {
	patience, fallback := DefaultPatience, DefaultCooldownFallback
	if s.hooks.CooldownTimes != nil {
		patience, fallback = s.hooks.CooldownTimes(ctx)
	}
	if d <= 0 {
		d = fallback
	}
	d = max(d, patience)
	c := &s.cooldowns
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.until == nil {
		c.until = map[string]time.Time{}
	}
	c.until[provider] = c.clock().Add(d)
}

// modelProvider is the provider half of a provider/model id.
func modelProvider(model string) string {
	provider, _, _ := strings.Cut(model, "/")
	return provider
}

// selectModel resolves the model a prompt or command is sent with:
//  1. empty model → the project's first configured model;
//  2. cooled-down provider → the first listed model whose provider is
//     not, overriding even an explicit pick, unless the project's
//     fallthrough is off. With nothing usable the model is kept so the
//     request fails visibly.
//
// Soft-fails: a lookup error never blocks the prompt.
func (s *Service) selectModel(ctx context.Context, p platforms.Platform, sessionID, model string) string {
	if s.hooks.ProjectModels == nil {
		return model
	}
	dir, ok := s.sessionDirs.Load(sessionID)
	if !ok {
		detail, err := p.Session(ctx, sessionID, 1, 0)
		if err != nil || detail == nil || detail.Session == nil || detail.Session.Directory == "" {
			return model
		}
		dir = detail.Session.Directory
		// ponytail: unbounded per-session cache, one string per prompted
		// session; add eviction if a process ever outlives millions.
		s.sessionDirs.Store(sessionID, dir)
	}
	models, off := s.hooks.ProjectModels(ctx, dir.(string))
	if model == "" && len(models) > 0 {
		model = models[0]
	}
	if off || model == "" || !s.cooldowns.cooled(modelProvider(model)) {
		return model
	}
	for _, m := range models {
		if !s.cooldowns.cooled(modelProvider(m)) {
			return m
		}
	}
	return model
}
