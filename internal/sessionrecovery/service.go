package sessionrecovery

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

type Deps struct {
	State *state.DB
	// Sessions supplies owner-local persisted identities and abort metadata.
	// It is required: replacement fails closed without it, because the
	// display list can omit inactive children or newly created sessions.
	// Live status still comes from the platform's lifecycle reader.
	Sessions                platforms.RuntimeSessionReader
	Platform                func(platforms.ID) (platforms.Platform, bool)
	BroadcastSessionChanged func(string)
	InjectHistoryNotice     func(string, string, int64, json.RawMessage, *[]db.Message, *[]db.Part)
}

// Service records interruption history and fences runtime replacement. It owns
// no workers or caches; durable progress remains in state.
type Service struct{ deps Deps }

func New(deps Deps) *Service { return &Service{deps: deps} }

// ErrNoSessionReader reports a Service built without Deps.Sessions.
var ErrNoSessionReader = errors.New("session recovery: no owner-local session reader")

// sessionIdentities lists every owner-local session identity.
func (s *Service) sessionIdentities(ctx context.Context) ([]db.Session, error) {
	if s.deps.Sessions == nil {
		return nil, ErrNoSessionReader
	}
	return s.deps.Sessions.GetSessionIdentities(ctx)
}
