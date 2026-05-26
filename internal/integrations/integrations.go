// Package integrations holds third-party service integrations (GitHub, etc.).
// Each integration is registered at server startup; the server exposes them
// under /api/integrations/<id>/.
package integrations

import (
	"github.com/NoUseFreak/ocman/internal/integrations/github"
)

// Registry holds all configured integrations.
type Registry struct {
	GitHub *github.Client
}

// New creates a Registry and initialises every integration.
func New() *Registry {
	return &Registry{
		GitHub: github.New(),
	}
}
