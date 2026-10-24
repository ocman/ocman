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
	kq   int
	mu   sync.Mutex
	fds  map[string]int
	stop chan struct{}
	done chan struct{}
}

func newDirWatcher(notify func()) (*dirWatcher, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	w := &dirWatcher{kq: kq, fds: map[string]int{}, stop: make(chan struct{}), done: make(chan struct{})}
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
				notify()
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
