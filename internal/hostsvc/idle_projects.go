package hostsvc

import (
	"context"
	"errors"
)

// ErrMissingProjectDirectory is definite absence, not an uncertain git probe.
var ErrMissingProjectDirectory = errors.New("project directory no longer exists")

// IdleProjectRuntime manages owner-local, per-project instances. Machine-wide
// servers and externally started instances are never candidates.
type IdleProjectRuntime interface {
	ProjectRoot(context.Context, string) (string, error)
	// Absent IDs are unmanaged; an empty root for a present ID is unknown.
	HistoricalSessionRoots(context.Context) (map[string]string, error)
	ManagedIdleProjects(context.Context) ([]string, error)
	CullIdleProject(context.Context, string, func(context.Context) (bool, error)) (bool, error)
}
