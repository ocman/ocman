package server

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/httpx"
)

const archiveResurfaceKey = "session.archive_resurface"

func (s *Server) archiveResurfaceMode(ctx context.Context) (string, error) {
	value, _, err := s.stateDB.GetSetting(ctx, archiveResurfaceKey)
	if value != "activity" {
		value = "halt"
	}
	return value, err
}

type archiveResurfaceBody struct {
	Mode string `json:"mode"`
}

func (s *Server) archiveResurfaceSetting() httpx.SettingRoute[archiveResurfaceBody] {
	return httpx.SettingRoute[archiveResurfaceBody]{
		Name:      "archive resurfacing setting",
		Available: s.hasStateDB,
		Get: func(ctx context.Context) (any, error) {
			mode, err := s.archiveResurfaceMode(ctx)
			return archiveResurfaceBody{mode}, err
		},
		Validate: func(body *archiveResurfaceBody) error {
			if body.Mode != "activity" && body.Mode != "halt" {
				return errors.New("mode must be activity or halt")
			}
			return nil
		},
		Set: func(ctx context.Context, body archiveResurfaceBody) error {
			return s.stateDB.SetSetting(ctx, archiveResurfaceKey, body.Mode)
		},
	}
}
