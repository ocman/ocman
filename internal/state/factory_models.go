package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type factoryRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// GetFactoryEpicModels returns the Epic's per-phase model choices.
func (d *DB) GetFactoryEpicModels(ctx context.Context, epicID string) (model.EpicModels, error) {
	return getFactoryEpicModels(ctx, d.db, epicID)
}

func getFactoryEpicModels(ctx context.Context, q factoryRowQueryer, epicID string) (model.EpicModels, error) {
	var raw string
	var models model.EpicModels
	if err := q.QueryRowContext(ctx, `SELECT models_json FROM factory_epic WHERE id = ?`, epicID).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
		return models, model.ErrNativeEpicNotFound
	} else if err != nil {
		return models, fmt.Errorf("reading Factory Epic models: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return models, fmt.Errorf("decoding Factory Epic models: %w", err)
	}
	return models, nil
}

// SetFactoryEpicModels replaces the Epic's per-phase model choices. Claimed
// attempts keep the model frozen in their policy.
func (d *DB) SetFactoryEpicModels(ctx context.Context, epicID string, models model.EpicModels) error {
	raw, err := json.Marshal(models)
	if err != nil {
		return err
	}
	result, err := d.db.ExecContext(ctx, `UPDATE factory_epic SET models_json = ? WHERE id = ?`, string(raw), epicID)
	if err != nil {
		return fmt.Errorf("storing Factory Epic models: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return model.ErrNativeEpicNotFound
	}
	return nil
}
