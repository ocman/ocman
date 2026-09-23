package factory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type nativeModelsStore interface {
	GetFactoryEpicModels(context.Context, string) (model.EpicModels, error)
	SetFactoryEpicModels(context.Context, string, model.EpicModels) error
}

// validModel accepts "" (use the fallback) or a "provider/model" reference.
func validModel(value string) bool {
	if value == "" {
		return true
	}
	provider, name, ok := strings.Cut(value, "/")
	return ok && provider != "" && name != "" && len(value) <= 300 && !strings.ContainsAny(value, " \t\r\n")
}

// SetEpicModels saves the Epic's per-phase models. Attempts already claimed
// keep the model frozen in their policy, so only unstarted work changes.
func (s *NativeService) SetEpicModels(ctx context.Context, epicID string, models model.EpicModels) (model.EpicModels, error) {
	store, ok := s.store.(nativeModelsStore)
	if !ok {
		return model.EpicModels{}, ErrFactoryUnavailable
	}
	models = model.EpicModels{Plan: strings.TrimSpace(models.Plan), Implementation: strings.TrimSpace(models.Implementation), Verification: strings.TrimSpace(models.Verification)}
	for _, value := range []string{models.Plan, models.Implementation, models.Verification} {
		if !validModel(value) {
			return model.EpicModels{}, fmt.Errorf("%w: models must be provider/model", ErrInvalidRequest)
		}
	}
	err := store.SetFactoryEpicModels(ctx, epicID, models)
	if errors.Is(err, model.ErrNativeEpicNotFound) {
		err = ErrWorkEpicNotFound
	}
	return models, err
}

func (s *NativeService) epicModels(ctx context.Context, epicID string) model.EpicModels {
	if store, ok := s.store.(nativeModelsStore); ok {
		if models, err := store.GetFactoryEpicModels(ctx, epicID); err == nil {
			return models
		}
	}
	return model.EpicModels{}
}
