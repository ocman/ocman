package pluginsmod

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/module"
)

// Active tool sessions cannot wait for the global outbox sweep, which can be
// slower than their deadline. Idle events remain the primary path. This bounded
// recovery uses the same lifecycle gate, durable outbox and delivery deduplication
// when an idle edge is missed or arrives before the history projection is ready.
func (m *Module) watchToolSession(lifetime context.Context, b *sessionToolBinding) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			if m.closeSessionToolBinding(b) {
				m.abortToolSession(b)
			}
			return
		case <-b.done:
			return
		case <-ticker.C:
			if m.store == nil || m.deps.Adapter == nil {
				continue
			}
			module.RunWithRecover("plugin-tool-session-reconcile", func() {
				// Completion revokes b.ctx before appending its outbox row. Use
				// the module lifetime so that revocation cannot cancel persistence.
				ctx, cancel := context.WithTimeout(lifetime, 5*time.Second)
				defer cancel()
				m.ReplyToSession(ctx, b.platform, b.session)
			})
		}
	}
}
