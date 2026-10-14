package server

import "sync"

// lazy holds a value built once, on first use (see "Feature modules" in
// AGENTS.md). A test may store one with set before that first use; the
// builder then never runs.
type lazy[T comparable] struct {
	once sync.Once
	v    T
}

// get returns the stored value, building it on the first call unless one
// was set. A builder may return the zero value (a module that needs a
// state DB); that answer is kept too.
func (l *lazy[T]) get(build func() T) T {
	l.once.Do(func() {
		var zero T
		if l.v == zero {
			l.v = build()
		}
	})
	return l.v
}

// set stores v; only valid before the first get.
func (l *lazy[T]) set(v T) { l.v = v }
