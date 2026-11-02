package claudeprovider

import (
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// dirWatcher holds one kqueue vnode watch per directory, never per file.
// A directory event fires for created, removed and renamed entries; appends
// to an existing transcript are left to the mtime sweep.
type dirWatcher struct {
	kq    int
	mu    sync.Mutex
	fds   map[string]int
	paths map[uint64]string
	stop  chan struct{}
	done  chan struct{}
}

func newDirWatcher(notify func(string)) (*dirWatcher, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	w := &dirWatcher{kq: kq, fds: map[string]int{}, paths: map[uint64]string{}, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		events := make([]unix.Kevent_t, 16)
		timeout := unix.NsecToTimespec(int64(250 * time.Millisecond))
		for {
			select {
			case <-w.stop:
				return
			default:
			}
			if n, _ := unix.Kevent(kq, nil, events, &timeout); n > 0 {
				for _, event := range events[:n] {
					w.mu.Lock()
					path := w.paths[event.Ident]
					if event.Fflags&(unix.NOTE_DELETE|unix.NOTE_RENAME) != 0 {
						delete(w.paths, event.Ident)
						delete(w.fds, path)
						_ = unix.Close(int(event.Ident))
					}
					w.mu.Unlock()
					notify(path)
				}
			}
		}
	}()
	return w, nil
}

// ponytail: a removed directory keeps its fd until Close; drop it on
// NOTE_DELETE if long-running instances accumulate deleted projects.
func (w *dirWatcher) Add(dir string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.fds[dir]; ok {
		return
	}
	fd, err := unix.Open(dir, unix.O_EVTONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	var ev unix.Kevent_t
	unix.SetKevent(&ev, fd, unix.EVFILT_VNODE, unix.EV_ADD|unix.EV_CLEAR)
	ev.Fflags = unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME
	if _, err := unix.Kevent(w.kq, []unix.Kevent_t{ev}, nil, nil); err != nil {
		unix.Close(fd)
		return
	}
	w.fds[dir] = fd
	w.paths[uint64(fd)] = dir
}

func (w *dirWatcher) Close() {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, fd := range w.fds {
		unix.Close(fd)
	}
	unix.Close(w.kq)
}
