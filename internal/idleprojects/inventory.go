package idleprojects

import (
	"context"
	"errors"
	"fmt"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
)

// inventory is sweep-local. The stop callback always builds a new one, including
// directory membership, before making fresh lifecycle and intent reads.
type inventory struct {
	sessions map[string][]db.Session
	resolve  func(string) (string, error)
}

func projectInventory(ctx context.Context, target Target) (inventory, error) {
	type resolution struct {
		root string
		err  error
	}
	resolved := map[string]resolution{}
	result := inventory{sessions: map[string][]db.Session{}}
	result.resolve = func(directory string) (string, error) {
		if found, ok := resolved[directory]; ok {
			return found.root, found.err
		}
		root, err := target.Runtime.ProjectRoot(ctx, directory)
		resolved[directory] = resolution{root, err}
		return root, err
	}
	all, err := target.Platform.Sessions(ctx, "", 0)
	if err != nil {
		return inventory{}, err
	}
	var history map[string]string
	historyRead := false
	for _, session := range all {
		owner, err := result.resolve(session.Directory)
		if errors.Is(err, hostsvc.ErrMissingProjectDirectory) {
			if !historyRead {
				history, err = target.Runtime.HistoricalSessionRoots(ctx)
				if err != nil {
					return inventory{}, err
				}
				historyRead = true
			}
			var owned bool
			owner, owned = history[session.ID]
			err = nil
			if owned && owner == "" {
				err = fmt.Errorf("historical project membership is unknown for session %s", session.ID)
			}
		}
		if err != nil {
			return inventory{}, err
		}
		result.sessions[owner] = append(result.sessions[owner], session)
	}
	return result, nil
}
