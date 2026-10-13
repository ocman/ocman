//go:build !darwin && !linux

package claudeprovider

// dirWatcher is a no-op here; the mtime sweep keeps the index current.
type dirWatcher struct{}

func newDirWatcher(func()) (*dirWatcher, error) { return &dirWatcher{}, nil }
func (*dirWatcher) Add(string)                   {}
func (*dirWatcher) Close()                       {}
