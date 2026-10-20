package server

import (
	"context"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/sessionview"
)

func publicSessionValue[T any](s *Server, ctx context.Context, platform string, value T, rootSessions bool) (T, error) {
	if s.sessionCatalogue == nil {
		return value, nil
	}
	var zero T
	raw, err := json.Marshal(value)
	if err != nil {
		return zero, err
	}
	shape := sessionview.ConversationShape
	if rootSessions {
		shape = sessionview.SessionShape
	}
	p := s.publicSessionProjector(platform, shape, rootSessions)
	raw, err = p.JSON(ctx, raw)
	if err != nil {
		return zero, err
	}
	var projected T
	if err := json.Unmarshal(raw, &projected); err != nil {
		return zero, err
	}
	return projected, nil
}

func (s *Server) publicSessionProjector(platform string, shape *sessionview.Shape, omitUnresolvedRows bool) sessionview.Projector {
	return sessionview.Projector{Platform: platform, Shape: shape, OmitUnresolvedRows: omitUnresolvedRows, Lookup: func(ctx context.Context, platform string, ids []string) (map[string]string, error) {
		rows, err := s.sessions.LookupSessionIdentities(ctx, platform, ids)
		if err != nil {
			return nil, err
		}
		out := make(map[string]string, len(rows))
		for _, row := range rows {
			out[row.NativeID] = row.ID
		}
		return out, nil
	}}
}
