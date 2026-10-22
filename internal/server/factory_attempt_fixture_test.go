package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
)

// createPreparedFactoryAttempt inserts a prepared attempt without the claim's
// capacity and graph checks, so tests can bind sessions to Factory work.
func createPreparedFactoryAttempt(ctx context.Context, store *state.DB, epicID, workID string, policy model.FactoryAttemptPolicy, at time.Time) (model.FactoryAttempt, error) {
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return model.FactoryAttempt{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return model.FactoryAttempt{}, err
	}
	id := "fa_" + hex.EncodeToString(random[:])
	now := at.UnixMilli()
	if _, err := store.SQL().ExecContext(ctx, `INSERT INTO factory_attempt
		(id, epic_id, work_item_id, sequence, phase, frozen_policy_json, created_at, updated_at)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(sequence), 0) + 1 FROM factory_attempt WHERE work_item_id = ?), 'prepared', ?, ?, ?)`,
		id, epicID, workID, workID, string(policyJSON), now, now); err != nil {
		return model.FactoryAttempt{}, err
	}
	return model.FactoryAttempt{ID: id, EpicID: epicID, WorkID: workID, Phase: model.FactoryAttemptPrepared, FrozenPolicy: policy, CreatedAt: now, UpdatedAt: now}, nil
}
