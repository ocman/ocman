package statev2

// CommitObserver learns each session revision after its transaction commits.
// It runs on the committing goroutine and must not block or call back into
// the store; hand the notification to another goroutine instead.
type CommitObserver func(Key, int64)

// OnCommitted installs the observer for later commits, replacing any earlier
// one. A nil observer stops notifications.
func (d *DB) OnCommitted(fn CommitObserver) {
	if fn == nil {
		d.committed.Store(nil)
		return
	}
	d.committed.Store(&fn)
}

func (d *DB) notifyCommitted(key Key, revision int64) {
	if fn := d.committed.Load(); fn != nil && revision > 0 {
		(*fn)(key, revision)
	}
}
