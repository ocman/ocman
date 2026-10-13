package sessionrecovery

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// Consult before refreshing preparation, discovery or current runtime rows.
func (s *Service) OpencodeReplacementStopping(ctx context.Context, root string) (*ocruntime.Instance, error) {
	if s.deps.State == nil {
		return nil, nil
	}
	inst, pending, err := s.deps.State.SessionReplacementStopping(ctx, "opencode", root)
	if err != nil || !pending {
		return nil, err
	}
	return &ocruntime.Instance{Endpoint: inst.Endpoint, Kind: inst.Kind, ID: inst.RuntimeID, PID: inst.PID, RepoRoot: inst.RepoRoot}, nil
}

func (s *Service) CancelOpencodeReplacementStop(ctx context.Context, root string) error {
	if s.deps.State == nil {
		return nil
	}
	return s.deps.State.CancelSessionReplacementStop(ctx, "opencode", root)
}
