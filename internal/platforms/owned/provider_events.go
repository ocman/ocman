package owned

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

type providerEvents interface {
	SetEventHandler(func(providerprotocol.Event))
	WatchEvents(context.Context) error
}

// Provider event handlers run within the native watcher's joined lifetime.
// Projection is bounded independently because the callback has no context.
func (a *Adapter) SetEventHandler(handler func(providerprotocol.Event)) {
	if source, ok := a.native.(providerEvents); ok {
		source.SetEventHandler(func(event providerprotocol.Event) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			raw, err := json.Marshal(event)
			if err != nil {
				return
			}
			writer := a.eventWriter(ctx, nil)
			writer.observe = a.observeBackground
			frame, err := writer.frame(append([]byte("data: "), raw...))
			if err != nil {
				return
			}
			var projected providerprotocol.Event
			if json.Unmarshal(frame[len("data: "):], &projected) == nil {
				handler(projected)
			}
		})
	}
}

func (a *Adapter) WatchEvents(ctx context.Context) error {
	if source, ok := a.native.(providerEvents); ok {
		return source.WatchEvents(ctx)
	}
	return platforms.ErrUnsupported
}
