package server

import (
	"net/http"
	"sync"
)

// requestFence tracks every admitted HTTP request. Shutdown may force a stuck
// connection closed, but a handler can keep running detached work (a
// confirmed Factory action writes its receipt under WithoutCancel); close
// stops admission and waits for those handlers before their dependencies are
// torn down.
type requestFence struct {
	mu      sync.Mutex
	stopped bool
	wg      sync.WaitGroup
}

func (f *requestFence) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if f.stopped {
			f.mu.Unlock()
			http.Error(w, "server is shutting down", http.StatusServiceUnavailable)
			return
		}
		f.wg.Add(1)
		f.mu.Unlock()
		defer f.wg.Done()
		next.ServeHTTP(w, r)
	})
}

// close refuses new requests and waits for admitted ones.
func (f *requestFence) close() {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
	f.wg.Wait()
}
