package server

import (
	"context"
	"fmt"
	"strings"

	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
)

func mcpSelectionError(format string, args ...any) error {
	return fmt.Errorf("%w: %s; ask the user to clarify the requested platform/model; do not omit it or substitute defaults", internalmcp.ErrInvalidSessionCreate, fmt.Sprintf(format, args...))
}

// Explicit models must resolve on the destination before any runtime or worktree
// is launched. Admission of an asynchronous prompt is not proof it can execute.
func (s sessionMCPService) validateCreateModel(ctx context.Context, platformID, directory, ref string) error {
	catalog, _, err := s.server.sessions.DirectoryCatalog(ctx, platformID, platforms.DirectoryCatalogRequest{Directory: directory})
	if err != nil || catalog == nil || catalog.Models == nil {
		return mcpSelectionError("cannot verify model %q on platform %q", ref, platformID)
	}
	_, model := platforms.SplitModel(ref)
	model, _, _ = strings.Cut(model, "#")
	for _, entry := range catalog.Models.Models {
		if entry.Provider+"/"+entry.Model == model && entry.IsAvailable {
			return nil
		}
	}
	return mcpSelectionError("model %q is not available on platform %q", ref, platformID)
}
