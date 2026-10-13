package hostsvc

import "context"

// IdleProjectRuntime manages owner-local, per-project instances. Machine-wide
// servers and externally started instances are never candidates.
type IdleProjectRuntime interface {
	ManagedIdleProjects(context.Context) ([]string, error)
	CullIdleProject(context.Context, string, []string, func(context.Context) (bool, error)) (bool, error)
}
