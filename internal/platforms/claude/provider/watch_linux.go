package claudeprovider

import (
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// dirWatcher holds one inotify watch per directory, never per file. Linux
// reports writes to entries of a watched directory, including appends.
type dirWatcher struct {
	fd   int
	file *os.File
	mu   sync.Mutex
	dirs map[string]bool
}

func newDirWatcher(notify func()) (*dirWatcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	// Non-blocking, so reads go through the runtime poller and Close
	// unblocks them.
	w := &dirWatcher{fd: fd, file: os.NewFile(uintptr(fd), "inotify"), dirs: map[string]bool{}}
	go func() {
		buf := make([]byte, 64<<10)
		for {
			if _, err := w.file.Read(buf); err != nil {
				return
			}
			notify()
		}
	}()
	return w, nil
}

func (w *dirWatcher) Add(dir string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dirs[dir] {
		return
	}
	mask := uint32(unix.IN_CREATE | unix.IN_MODIFY | unix.IN_MOVED_TO | unix.IN_MOVED_FROM | unix.IN_DELETE)
	if _, err := unix.InotifyAddWatch(w.fd, dir, mask); err == nil {
		w.dirs[dir] = true
	}
}

func (w *dirWatcher) Close() { _ = w.file.Close() }
