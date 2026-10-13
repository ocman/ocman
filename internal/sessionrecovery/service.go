package sessionrecovery

import (
	"context"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// SessionReader supplies owner-local persisted identities and abort metadata.
// Live status still comes from the platform's lifecycle reader.
type SessionReader interface {
	GetSessionIdentities(context.Context) ([]db.Session, error)
	GetSessionLifecycle(context.Context, string) (db.SessionLifecycle, error)
}

type Deps struct {
	State                   *state.DB
	Sessions                SessionReader
	Platform                func(platforms.ID) (platforms.Platform, bool)
	ManagedRootReader       func() hostsvc.ManagedRootReader
	MachineWide             func() bool
	BroadcastSessionChanged func(string)
	InjectHistoryNotice     func(string, string, int64, json.RawMessage, *[]db.Message, *[]db.Part)
}

// Service records interruption history and fences runtime replacement. It owns
// no workers or caches; durable progress remains in state.
type Service struct{ deps Deps }

func New(deps Deps) *Service { return &Service{deps: deps} }
