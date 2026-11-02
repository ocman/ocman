package claudeprovider

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// dirWatcher holds one inotify watch per directory, never per file. Linux
// reports writes to entries of a watched directory, including appends.
type dirWatcher struct {
	fd    int
	file  *os.File
	mu    sync.Mutex
	dirs  map[string]bool
	paths map[int32]string
}

func newDirWatcher(notify func(string)) (*dirWatcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, err
	}
	// Non-blocking, so reads go through the runtime poller and Close
	// unblocks them.
	w := &dirWatcher{fd: fd, file: os.NewFile(uintptr(fd), "inotify"), dirs: map[string]bool{}, paths: map[int32]string{}}
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := w.file.Read(buf)
			if err != nil {
				return
			}
			for offset := 0; offset+unix.SizeofInotifyEvent <= n; {
				b := buf[offset:]
				wd := int32(binary.NativeEndian.Uint32(b))
				mask, size := binary.NativeEndian.Uint32(b[4:]), int(binary.NativeEndian.Uint32(b[12:]))
				end := offset + unix.SizeofInotifyEvent + size
				if end > n {
					notify("")
					break
				}
				w.mu.Lock()
				path := w.paths[wd]
				if mask&unix.IN_IGNORED != 0 {
					delete(w.paths, wd)
					delete(w.dirs, path)
				}
				w.mu.Unlock()
				if mask&unix.IN_Q_OVERFLOW != 0 {
					path = ""
				} else if size > 0 && path != "" {
					path = filepath.Join(path, strings.TrimRight(string(buf[offset+unix.SizeofInotifyEvent:end]), "\x00"))
				}
				notify(path)
				offset = end
			}
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
	if wd, err := unix.InotifyAddWatch(w.fd, dir, mask); err == nil {
		w.dirs[dir] = true
		w.paths[int32(wd)] = dir
	}
}

func (w *dirWatcher) Close() { _ = w.file.Close() }
