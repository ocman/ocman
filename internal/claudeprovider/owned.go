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
// reload it when it was replaced.

// syncOwnedLocked reloads ownership written by another supervisor.
func (l *live) syncOwnedLocked() {
	if l.opts.StatePath == "" {
		return
	}
	info, err := os.Stat(l.opts.StatePath)
	if err != nil {
		return
	}
	// Every write renames a new file, so a changed inode or mtime is a change.
	if l.ownedInfo != nil && os.SameFile(info, l.ownedInfo) && info.ModTime().Equal(l.ownedInfo.ModTime()) {
		return
	}
	data, err := os.ReadFile(l.opts.StatePath)
	if err != nil {
		return
	}
	owned := map[string]*owned{}
	if json.Unmarshal(data, &owned) == nil {
		l.owned, l.ownedInfo = owned, info
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
	info, err := os.Stat(path)
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
	l.owned, l.ownedInfo = latest, info
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
	l.ownedInfo, _ = os.Stat(path)
	return nil
}

// OwnedDirectory reports the directory of a session ocman launched, read
// from statePath. ocman routes such a session to its project supervisor;
// any other session was started in a terminal and stays read-only.
func OwnedDirectory(statePath, id string) (string, bool) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return "", false
	}
	var all map[string]owned
	if json.Unmarshal(data, &all) != nil {
		return "", false
	}
	o, ok := all[id]
	return o.Directory, ok
}
