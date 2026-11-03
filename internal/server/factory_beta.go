package server

import (
	"context"

	"github.com/ocman/ocman/internal/httpx"
	log "github.com/sirupsen/logrus"
)

const factoryBetaKey = "beta.factory"

type betaFeaturesView struct {
	Factory bool `json:"factory"`
}

func (s *Server) factoryBetaEnabled(ctx context.Context) bool {
	if s.stateDB == nil {
		return false
	}
	value, _, err := s.stateDB.GetSetting(ctx, factoryBetaKey)
	return err == nil && value == "true"
}

func (s *Server) betaFeaturesSetting() httpx.SettingRoute[betaFeaturesView] {
	return httpx.SettingRoute[betaFeaturesView]{
		Name: "beta features", Available: s.hasStateDB,
		Get: func(ctx context.Context) (any, error) {
			value, _, err := s.stateDB.GetSetting(ctx, factoryBetaKey)
			return betaFeaturesView{Factory: value == "true"}, err
		},
		Set: func(ctx context.Context, body betaFeaturesView) error {
			value := "false"
			if body.Factory {
				value = "true"
			}
			if err := s.stateDB.SetSetting(ctx, factoryBetaKey, value); err != nil {
				return err
			}
			// The setting is saved; a dispatch start failure is retried on
			// the next save rather than reported as a failed save.
			if err := s.factoryModule().SyncEnabled(context.WithoutCancel(ctx)); err != nil {
				log.WithError(err).Error("starting Factory dispatch")
			}
			return nil
		},
	}
}
