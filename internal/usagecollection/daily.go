package usagecollection

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/usagewire"
)

func (m *Module) sendDaily(ctx context.Context) {
	m.mu.Lock()
	day := m.now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	report, url := m.dailyReport(ctx, day)
	if report == nil {
		m.mu.Unlock()
		return
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	m.cancel = cancel
	m.nextDaily = m.now().Add(15 * time.Minute)
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.cancel = nil
		m.mu.Unlock()
	}()

	daily, err := m.deps.Daily(requestCtx, day)
	if err != nil || !daily.Valid() || requestCtx.Err() != nil {
		return
	}
	report.Daily = &daily
	if !m.post(requestCtx, url, report) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, _ = m.deps.Store.SQL().ExecContext(ctx, `UPDATE usage_collection SET daily_sent=? WHERE id=1`, report.Date)
}

// dailyReport runs under mu. The first eligible day is the next full UTC
// day after opt-in is observed. Never send historical usage on first enable.
func (m *Module) dailyReport(ctx context.Context, day time.Time) (*usagewire.Report, string) {
	if ctx.Err() != nil || m.now().Before(m.nextDaily) {
		return nil, ""
	}
	enabled, err := m.enabled(ctx)
	if err != nil {
		return nil, ""
	}
	if !enabled || m.deps.Daily == nil {
		_, _ = m.deps.Store.SQL().ExecContext(ctx, `UPDATE usage_collection SET daily_from='' WHERE id=1 AND daily_from<>''`)
		return nil, ""
	}
	url := endpoint(m.deps.RelayURL())
	if url == "" {
		return nil, ""
	}
	db := m.deps.Store.SQL()
	first := day.AddDate(0, 0, 2).Format(time.DateOnly)
	if _, err := db.ExecContext(ctx, `INSERT INTO usage_collection(id,installation_id,daily_from) VALUES(1,?,?)
		ON CONFLICT(id) DO UPDATE SET daily_from=excluded.daily_from WHERE daily_from=''`, uuid.NewString(), first); err != nil {
		return nil, ""
	}
	report := &usagewire.Report{Date: day.Format(time.DateOnly), Version: usagewire.NormalizeVersion(m.deps.Version())}
	var from, sent string
	if err := db.QueryRowContext(ctx, `SELECT installation_id,daily_from,daily_sent FROM usage_collection WHERE id=1`).Scan(&report.InstallationID, &from, &sent); err != nil || report.Date < from || report.Date <= sent {
		return nil, ""
	}
	return report, url + "/daily"
}
