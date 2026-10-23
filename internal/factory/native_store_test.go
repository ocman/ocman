package factory

import (
	"context"
	"database/sql"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

// Empty read results for the lightweight store fake. Methods not stubbed by a
// test still panic through its embedded interface rather than hiding a write.
func (*nativeStoreFake) ListFactoryAttempts(context.Context, string) ([]model.FactoryAttempt, error) {
	return nil, nil
}
func (*nativeStoreFake) GetFactoryPlanGate(context.Context, string) (model.NativePlanGate, error) {
	return model.NativePlanGate{}, sql.ErrNoRows
}
func (*nativeStoreFake) GetFactoryProposalRevision(context.Context, string, int) (model.NativeProposalRevision, error) {
	return model.NativeProposalRevision{}, sql.ErrNoRows
}
func (*nativeStoreFake) GetFactoryEpicModels(context.Context, string) (model.EpicModels, error) {
	return model.EpicModels{}, nil
}
func (*nativeStoreFake) GetFactoryRecoveryGate(context.Context, string) (model.RecoveryGate, bool, error) {
	return model.RecoveryGate{}, false, nil
}
func (*nativeStoreFake) GetFactoryAuthorityEscalationGate(context.Context, string) (model.AuthorityEscalationGate, bool, error) {
	return model.AuthorityEscalationGate{}, false, nil
}
func (*nativeStoreFake) SyncFactoryActionInbox(context.Context, string, string, map[string]string) ([]string, error) {
	return nil, nil
}
func (*nativeStoreFake) NextFactoryDispatchAt(context.Context, time.Duration) (time.Time, error) {
	return time.Time{}, nil
}
func (s *nativeStoreFake) ListFactoryDispatchEpics(ctx context.Context) ([]model.NativeEpic, error) {
	return s.ListFactoryEpics(ctx)
}

func (*nativeStoreFake) ReopenFactoryIssue(context.Context, string, string) error {
	return ErrFactoryUnavailable
}

func (*nativeStoreFake) MaterializeFactoryPlan(context.Context, string, string, string, time.Time) (model.NativeMaterialization, error) {
	return model.NativeMaterialization{}, ErrFactoryUnavailable
}

func (*nativeStoreFake) SetFactoryEpicPaused(context.Context, string, bool) error {
	return ErrFactoryUnavailable
}

func (verificationStoreFake) FactoryAttemptLastResumedAt(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}
