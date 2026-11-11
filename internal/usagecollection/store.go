package usagecollection

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/usagewire"
)

const EnabledSetting = "usage.collection_enabled"

var Schema = state.ModuleSchema{
	Module: "usage-collection", Tables: []string{"usage_collection"},
	Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE usage_collection (
		 id INTEGER PRIMARY KEY CHECK(id=1), installation_id TEXT NOT NULL,
		 pending_date TEXT NOT NULL DEFAULT '', sent_date TEXT NOT NULL DEFAULT '')`)
		return err
	}},
}

func (m *Module) record(ctx context.Context, day string) error {
	_, err := m.deps.Store.SQL().ExecContext(ctx, `INSERT INTO usage_collection(id,installation_id,pending_date)
	 VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET pending_date=excluded.pending_date
	 WHERE usage_collection.sent_date<>excluded.pending_date`, uuid.NewString(), day)
	return err
}

func (m *Module) pending(ctx context.Context, now time.Time) (*usagewire.Report, error) {
	var report usagewire.Report
	err := m.deps.Store.SQL().QueryRowContext(ctx, `SELECT installation_id,pending_date FROM usage_collection WHERE id=1`).Scan(&report.InstallationID, &report.Date)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if report.Date == "" {
		return nil, nil
	}
	if report.Date != now.UTC().Format(time.DateOnly) {
		return nil, m.clearPending(ctx) // Do not replay offline activity or obsolete dates.
	}
	report.Version = usagewire.NormalizeVersion(m.deps.Version())
	return &report, nil
}

func (m *Module) clearPending(ctx context.Context) error {
	_, err := m.deps.Store.SQL().ExecContext(ctx, `UPDATE usage_collection SET pending_date='' WHERE id=1`)
	return err
}

func (m *Module) acknowledge(ctx context.Context, report *usagewire.Report) error {
	_, err := m.deps.Store.SQL().ExecContext(ctx, `UPDATE usage_collection SET sent_date=?,pending_date=''
	 WHERE id=1 AND pending_date=?`, report.Date, report.Date)
	return err
}
