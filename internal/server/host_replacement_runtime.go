package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// Consult before refreshing preparation, discovery or current runtime rows.
func (s *Server) opencodeReplacementStopping(ctx context.Context, root string) (*ocruntime.Instance, error) {
	if s.stateDB == nil {
		return nil, nil
	}
	inst, pending, err := s.stateDB.SessionReplacementStopping(ctx, "opencode", root)
	if err != nil || !pending {
		return nil, err
	}
	return &ocruntime.Instance{Endpoint: inst.Endpoint, Kind: inst.Kind, ID: inst.RuntimeID, PID: inst.PID, RepoRoot: inst.RepoRoot}, nil
}

func (s *Server) cancelOpencodeReplacementStop(ctx context.Context, root string) error {
	if s.stateDB == nil {
		return nil
	}
	return s.stateDB.CancelSessionReplacementStop(ctx, "opencode", root)
}
