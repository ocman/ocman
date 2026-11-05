package hostsvc

import "context"

// WorktreeCheckpoint preserves enough identity to recover the exact checkout.
type WorktreeCheckpoint struct {
	Project string `json:"project"`
	Path    string `json:"path"`
	Branch  string `json:"branch"`
	Head    string `json:"head"`
}

type WorktreeSessionCheck struct {
	Platform    string `json:"platform"`
	SessionID   string `json:"sessionId"`
	TimeUpdated int64  `json:"timeUpdated"`
}

type CleanupWorktreeRequest struct {
	WorktreeCheckpoint
	Sessions []WorktreeSessionCheck `json:"sessions"`
}

// WorktreeMaintenance is deliberately separate from force-capable manual
// removal. Older owners answer unsupported instead of ignoring safety options.
type WorktreeMaintenance interface {
	CleanupWorktree(context.Context, CleanupWorktreeRequest) error
	RestoreWorktree(context.Context, WorktreeCheckpoint) error
}
