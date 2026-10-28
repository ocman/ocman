package sessionsvc

import (
	"context"
	"sync"

	"github.com/ocman/ocman/internal/platforms"
)

type creationKey struct{ owner, request string }

type creationLock struct {
	gate chan struct{}
	refs int
}

// One owner/request operation holds its lease through native configuration,
// application linkage and publication. Waiters reread the durable receipt only
// after acquiring the lease. Independent requests and owners remain concurrent.
type creationLocks struct {
	mu      sync.Mutex
	entries map[creationKey]*creationLock
}

func (s *Service) lockCreation(ctx context.Context, platform platforms.ID, request string) (func(), error) {
	owner, _ := platforms.SplitPlatformID(string(platform))
	return s.creations.acquire(ctx, creationKey{owner: owner, request: request})
}

func (locks *creationLocks) acquire(ctx context.Context, key creationKey) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	locks.mu.Lock()
	if locks.entries == nil {
		locks.entries = make(map[creationKey]*creationLock)
	}
	entry := locks.entries[key]
	if entry == nil {
		entry = &creationLock{gate: make(chan struct{}, 1)}
		locks.entries[key] = entry
	}
	entry.refs++
	locks.mu.Unlock()

	select {
	case entry.gate <- struct{}{}:
		var once sync.Once
		unlock := func() { once.Do(func() { locks.release(key, entry, true) }) }
		if err := ctx.Err(); err != nil {
			unlock()
			return nil, err
		}
		return unlock, nil
	case <-ctx.Done():
		locks.release(key, entry, false)
		return nil, ctx.Err()
	}
}

func (locks *creationLocks) release(key creationKey, entry *creationLock, held bool) {
	locks.mu.Lock()
	defer locks.mu.Unlock()
	if held {
		<-entry.gate
	}
	entry.refs--
	if entry.refs == 0 {
		delete(locks.entries, key)
	}
}
