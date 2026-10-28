package analytics

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/ocman/ocman/internal/state"
)

// Schema owns the local cost ledger. No credentials or permission content are stored.
var Schema = state.ModuleSchema{
	Module: "analytics",
	Tables: []string{"provider_usage"},
	Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE provider_usage (
			created_at INTEGER NOT NULL, model TEXT NOT NULL,
			input_tokens INTEGER, output_tokens INTEGER, estimated_usd REAL
		); CREATE INDEX provider_usage_created ON provider_usage(created_at);`)
		return err
	}},
}

type providerSpend struct {
	EstimatedUSD     *float64 `json:"estimatedUSD"`
	InputTokens      int64    `json:"inputTokens"`
	OutputTokens     int64    `json:"outputTokens"`
	Requests         int64    `json:"requests"`
	UnpricedRequests int64    `json:"unpricedRequests"`
	PeriodStart      string   `json:"periodStart"`
	PeriodEnd        string   `json:"periodEnd"`
}

// RecordTypeSafeUsage records successful hosted API responses, even when their
// verdict is invalid. Unknown models or missing counts remain explicitly unpriced.
func (m *Module) RecordTypeSafeUsage(ctx context.Context, model string, input, output *int64) error {
	if m.deps.Store == nil {
		return nil
	}
	return recordTypeSafeUsage(ctx, m.deps.Store.SQL(), time.Now(), model, input, output)
}

func recordTypeSafeUsage(ctx context.Context, db *sql.DB, now time.Time, model string, input, output *int64) error {
	if input != nil && *input < 0 {
		input = nil
	}
	if output != nil && *output < 0 {
		output = nil
	}
	var cost *float64
	// https://docs.typesafe.ai/models: $0.042/M input tokens, output free.
	// Do not assume future releases, aliases or compatible servers have this price.
	if input != nil && (model == "jev-1.13.0" || model == "jev-1.14.0") {
		usd := float64(*input) * .042 / 1_000_000
		cost = &usd
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_usage WHERE created_at < ?`, now.Add(-7*24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO provider_usage VALUES (?, ?, ?, ?, ?)`, now.UnixMilli(), model, input, output, cost); err != nil {
		return err
	}
	return tx.Commit()
}

func readTypeSafeSpend(ctx context.Context, db *sql.DB, now time.Time) (*providerSpend, error) {
	since := now.Add(-7 * 24 * time.Hour)
	spend := &providerSpend{PeriodStart: since.UTC().Format(time.RFC3339), PeriodEnd: now.UTC().Format(time.RFC3339)}
	var cost sql.NullFloat64
	err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(input_tokens), 0), coalesce(sum(output_tokens), 0),
		sum(estimated_usd), count(*) - count(estimated_usd) FROM provider_usage WHERE created_at >= ? AND created_at <= ?`,
		since.UnixMilli(), now.UnixMilli()).Scan(&spend.Requests, &spend.InputTokens, &spend.OutputTokens, &cost, &spend.UnpricedRequests)
	if cost.Valid {
		spend.EstimatedUSD = &cost.Float64
	}
	return spend, err
}

func (m *Module) appendTypeSafeUsage(ctx context.Context, usage *subscriptionUsageResponse) error {
	if m.deps.Store == nil || m.deps.TypeSafeConfigured == nil {
		return nil
	}
	configured, err := m.deps.TypeSafeConfigured(ctx)
	if err != nil {
		return err
	}
	if !configured {
		return nil
	}
	spend, err := readTypeSafeSpend(ctx, m.deps.Store.SQL(), time.Now())
	if err != nil {
		return err
	}
	usage.Providers = append(slices.Clone(usage.Providers), subscriptionProviderUsage{
		ID: "typesafe", Name: "TypeSafe", Status: "ok", Windows: []subscriptionUsageWindow{}, Spend: spend,
	})
	return nil
}
