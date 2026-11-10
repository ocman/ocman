package platformconnections

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ocman/ocman/internal/platforms"
)

var ErrAlreadyBound = errors.New("platform connection already has an adapter")

// Reference selects exactly one owner-local connection. An empty reference uses
// the explicit saved default. LegacyKind resolves only the retained migration
// mapping, never the first available connection of that kind.
type Reference struct {
	ConnectionID string
	LegacyKind   string
}

// Route keeps the selected connection separate from the adapter's platform kind.
// It is an admission result, not a durable login or readiness grant. Callers must
// resolve again for each operation and persist Connection.ID for future routing.
type Route struct {
	Connection Connection
	Platform   platforms.Platform
}

// Router binds configured adapters by connection ID, allowing multiple adapters
// of one platform kind without rewriting their protocol or physical identities.
// Each router belongs to one owner's Store. Resolve the owner before using it;
// a disconnected remote must never be sent to a local router as fallback.
//
// Adapters are built and owned by the composition layer. Bind neither launches
// nor closes them and cannot prove native credential isolation. Startup and the
// session registry share these immutable bindings for the current composition.
type Router struct {
	store *Store
	mu    sync.RWMutex
	bound map[string]platforms.Platform
	lifecycle connectionLifecycle
}

func NewRouter(store *Store) *Router {
	return &Router{store: store, bound: make(map[string]platforms.Platform)}
}

// Bind records an already configured adapter. It accepts disabled connections
// for startup composition, but Resolve will not admit them. Replacement is
// deliberately refused; a new composition must drain and retire the old router.
func (r *Router) Bind(ctx context.Context, id string, adapter platforms.Platform) error {
	if r.store == nil || adapter == nil {
		return ErrInvalid
	}
	c, err := r.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if c.Removed {
		return ErrUnavailable
	}
	if adapter.ID() != platforms.ID(c.Kind) {
		return fmt.Errorf("%w: adapter kind does not match connection", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.bound[id]; exists {
		return ErrAlreadyBound
	}
	r.bound[id] = adapter
	return nil
}

// Resolve checks persisted intent and live adapter availability every time.
// Missing, disabled, removed and unbound selections never switch to a sibling.
// A display name or bare native session ID is never a routing key.
func (r *Router) Resolve(ctx context.Context, ref Reference) (Route, error) {
	if r.store == nil || (ref.ConnectionID != "" && ref.LegacyKind != "") {
		return Route{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Route{}, err
	}
	var c Connection
	var err error
	switch {
	case ref.ConnectionID != "":
		c, err = r.store.Get(ctx, ref.ConnectionID)
	case ref.LegacyKind != "":
		c, err = r.store.Legacy(ctx, ref.LegacyKind)
	default:
		c, err = r.store.Default(ctx)
	}
	if err != nil {
		return Route{}, err
	}
	if !c.Enabled || c.Removed {
		return Route{}, ErrUnavailable
	}
	r.mu.RLock()
	adapter := r.bound[c.ID]
	r.mu.RUnlock()
	if adapter == nil || !adapter.Available(ctx) {
		return Route{}, fmt.Errorf("%w: selected adapter is not ready", ErrUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return Route{}, err
	}
	return Route{Connection: c, Platform: adapter}, nil
}
