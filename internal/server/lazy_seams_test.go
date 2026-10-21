package server

// set stores v; only valid before the first get.
func (l *lazy[T]) set(v T) { l.v = v }
