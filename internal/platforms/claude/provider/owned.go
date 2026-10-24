package claudeprovider

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Ownership (claude-sessions.json) is shared by the browsing supervisor and
// every project supervisor (AD-3): each one drives only its own sessions,
// but all of them list, title and route every session ocman launched.
// Writers serialize on an flock and replace the file atomically; readers
// reload the shared snapshot so reused inodes or coarse mtimes cannot hide writes.

// syncOwnedLocked reloads ownership written by another supervisor.
func (l *live) syncOwnedLocked() {
	if l.opts.StatePath == "" {
		return
	}
	data, err := os.ReadFile(l.opts.StatePath)
	if err != nil {
		return
	}
	owned := map[string]*owned{}
	if json.Unmarshal(data, &owned) == nil {
		l.owned = owned
	}
}

// updateOwnedLocked applies change to the latest ownership under the
// cross-process lock and persists it, so a concurrent supervisor's write is
// never lost and a restarted ocman keeps driving its sessions. It never
// writes without the lock, and a failure leaves memory as it was on disk.
func (l *live) updateOwnedLocked(change func()) error {
	path := l.opts.StatePath
	if path == "" {
		change()
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("session ownership: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("session ownership lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("session ownership lock: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	latest := map[string]*owned{}
	_, err = os.Stat(path)
	switch {
	case err == nil:
		data, err := os.ReadFile(path)
		if err == nil {
			err = json.Unmarshal(data, &latest)
		}
		if err != nil {
			return fmt.Errorf("session ownership %s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("session ownership: %w", err)
	}
	// change runs on a private copy: memory follows only a durable write.
	l.owned = latest
	data, _ := json.Marshal(latest)
	change()
	updated, _ := json.Marshal(l.owned)
	tmp := path + ".tmp"
	err = os.WriteFile(tmp, updated, 0o600)
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		l.owned = map[string]*owned{}
		_ = json.Unmarshal(data, &l.owned)
		return fmt.Errorf("saving session ownership: %w", err)
	}
	return nil
}

// OwnedDirectory reports the directory of a session ocman launched or adopted,
// read from statePath. Such sessions route to their project supervisor.
func OwnedDirectory(statePath, id string) (string, bool) {
	o, ok := readOwnedSession(statePath, id)
	return o.Directory, ok
}

// OwnedProjectRoot identifies the supervisor that owns a launched/adopted
// session. Knowing a transcript from a shared index is not live ownership.
// The bool still reports ownership for legacy records without a project root.
func OwnedProjectRoot(statePath, id string) (string, bool) {
	o, ok := readOwnedSession(statePath, id)
	return o.ProjectRoot, ok
}

func readOwnedSession(statePath, id string) (owned, bool) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return owned{}, false
	}
	var all map[string]owned
	if json.Unmarshal(data, &all) != nil {
		return owned{}, false
	}
	o, ok := all[id]
	return o, ok
}
